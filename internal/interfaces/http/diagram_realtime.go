package http

// Realtime for the diagram workspace runs over MQTT (see internal/interfaces/diagramhub and
// docs/MQTT.md); the HTTP side is what stays request / response, under /api/diagrams:
//
//	PUT /files/{id}/ydoc       a client's compacted Yjs snapshot (the same as the MQTT yjs/up message 3)
//	GET /rtc/config            ICE servers and the audio room size
//	GET /realtime/config       where and how to reach the MQTT broker
//
// and the broker's two questions, under /api/internal/mqtt, which only the broker may ask (see mqtt_internal.go).

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/yjs"
	"github.com/iBlog/iblog-monolith-go/internal/interfaces/diagramhub"
)

func (h *Handler) realtimeRoutes(mux dgRouter) {
	if h.Realtime == nil {
		return
	}
	p := DiagramPrefix
	mux.HandleFunc("PUT "+p+"/files/{id}/ydoc", h.dgPutYDoc)
	mux.HandleFunc("GET "+p+"/rtc/config", h.dgRTCConfig)
	mux.HandleFunc("GET "+p+"/realtime/config", h.dgRealtimeConfig)
}

// dgPutYDoc stores a compacted Yjs snapshot: the body is the whole document
// as one Yjs update (application/octet-stream), ?seq= the sequence number it
// is complete up to (from the server's compaction hint). The server keeps it
// as the new snapshot and drops the stored updates up to seq. 409
// SNAPSHOT_INCOMPLETE when it lacks an update the server holds, 400 for a body
// that is no Yjs update. Response {"compacted": bool}. Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgPutYDoc(w http.ResponseWriter, r *http.Request) {
	if !h.Realtime.CollabEnabled() {
		dgFail(w, r, http.StatusNotFound, "NOT_FOUND", "Route not found", nil)
		return
	}
	id, ok := dgID(w, r, "id")
	if !ok {
		return
	}
	uid, token, ok := h.dgActor(w, r)
	if !ok {
		return
	}
	seq, err := strconv.ParseInt(r.URL.Query().Get("seq"), 10, 64)
	if err != nil || seq < 1 {
		dgValidation(w, r, "query", "seq: must be a positive integer")
		return
	}
	a, err := h.Diagrams.OpenChannel(r.Context(), uid, token, "file", id)
	if err == nil && !a.Role.Allows(diagram.RoleEditor) {
		err = diagram.NeedRole(diagram.RoleEditor)
	}
	if err != nil {
		dgError(w, r, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(max(h.Diagrams.MaxDataBytes, 1<<20))))
	if err != nil {
		dgFail(w, r, http.StatusRequestEntityTooLarge, "TOO_LARGE", "The snapshot is too large", nil)
		return
	}
	done, err := h.Realtime.Compact(context.WithoutCancel(r.Context()), a.FileID, seq, body)
	switch {
	case errors.Is(err, yjs.ErrMalformed):
		dgFail(w, r, http.StatusBadRequest, "BAD_SNAPSHOT", "The body is not a Yjs update", nil)
	case errors.Is(err, diagramhub.ErrIncomplete):
		dgFail(w, r, http.StatusConflict, "SNAPSHOT_INCOMPLETE", "The snapshot lacks updates the server holds", nil)
	case err != nil:
		dgError(w, r, err)
	default:
		dgWrite(w, http.StatusOK, map[string]bool{"compacted": done})
	}
}

// dgRTCConfig hands out the ICE (STUN and TURN) servers and the size of an
// audio room. Signed-in users and holders of a live share link only: it can
// carry TURN credentials.
//
// Auth: user or X-Share-Token.
//
// spector:tags diagrams
func (h *Handler) dgRTCConfig(w http.ResponseWriter, r *http.Request) {
	if h.realtimeCaller(w, r) {
		dgWrite(w, http.StatusOK, h.Realtime.RTCConfig())
	}
}

// realtimeCaller lets signed-in users and holders of a live share link
// through; it answers the rest.
func (h *Handler) realtimeCaller(w http.ResponseWriter, r *http.Request) bool {
	if h.viewerID(r) == 0 {
		token := r.Header.Get("X-Share-Token")
		if token == "" {
			dgFail(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required", nil)
			return false
		}
		if err := h.Diagrams.CheckShare(r.Context(), token); err != nil {
			dgError(w, r, err)
			return false
		}
	}
	return true
}

// RealtimeConfig is the body of GET /realtime/config.
type RealtimeConfig struct {
	// WSPath is the same-origin path of the broker's MQTT-over-WebSocket
	// listener (MQTT_PUBLIC_WS_PATH).
	WSPath string `json:"wsPath"`
	// Collab says whether the Yjs topics are served (COLLAB_ENABLED).
	Collab bool `json:"collab"`
	// PresenceEveryMs is how often a client republishes its presence.
	PresenceEveryMs int `json:"presenceEveryMs"`
	// AwarenessMaxPerSec is the most cursor updates a client should send.
	AwarenessMaxPerSec int `json:"awarenessMaxPerSec"`
}

// dgRealtimeConfig tells a client how to reach the broker. Connect with
// MQTT 5, client id "u<userId>.<nonce>" and username "ticket" with a ticket
// from POST /api/me/stream-ticket as the password, or client id "g.<nonce>"
// and username "share" with the share token as the password (docs/MQTT.md).
//
// Auth: user or X-Share-Token.
//
// spector:tags diagrams
func (h *Handler) dgRealtimeConfig(w http.ResponseWriter, r *http.Request) {
	if !h.realtimeCaller(w, r) {
		return
	}
	path := h.MQTTPublicWSPath
	if path == "" {
		path = "/mqtt"
	}
	dgWrite(w, http.StatusOK, RealtimeConfig{WSPath: path, Collab: h.Realtime.CollabEnabled(), PresenceEveryMs: 30_000, AwarenessMaxPerSec: 15})
}

// Events

// NewDiagramEvents turns the use cases' events into the messages the realtime
// channels carry (the Node server's wire shapes).
func NewDiagramEvents(hub *diagramhub.Hub) application.DiagramEvents { return dgEvents{hub} }

type dgEvents struct{ hub *diagramhub.Hub }

// by is who made a change as clients show it.
func by(a application.DiagramActor) map[string]any {
	if a.Share {
		return map[string]any{"share": true}
	}
	return map[string]any{"userId": strconv.Itoa(a.UserID), "name": a.Name}
}

func (e dgEvents) Emit(_ context.Context, ev application.DiagramEvent) {
	switch ev.Kind {
	case application.EventTree:
		m := map[string]any{"type": "tree.updated", "projectId": ev.ProjectID, "rev": ev.Rev, "by": by(ev.By)}
		if ev.Reason != "" {
			m["reason"] = ev.Reason
		}
		if ev.FolderID != "" {
			m["folderId"] = ev.FolderID
		}
		if ev.FileID != "" {
			m["fileId"] = ev.FileID
		}
		e.hub.BroadcastTree(ev.ProjectID, m, ev.ClientID)
	case application.EventFile:
		m := map[string]any{"type": "file.updated", "projectId": ev.ProjectID, "fileId": ev.FileID, "version": ev.Version,
			"name": ev.Name, "kind": ev.FileKind, "updatedAt": ev.UpdatedAt, "by": by(ev.By)}
		if ev.Restored {
			m["restored"] = true
		}
		e.hub.BroadcastFile(ev.ProjectID, ev.FileID, m, ev.ClientID)
	case application.EventLibrary:
		e.hub.BroadcastTree(ev.ProjectID, map[string]any{"type": "library.updated", "projectId": ev.ProjectID, "rev": ev.Rev, "by": by(ev.By)}, ev.ClientID)
	case application.EventFileDeleted:
		e.hub.CloseFile(ev.ProjectID, ev.FileID)
	case application.EventProjectDeleted:
		e.hub.CloseProject(ev.ProjectID, ev.FileIDs)
	case application.EventRestored:
		e.hub.ResetFile(ev.ProjectID, ev.FileID)
	case application.EventCommentCreated, application.EventCommentUpdated:
		e.hub.BroadcastFile(ev.ProjectID, ev.FileID, map[string]any{"type": ev.Kind, "fileId": ev.FileID, "comment": commentJSON(*ev.Comment)}, "")
	case application.EventCommentDeleted:
		e.hub.BroadcastFile(ev.ProjectID, ev.FileID, map[string]any{"type": ev.Kind, "fileId": ev.FileID, "commentId": ev.CommentID}, "")
	}
}
