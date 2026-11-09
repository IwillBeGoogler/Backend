package diagramhub

import (
	"context"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/topic"
)

// Presence. A client that has a file open publishes a small non-empty message
// to dg/f/{file}/presence/{client} when it opens the file and again every
// 30 seconds, and sets a Last Will on the same topic with an empty payload.
// The server vouches for who it is: it republishes, retained and with a
// 90-second expiry, a roster entry for the file (dg/f/{file}/roster/{client})
// and for the project's tree (dg/p/{project}/roster/{file}/{client}), carrying
// the name, user id and role the account behind the client id has. An empty
// presence message (the Last Will, or a clean leave) clears both entries,
// ends the client's cursors and its place in the audio room. A client that
// vanishes without the broker noticing falls out when the expiry passes.

type rosterMark struct {
	at      time.Time
	project string
}

// rosterEvery is how often the same client's roster entry is republished; a
// client refreshes more often than it expires.
const rosterEvery = 45 * time.Second

type rosterEntry struct {
	ClientID  string  `json:"clientId"`
	UserID    *string `json:"userId"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	FileID    string  `json:"fileId"`
	ProjectID string  `json:"projectId"`
}

func (h *Hub) handlePresence(ctx context.Context, t topic.Topic, payload []byte) {
	if len(payload) == 0 {
		h.clientGone(ctx, t.FileID, t.ClientID)
		return
	}
	a, ok := h.access(ctx, t.ClientID, t.FileID)
	if !ok {
		return
	}
	key := t.FileID + "|" + t.ClientID
	now := time.Now()
	h.mu.Lock()
	mark, seen := h.roster[key]
	fresh := seen && now.Sub(mark.at) < rosterEvery
	if !fresh {
		h.roster[key] = rosterMark{at: now, project: a.ProjectID}
	}
	h.mu.Unlock()
	h.refreshSlot(ctx, t.FileID, t.ClientID)
	if fresh {
		return
	}
	b := mustJSON(rosterEntry{ClientID: t.ClientID, UserID: userRef(a.UserID), Name: a.Name, Role: string(a.Role), FileID: t.FileID, ProjectID: a.ProjectID})
	h.publish(ctx, application.Message{Topic: topic.FileRosterTopic(t.FileID, t.ClientID), Payload: b, Retain: true, Expiry: rosterExpiry})
	h.publish(ctx, application.Message{Topic: topic.ProjectRosterTopic(a.ProjectID, t.FileID, t.ClientID), Payload: b, Retain: true, Expiry: rosterExpiry})
}

// refreshSlot keeps the audio room place of a client that is in the room.
func (h *Hub) refreshSlot(ctx context.Context, fileID, clientID string) {
	members, err := h.o.Slots.Members(ctx, fileID)
	if err != nil {
		return
	}
	for _, m := range members {
		if m == clientID {
			_, _ = h.o.Slots.Acquire(ctx, fileID, clientID, h.o.MaxPeers)
			return
		}
	}
}

// clientGone ends everything a client had in a file.
func (h *Hub) clientGone(ctx context.Context, fileID, clientID string) {
	key := fileID + "|" + clientID
	h.mu.Lock()
	mark := h.roster[key]
	delete(h.roster, key)
	delete(h.seen, clientID)
	h.mu.Unlock()
	project := mark.project
	if project == "" { // another instance published it
		if a, ok := h.access(ctx, clientID, fileID); ok {
			project = a.ProjectID
		}
	}
	h.publish(ctx, application.Message{Topic: topic.FileRosterTopic(fileID, clientID), Retain: true})
	if project != "" {
		h.publish(ctx, application.Message{Topic: topic.ProjectRosterTopic(project, fileID, clientID), Retain: true})
	}
	h.leaveRTC(ctx, fileID, clientID)
	h.dropAwareness(ctx, fileID, clientID)
}
