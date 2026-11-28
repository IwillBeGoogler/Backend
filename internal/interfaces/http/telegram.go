package http

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	infra "github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/telegram"
)

const telegramDisabled = "telegram is not enabled"

// telegramStatus says whether the bot is available and the caller linked it.
//
// {"enabled", "linked", "linked_at"}; enabled false hides the feature.
// Auth: signed in.
//
// spector:tags telegram
func (h *Handler) telegramStatus(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if h.Telegram == nil {
		writeJSON(w, http.StatusOK, application.TelegramStatus{})
		return
	}
	s, err := h.Telegram.Status(r.Context(), actor.ID)
	respond(w, r, http.StatusOK, s, err, "")
}

// telegramLink returns a single-use bot link, valid for 10 minutes, that
// connects the Telegram chat opening it to the caller.
//
// Rate limit: 10 per 10 minutes. 404 when the bot is not enabled.
// Auth: signed in.
//
// spector:tags telegram
func (h *Handler) telegramLink(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "telegram-link", 10, 10*time.Minute) {
		return
	}
	if h.Telegram == nil {
		writeError(w, r, http.StatusNotFound, telegramDisabled)
		return
	}
	u, err := h.Telegram.LinkURL(r.Context(), actor.ID)
	respond(w, r, http.StatusOK, map[string]string{"url": u}, err, "")
}

// telegramUnlink disconnects the caller's Telegram chat.
//
// Auth: signed in.
//
// spector:tags telegram
func (h *Handler) telegramUnlink(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if h.Telegram == nil {
		writeError(w, r, http.StatusNotFound, telegramDisabled)
		return
	}
	noContent(w, r, h.Telegram.Unlink(r.Context(), actor.ID), "")
}

// telegramWebhook receives bot updates from Telegram (webhook mode only).
//
// The X-Telegram-Bot-Api-Secret-Token header must equal
// TELEGRAM_MAIN_WEBHOOK_SECRET. A request with the right secret is always
// answered 200, so Telegram does not redeliver it; failures are logged.
// Auth: webhook secret.
//
// spector:tags telegram
func (h *Handler) telegramWebhook(w http.ResponseWriter, r *http.Request) {
	t := h.Telegram
	if t == nil || t.Mode != application.TelegramWebhook || t.WebhookSecret == "" {
		writeError(w, r, http.StatusNotFound, telegramDisabled)
		return
	}
	got := sha256.Sum256([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")))
	want := sha256.Sum256([]byte(t.WebhookSecret))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	var u infra.Update
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&u); err != nil {
		zap.L().Warn("telegram: bad update", zap.Error(err))
	} else {
		// a link token is single use: finish even if Telegram hangs up
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
		defer cancel()
		if err := t.HandleUpdate(ctx, u.Domain()); err != nil {
			zap.L().Warn("telegram: update", zap.Any("ctx", ctx), zap.Error(err))
		}
	}
	w.WriteHeader(http.StatusOK)
}
