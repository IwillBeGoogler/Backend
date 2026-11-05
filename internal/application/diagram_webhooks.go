package application

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// Outgoing webhooks of a diagram project. The project's owners and editors
// manage endpoints; a change in the project queues one signed delivery per
// subscribed endpoint, which the background worker sends with retries.

// WebhookQueue schedules the delivery of a queued event.
type WebhookQueue interface {
	DeliverWebhookAt(ctx context.Context, deliveryID string, attempt int, at time.Time) error
}

// WebhookRequest is one POST to an endpoint.
type WebhookRequest struct {
	URL        string
	Event      string
	DeliveryID string
	Timestamp  int64
	Signature  string
	Body       []byte
}

// WebhookSender makes the request and returns the response status. It must
// refuse targets the server must not reach (ErrWebhookURL), time out, and
// read only a bounded part of the answer.
type WebhookSender interface {
	Send(ctx context.Context, r WebhookRequest) (status int, err error)
}

// ErrWebhookURL is an endpoint URL the server refuses to call.
var ErrWebhookURL = errors.New("webhook url rejected")

// WebhookDispatcher queues and sends deliveries. It is also the realtime
// DiagramEvents sink that turns project changes into webhook events.
type WebhookDispatcher struct {
	Repo   diagram.WebhookRepository
	Queue  WebhookQueue
	Sender WebhookSender
	// Check validates an endpoint URL when it is saved and before each
	// attempt (nil: any http(s) URL).
	Check func(ctx context.Context, url string) error
	// OnRejected is told when an attempt is refused by Check (nil: nobody).
	OnRejected func()

	now    func() time.Time
	jitter func() float64
}

func NewWebhookDispatcher(repo diagram.WebhookRepository, q WebhookQueue, s WebhookSender, check func(context.Context, string) error) *WebhookDispatcher {
	return &WebhookDispatcher{Repo: repo, Queue: q, Sender: s, Check: check, now: time.Now,
		jitter: func() float64 { return rand.Float64()*2 - 1 }}
}

func (w *WebhookDispatcher) checkURL(ctx context.Context, url string) error {
	if w.Check == nil {
		return nil
	}
	return w.Check(ctx, url)
}

type webhookPayload struct {
	ID        string         `json:"id"`
	Event     string         `json:"event"`
	ProjectID string         `json:"projectId"`
	FileID    string         `json:"fileId,omitempty"`
	Version   *int           `json:"version,omitempty"`
	At        string         `json:"at"`
	Data      map[string]any `json:"data"`
}

// Emit implements DiagramEvents: it maps the change to a webhook event and
// queues a delivery for every subscribed endpoint. It never fails the
// change that caused it.
func (w *WebhookDispatcher) Emit(ctx context.Context, e DiagramEvent) {
	var event string
	data := map[string]any{}
	var version *int
	switch {
	case e.Kind == EventTree && e.Reason == "file.created":
		event = diagram.WebhookFileCreated
	case e.Kind == EventFile:
		event, version = diagram.WebhookFileUpdated, &e.Version
	case e.Kind == EventRestored:
		event = diagram.WebhookFileUpdated
		data["restored"] = true
	case e.Kind == EventFileDeleted:
		event = diagram.WebhookFileDeleted
	case e.Kind == EventLibrary:
		event = diagram.WebhookLibraryUpdated
		data["rev"] = e.Rev
	default:
		return
	}
	if e.ProjectID == "" {
		return
	}
	fileID := e.FileID
	if e.Kind == EventTree && fileID == "" {
		return
	}
	w.emit(ctx, e.ProjectID, event, fileID, version, data)
}

func (w *WebhookDispatcher) emit(ctx context.Context, projectID, event, fileID string, version *int, data map[string]any) {
	ctx = context.WithoutCancel(ctx)
	hooks, err := w.Repo.ActiveWebhooks(ctx, projectID)
	if err != nil {
		zap.L().Warn("webhooks: list", zap.String("project", projectID), zap.Error(err))
		return
	}
	now := w.now().Truncate(time.Millisecond)
	for _, h := range hooks {
		if !h.Subscribed(event) {
			continue
		}
		payload, err := json.Marshal(webhookPayload{ID: diagram.NewID(diagram.PrefixEvent), Event: event, ProjectID: projectID,
			FileID: fileID, Version: version, At: now.UTC().Format(time.RFC3339Nano), Data: data})
		if err != nil {
			continue
		}
		d := diagram.Delivery{ID: diagram.NewID(diagram.PrefixDelivery), WebhookID: h.ID, Event: event, Payload: string(payload),
			Status: diagram.DeliveryPending, NextAttemptAt: &now, CreatedAt: now}
		if err := w.Repo.CreateDelivery(ctx, d); err != nil {
			zap.L().Warn("webhooks: record delivery", zap.String("webhook", h.ID), zap.Error(err))
			continue
		}
		if err := w.Queue.DeliverWebhookAt(ctx, d.ID, 1, now); err != nil {
			// the row stays pending; nothing will pick it up, so say so
			d.Status, d.Error, d.NextAttemptAt = diagram.DeliveryFailed, "could not be queued", nil
			_ = w.Repo.UpdateDelivery(ctx, d)
			zap.L().Warn("webhooks: enqueue", zap.String("delivery", d.ID), zap.Error(err))
		}
	}
}

// Deliver makes the next attempt of a queued delivery (the worker's job).
// A retryable failure schedules the following attempt itself, so the
// backoff is the one the API reports as nextAttemptAt.
func (w *WebhookDispatcher) Deliver(ctx context.Context, deliveryID string) error {
	d, err := w.Repo.Delivery(ctx, deliveryID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil // its webhook was deleted
	}
	if err != nil {
		return err
	}
	if d.Status != diagram.DeliveryPending {
		return nil // already done (a duplicate task)
	}
	if d.NextAttemptAt != nil && d.NextAttemptAt.After(w.now()) {
		// retried early (the follow-up could not be queued): wait for the backoff
		return w.Queue.DeliverWebhookAt(ctx, d.ID, d.Attempt+1, *d.NextAttemptAt)
	}
	d, err = w.attempt(ctx, d)
	if err != nil {
		return err
	}
	if d.Status == diagram.DeliveryPending && d.NextAttemptAt != nil {
		return w.Queue.DeliverWebhookAt(ctx, d.ID, d.Attempt+1, *d.NextAttemptAt)
	}
	return nil
}

// attempt tries a delivery once and stores the outcome.
func (w *WebhookDispatcher) attempt(ctx context.Context, d diagram.Delivery) (diagram.Delivery, error) {
	hook, err := w.Repo.WebhookByID(ctx, d.WebhookID)
	missing := errors.Is(err, domain.ErrNotFound)
	if err != nil && !missing {
		return d, err
	}
	d.Attempt++
	d.Status, d.ResponseCode, d.NextAttemptAt, d.Error = diagram.DeliveryFailed, nil, nil, ""
	switch {
	case missing:
		d.Error = "webhook deleted"
	case !hook.Active && d.Event != diagram.WebhookPing:
		d.Error = "webhook disabled"
	default:
		w.send(ctx, &d, hook)
	}
	if err := w.Repo.UpdateDelivery(ctx, d); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return d, err
	}
	return d, nil
}

func (w *WebhookDispatcher) send(ctx context.Context, d *diagram.Delivery, hook diagram.Webhook) {
	if err := w.checkURL(ctx, hook.URL); err != nil {
		d.Error = "URL rejected: " + err.Error()
		if w.OnRejected != nil {
			w.OnRejected()
		}
		return
	}
	ts := w.now().Unix()
	body := []byte(d.Payload)
	status, err := w.Sender.Send(ctx, WebhookRequest{URL: hook.URL, Event: d.Event, DeliveryID: d.ID, Timestamp: ts,
		Signature: diagram.Sign(hook.Secret, ts, body), Body: body})
	retry := false
	switch {
	case errors.Is(err, ErrWebhookURL):
		d.Error = "URL rejected: " + strings.TrimPrefix(err.Error(), ErrWebhookURL.Error()+": ")
		return
	case err != nil:
		d.Error, retry = failureText(err), true
	default:
		d.ResponseCode = &status
		switch diagram.ClassifyStatus(status) {
		case diagram.StatusOK:
			d.Status = diagram.DeliveryOK
			return
		case diagram.StatusRetry:
			retry = true
		}
		d.Error = "HTTP " + strconv.Itoa(status)
	}
	if retry {
		if delay, ok := diagram.RetryDelay(d.Attempt, w.jitter()); ok {
			at := w.now().Add(delay).Truncate(time.Millisecond)
			d.Status, d.NextAttemptAt = diagram.DeliveryPending, &at
		}
	}
}

func failureText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	msg := err.Error()
	return truncate(msg, 200) // on a rune boundary: Postgres rejects invalid UTF-8
}

// Ping sends a signed "ping" to one endpoint now: one attempt, no retries.
func (w *WebhookDispatcher) Ping(ctx context.Context, hook diagram.Webhook) (diagram.Delivery, error) {
	now := w.now().Truncate(time.Millisecond)
	payload, _ := json.Marshal(webhookPayload{ID: diagram.NewID(diagram.PrefixEvent), Event: diagram.WebhookPing, ProjectID: hook.ProjectID,
		At: now.UTC().Format(time.RFC3339Nano), Data: map[string]any{"message": "Webhook test"}})
	d := diagram.Delivery{ID: diagram.NewID(diagram.PrefixDelivery), WebhookID: hook.ID, Event: diagram.WebhookPing,
		Payload: string(payload), Status: diagram.DeliveryPending, CreatedAt: now}
	if err := w.Repo.CreateDelivery(ctx, d); err != nil {
		return d, err
	}
	d, err := w.attempt(ctx, d)
	if err != nil {
		return d, err
	}
	if d.Status == diagram.DeliveryPending { // a test is not retried in the background
		d.Status, d.NextAttemptAt = diagram.DeliveryFailed, nil
		err = w.Repo.UpdateDelivery(ctx, d)
	}
	return d, err
}

// EventsFanout sends each event to every sink, in order.
type EventsFanout []DiagramEvents

func (f EventsFanout) Emit(ctx context.Context, e DiagramEvent) {
	for _, s := range f {
		if s != nil {
			s.Emit(ctx, e)
		}
	}
}

// --- use cases ---

func (d *Diagrams) needWebhooks() error {
	if d.Webhooks == nil || d.Hooks == nil {
		return errors.New("diagram webhooks not configured")
	}
	return nil
}

func (d *Diagrams) webhookAccess(ctx context.Context, actor int, projectID string) error {
	if err := d.needWebhooks(); err != nil {
		return err
	}
	_, err := d.projectAccess(ctx, actor, projectID, diagram.RoleEditor)
	return err
}

func (d *Diagrams) loadWebhook(ctx context.Context, projectID, id string) (diagram.Webhook, error) {
	w, err := d.Webhooks.Webhook(ctx, projectID, id)
	if errors.Is(err, domain.ErrNotFound) {
		return w, diagram.NotFound("Webhook")
	}
	return w, err
}

// WebhookURLRejected is a 400 for an endpoint the server will not call.
func webhookURLRejected(err error) error {
	return &diagram.Error{Kind: domain.Invalid("webhook url"), Code: "WEBHOOK_URL_REJECTED", Msg: "Webhook URL rejected: " + err.Error()}
}

func cleanEvents(events []string) ([]string, error) {
	if len(events) == 0 || len(events) > len(diagram.WebhookEvents) {
		return nil, diagram.Validation("events: choose at least one event")
	}
	var out []string
	for _, e := range events {
		if !slices.Contains(diagram.WebhookEvents, e) {
			return nil, diagram.Validation("events: unknown event " + e)
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (d *Diagrams) checkWebhookURL(ctx context.Context, url string) error {
	url = strings.TrimSpace(url)
	if url == "" || len(url) > diagram.MaxWebhookURL {
		return diagram.Validation("url: 1-2000 characters")
	}
	if err := d.Hooks.checkURL(ctx, url); err != nil {
		return webhookURLRejected(err)
	}
	return nil
}

// Webhooks lists a project's webhooks (editors and owners).
func (d *Diagrams) ListWebhooks(ctx context.Context, actor int, projectID string) ([]diagram.Webhook, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ListWebhooks")
	defer span.End()
	if err := d.webhookAccess(ctx, actor, projectID); err != nil {
		return nil, err
	}
	return d.Webhooks.Webhooks(ctx, projectID)
}

// CreateWebhook adds an endpoint. Its signing secret is on the returned
// webhook; the API shows it this once.
func (d *Diagrams) CreateWebhook(ctx context.Context, actor int, projectID, url string, events []string) (diagram.Webhook, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.CreateWebhook")
	defer span.End()
	if err := d.webhookAccess(ctx, actor, projectID); err != nil {
		return diagram.Webhook{}, err
	}
	evs, err := cleanEvents(events)
	if err != nil {
		return diagram.Webhook{}, err
	}
	if err := d.checkWebhookURL(ctx, url); err != nil {
		return diagram.Webhook{}, err
	}
	w := diagram.Webhook{ID: diagram.NewID(diagram.PrefixWebhook), ProjectID: projectID, URL: strings.TrimSpace(url),
		Secret: diagram.NewWebhookSecret(), Events: evs, Active: true, CreatedBy: actor, CreatedAt: d.clock()}
	return w, d.Webhooks.CreateWebhook(ctx, w)
}

// WebhookPatch changes an endpoint; nil fields stay as they are.
type WebhookPatch struct {
	URL    *string
	Events []string // nil: unchanged
	Active *bool
}

func (d *Diagrams) UpdateWebhook(ctx context.Context, actor int, projectID, id string, in WebhookPatch) (diagram.Webhook, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.UpdateWebhook")
	defer span.End()
	if err := d.webhookAccess(ctx, actor, projectID); err != nil {
		return diagram.Webhook{}, err
	}
	w, err := d.loadWebhook(ctx, projectID, id)
	if err != nil {
		return w, err
	}
	if in.URL != nil {
		if err := d.checkWebhookURL(ctx, *in.URL); err != nil {
			return w, err
		}
		w.URL = strings.TrimSpace(*in.URL)
	}
	if in.Events != nil {
		if w.Events, err = cleanEvents(in.Events); err != nil {
			return w, err
		}
	}
	if in.Active != nil {
		w.Active = *in.Active
	}
	return w, d.Webhooks.UpdateWebhook(ctx, w)
}

// DeleteWebhook removes an endpoint and its delivery log.
func (d *Diagrams) DeleteWebhook(ctx context.Context, actor int, projectID, id string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.DeleteWebhook")
	defer span.End()
	if err := d.webhookAccess(ctx, actor, projectID); err != nil {
		return err
	}
	w, err := d.loadWebhook(ctx, projectID, id)
	if err != nil {
		return err
	}
	return d.Webhooks.DeleteWebhook(ctx, w.ID)
}

// WebhookDeliveries lists an endpoint's last 50 deliveries.
func (d *Diagrams) WebhookDeliveries(ctx context.Context, actor int, projectID, id string) ([]diagram.Delivery, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.WebhookDeliveries")
	defer span.End()
	if err := d.webhookAccess(ctx, actor, projectID); err != nil {
		return nil, err
	}
	w, err := d.loadWebhook(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	return d.Webhooks.Deliveries(ctx, w.ID, 50)
}

// TestWebhook sends a signed ping now and returns the delivery.
func (d *Diagrams) TestWebhook(ctx context.Context, actor int, projectID, id string) (diagram.Delivery, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.TestWebhook")
	defer span.End()
	if err := d.webhookAccess(ctx, actor, projectID); err != nil {
		return diagram.Delivery{}, err
	}
	w, err := d.loadWebhook(ctx, projectID, id)
	if err != nil {
		return diagram.Delivery{}, err
	}
	return d.Hooks.Ping(ctx, w)
}
