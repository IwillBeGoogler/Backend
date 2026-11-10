package application

// The webhook dispatcher when its repository or queue fails: a webhook
// problem never fails the change, and the worker retries what it must.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// hookRepo is a webhook repository with switchable failures.
type hookRepo struct {
	diagram.WebhookRepository
	hooks                                 []diagram.Webhook
	delivery                              diagram.Delivery
	activeErr, createErr, getErr, byIDErr error
	updateErr                             error
	hook                                  diagram.Webhook
	created, updated                      int
}

func (r *hookRepo) ActiveWebhooks(context.Context, string) ([]diagram.Webhook, error) {
	return r.hooks, r.activeErr
}
func (r *hookRepo) CreateDelivery(context.Context, diagram.Delivery) error {
	r.created++
	return r.createErr
}
func (r *hookRepo) Delivery(context.Context, string) (diagram.Delivery, error) {
	return r.delivery, r.getErr
}
func (r *hookRepo) WebhookByID(context.Context, string) (diagram.Webhook, error) {
	return r.hook, r.byIDErr
}
func (r *hookRepo) UpdateDelivery(_ context.Context, d diagram.Delivery) error {
	r.updated++
	r.delivery = d
	return r.updateErr
}

func TestWebhookDispatcherRepositoryFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repo := &hookRepo{hook: diagram.Webhook{ID: "w", URL: "https://x.test/", Active: true}}
	q := &memQueue{}
	send := &scriptedSender{status: 500}
	w := NewWebhookDispatcher(repo, q, send, nil)
	w.now = func() time.Time { return now }
	w.jitter = func() float64 { return 0 }
	hook := diagram.Webhook{ID: "w", ProjectID: "p", Active: true, Events: []string{diagram.WebhookFileUpdated, diagram.WebhookFileDeleted, diagram.WebhookLibraryUpdated, diagram.WebhookFileCreated}}
	repo.hooks = []diagram.Webhook{hook}

	// events that map to nothing, or lack a project/file, queue nothing
	w.Emit(dctx, DiagramEvent{Kind: "comment.added", ProjectID: "p"})
	w.Emit(dctx, DiagramEvent{Kind: EventFile})
	w.Emit(dctx, DiagramEvent{Kind: EventTree, Reason: "file.created", ProjectID: "p"})
	if repo.created != 0 {
		t.Fatalf("created %d deliveries for nothing", repo.created)
	}
	// each mapped kind queues one delivery
	for _, k := range []DiagramEvent{
		{Kind: EventFile, ProjectID: "p", FileID: "f", Version: 2},
		{Kind: EventRestored, ProjectID: "p", FileID: "f"},
		{Kind: EventFileDeleted, ProjectID: "p", FileID: "f"},
		{Kind: EventLibrary, ProjectID: "p", Rev: 3},
		{Kind: EventTree, Reason: "file.created", ProjectID: "p", FileID: "f"},
	} {
		w.Emit(dctx, k)
	}
	if repo.created != 5 || len(q.jobs) != 5 {
		t.Errorf("created %d, queued %d", repo.created, len(q.jobs))
	}

	// repository failures are swallowed
	repo.activeErr = errBoom
	w.Emit(dctx, DiagramEvent{Kind: EventFile, ProjectID: "p", FileID: "f"})
	repo.activeErr, repo.createErr = nil, errBoom
	before := len(q.jobs)
	w.Emit(dctx, DiagramEvent{Kind: EventFile, ProjectID: "p", FileID: "f"})
	if len(q.jobs) != before {
		t.Error("queued a delivery that was not recorded")
	}
	repo.createErr = nil
	repo.updateErr = errBoom // marking an unqueueable delivery failed also fails: still no panic
	q.err = errBoom
	w.Emit(dctx, DiagramEvent{Kind: EventFile, ProjectID: "p", FileID: "f"})
	q.err, repo.updateErr = nil, nil

	// Deliver
	repo.getErr = domain.ErrNotFound
	if err := w.Deliver(dctx, "d"); err != nil {
		t.Errorf("gone delivery: %v", err)
	}
	repo.getErr = errBoom
	if err := w.Deliver(dctx, "d"); !errors.Is(err, errBoom) {
		t.Errorf("repo error: %v", err)
	}
	repo.getErr = nil
	repo.delivery = diagram.Delivery{ID: "d", WebhookID: "w", Event: diagram.WebhookFileUpdated, Status: diagram.DeliveryOK}
	if err := w.Deliver(dctx, "d"); err != nil || len(send.sent) != 0 {
		t.Errorf("finished delivery re-sent: %v", err)
	}
	// retried before its backoff ends: waits, does not send
	later := now.Add(time.Hour)
	repo.delivery = diagram.Delivery{ID: "d", WebhookID: "w", Event: diagram.WebhookFileUpdated, Status: diagram.DeliveryPending, Attempt: 1, NextAttemptAt: &later, Payload: "{}"}
	n := len(q.jobs)
	if err := w.Deliver(dctx, "d"); err != nil || len(q.jobs) != n+1 || len(send.sent) != 0 {
		t.Errorf("early retry: %v %d", err, len(q.jobs))
	}
	// due: a 500 is retried and the follow-up queued
	repo.delivery.NextAttemptAt = nil
	if err := w.Deliver(dctx, "d"); err != nil || repo.delivery.Status != diagram.DeliveryPending || repo.delivery.NextAttemptAt == nil {
		t.Errorf("500 retry: %v %+v", err, repo.delivery)
	}
	// ... and when the follow-up cannot be queued the error comes back for the worker to retry
	repo.delivery.NextAttemptAt = nil
	q.err = errBoom
	if err := w.Deliver(dctx, "d"); !errors.Is(err, errBoom) {
		t.Errorf("follow-up queue down: %v", err)
	}
	q.err = nil
	// transport errors retry too; a timeout is named
	send.status, send.err = 0, context.DeadlineExceeded
	repo.delivery.NextAttemptAt, repo.delivery.Status = nil, diagram.DeliveryPending
	if err := w.Deliver(dctx, "d"); err != nil || repo.delivery.Error != "timeout" {
		t.Errorf("timeout: %v %q", err, repo.delivery.Error)
	}
	send.err = errors.New("connection refused")
	repo.delivery.NextAttemptAt, repo.delivery.Status = nil, diagram.DeliveryPending
	_ = w.Deliver(dctx, "d")
	// a URL the sender refuses is final
	send.err = wrapURL("private")
	repo.delivery.NextAttemptAt, repo.delivery.Status = nil, diagram.DeliveryPending
	if err := w.Deliver(dctx, "d"); err != nil || repo.delivery.Status != diagram.DeliveryFailed {
		t.Errorf("refused url: %v %+v", err, repo.delivery)
	}
	send.err = nil
	// 4xx is final, 2xx done
	send.status = 404
	repo.delivery.NextAttemptAt, repo.delivery.Status = nil, diagram.DeliveryPending
	_ = w.Deliver(dctx, "d")
	if repo.delivery.Status != diagram.DeliveryFailed {
		t.Errorf("404: %+v", repo.delivery)
	}
	send.status = 204
	repo.delivery.NextAttemptAt, repo.delivery.Status = nil, diagram.DeliveryPending
	_ = w.Deliver(dctx, "d")
	if repo.delivery.Status != diagram.DeliveryOK {
		t.Errorf("204: %+v", repo.delivery)
	}

	// attempt: webhook lookup fails / vanished / update fails
	repo.delivery.Status = diagram.DeliveryPending
	repo.byIDErr = errBoom
	if err := w.Deliver(dctx, "d"); !errors.Is(err, errBoom) {
		t.Errorf("webhook lookup: %v", err)
	}
	repo.byIDErr = domain.ErrNotFound
	if err := w.Deliver(dctx, "d"); err != nil || repo.delivery.Error != "webhook deleted" {
		t.Errorf("webhook deleted: %v %q", err, repo.delivery.Error)
	}
	repo.byIDErr, repo.delivery.Status = nil, diagram.DeliveryPending
	repo.updateErr = errBoom
	if err := w.Deliver(dctx, "d"); !errors.Is(err, errBoom) {
		t.Errorf("update: %v", err)
	}
	repo.updateErr, repo.delivery.Status = domain.ErrNotFound, diagram.DeliveryPending
	if err := w.Deliver(dctx, "d"); err != nil {
		t.Errorf("update of a removed row: %v", err)
	}
	repo.updateErr = nil

	// Ping: record fails, attempt fails, a retryable failure ends as failed
	repo.createErr = errBoom
	if _, err := w.Ping(dctx, hook); !errors.Is(err, errBoom) {
		t.Errorf("ping record: %v", err)
	}
	repo.createErr, repo.byIDErr = nil, errBoom
	if _, err := w.Ping(dctx, hook); !errors.Is(err, errBoom) {
		t.Errorf("ping attempt: %v", err)
	}
	repo.byIDErr = nil
	send.status = 503
	d, err := w.Ping(dctx, hook)
	if err != nil || d.Status != diagram.DeliveryFailed || d.NextAttemptAt != nil {
		t.Errorf("ping 503: %v %+v", err, d)
	}
	repo.updateErr = nil
}
