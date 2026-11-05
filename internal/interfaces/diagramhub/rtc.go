package diagramhub

import (
	"context"
	"encoding/json"
	"slices"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/topic"
)

// Audio rooms: signaling for a WebRTC mesh, over MQTT. The server never
// touches media: it keeps who is in each file's call (Redis slots, shared by
// the API instances), forwards offer / answer / ice between members of the
// same room and relays tiny mute and speaking flags. A room holds at most
// MaxPeers people (a mesh does not scale past a handful); one more is told
// rtc.full. Clients publish commands to dg/f/{file}/rtc/up/{client} and hear
// the answers on dg/f/{file}/rtc/{client}; the call roster is also published
// on the file's events topic.
//
// client -> server: rtc.join | rtc.leave | rtc.signal {to, kind, data} | rtc.state {muted, speaking}
// server -> client: rtc.joined {peers} | rtc.peer-joined {peer} | rtc.peer-left {clientId}
//
//	| rtc.signal {from, kind, data} | rtc.state {from, muted, speaking}
//	| rtc.full {code, max, count}
//
// file events: rtc.roster {fileId, peers} (the whole call)

const maxSignalBytes = 16_000

var signalKinds = []string{"offer", "answer", "ice"}

type rtcPeer struct {
	ClientID string  `json:"clientId"`
	UserID   *string `json:"userId"`
	Name     string  `json:"name"`
}

// who describes a member of a call from the account behind its client id.
func (h *Hub) who(ctx context.Context, fileID, clientID string) rtcPeer {
	if a, ok := h.access(ctx, clientID, fileID); ok {
		return rtcPeer{clientID, userRef(a.UserID), a.Name}
	}
	user, _ := topic.ParseClientID(clientID)
	return rtcPeer{clientID, userRef(user), "Someone"}
}

func (h *Hub) peers(ctx context.Context, fileID string, except string) []rtcPeer {
	members, err := h.o.Slots.Members(ctx, fileID)
	if err != nil {
		zap.L().Warn("diagramhub: audio room members", zap.Error(err))
	}
	out := make([]rtcPeer, 0, len(members))
	for _, m := range members {
		if m != except {
			out = append(out, h.who(ctx, fileID, m))
		}
	}
	return out
}

func (h *Hub) inbox(ctx context.Context, fileID, clientID string, v any) {
	h.publish(ctx, application.Message{Topic: topic.RTCInboxTopic(fileID, clientID), Payload: mustJSON(v), QoS: 1, Expiry: eventExpiry})
}

func (h *Hub) handleRTC(ctx context.Context, t topic.Topic, text []byte) {
	var m struct {
		Type string `json:"type"`
		To   string `json:"to"`
		Kind string `json:"kind"`
		Data json.RawMessage
		Mute *bool `json:"muted"`
		Spk  *bool `json:"speaking"`
	}
	if len(text) > maxSignalBytes+1024 || json.Unmarshal(text, &m) != nil {
		return
	}
	if _, ok := h.access(ctx, t.ClientID, t.FileID); !ok {
		return
	}
	switch m.Type {
	case "rtc.join":
		h.joinRTC(ctx, t.FileID, t.ClientID)
	case "rtc.leave":
		h.leaveRTC(ctx, t.FileID, t.ClientID)
	case "rtc.signal":
		h.signalRTC(ctx, t.FileID, t.ClientID, m.To, m.Kind, m.Data)
	case "rtc.state":
		h.stateRTC(ctx, t.FileID, t.ClientID, m.Mute != nil && *m.Mute, m.Spk != nil && *m.Spk)
	}
}

func (h *Hub) isMember(ctx context.Context, fileID, clientID string) bool {
	members, _ := h.o.Slots.Members(ctx, fileID)
	return slices.Contains(members, clientID)
}

func (h *Hub) joinRTC(ctx context.Context, fileID, clientID string) {
	if h.isMember(ctx, fileID, clientID) {
		return
	}
	ok, err := h.o.Slots.Acquire(ctx, fileID, clientID, h.o.MaxPeers)
	if err != nil {
		zap.L().Warn("diagramhub: audio slot", zap.Error(err))
		h.inbox(ctx, fileID, clientID, map[string]any{"type": "rtc.full", "code": "unavailable", "max": h.o.MaxPeers, "count": 0})
		return
	}
	others := h.peers(ctx, fileID, clientID)
	if !ok {
		h.inbox(ctx, fileID, clientID, map[string]any{"type": "rtc.full", "code": "room_full", "max": h.o.MaxPeers, "count": len(others)})
		return
	}
	h.inbox(ctx, fileID, clientID, map[string]any{"type": "rtc.joined", "peers": others})
	self := h.who(ctx, fileID, clientID)
	for _, o := range others {
		h.inbox(ctx, fileID, o.ClientID, map[string]any{"type": "rtc.peer-joined", "peer": self})
	}
	h.callRoster(ctx, fileID, "")
}

// callRoster tells the whole file channel who is in the call.
func (h *Hub) callRoster(ctx context.Context, fileID, except string) {
	h.publish(ctx, application.Message{Topic: topic.FileEventsTopic(fileID), QoS: 1, Expiry: eventExpiry,
		Payload: mustJSON(map[string]any{"type": "rtc.roster", "fileId": fileID, "peers": h.peers(ctx, fileID, except)})})
}

// leaveRTC takes a client out of the call (explicitly, or its connection
// ended).
func (h *Hub) leaveRTC(ctx context.Context, fileID, clientID string) {
	if !h.isMember(ctx, fileID, clientID) {
		return
	}
	_ = h.o.Slots.Release(ctx, fileID, clientID)
	for _, o := range h.peers(ctx, fileID, clientID) {
		h.inbox(ctx, fileID, o.ClientID, map[string]any{"type": "rtc.peer-left", "clientId": clientID})
	}
	h.callRoster(ctx, fileID, clientID)
}

func (h *Hub) signalRTC(ctx context.Context, fileID, from, to, kind string, data json.RawMessage) {
	if !slices.Contains(signalKinds, kind) || len(data) == 0 || len(data) > maxSignalBytes || data[0] != '{' || to == "" || to == from {
		return
	}
	members, _ := h.o.Slots.Members(ctx, fileID)
	if !slices.Contains(members, from) || !slices.Contains(members, to) {
		return
	}
	h.inbox(ctx, fileID, to, map[string]any{"type": "rtc.signal", "from": from, "kind": kind, "data": data})
}

func (h *Hub) stateRTC(ctx context.Context, fileID, from string, muted, speaking bool) {
	if !h.isMember(ctx, fileID, from) {
		return
	}
	for _, o := range h.peers(ctx, fileID, from) {
		h.inbox(ctx, fileID, o.ClientID, map[string]any{"type": "rtc.state", "from": from, "muted": muted, "speaking": speaking})
	}
}

// RTCConfig is the body of GET /rtc/config.
type RTCConfig struct {
	Enabled    bool `json:"enabled"`
	MaxPeers   int  `json:"maxPeers"`
	ICEServers any  `json:"iceServers"`
}

// RTCConfig is what browsers need to join an audio room.
func (h *Hub) RTCConfig() RTCConfig {
	ice := h.o.ICEServers
	if ice == nil {
		ice = []any{}
	}
	return RTCConfig{Enabled: true, MaxPeers: h.o.MaxPeers, ICEServers: ice}
}
