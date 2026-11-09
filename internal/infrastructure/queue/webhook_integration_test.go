package queue_test

// A webhook delivery through a real asynq queue on Redis: queued by the
// event, picked up by the worker, retried by scheduling the next attempt.
// Skipped with -short or without Docker; set TEST_REDIS_URL to use a Redis
// you already run.

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/queue"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/redis"
)

type sender struct {
	mu     sync.Mutex
	status int
	got    []application.WebhookRequest
}

func (s *sender) Send(_ context.Context, r application.WebhookRequest) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, r)
	return s.status, nil
}

func (s *sender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func TestWebhookDeliveryThroughAsynq(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		rd, err := tcredis.Run(ctx, "redis:8-alpine")
		if err != nil {
			t.Skipf("docker unavailable: %v", err)
		}
		t.Cleanup(func() { testcontainers.TerminateContainer(rd) })
		if url, err = rd.ConnectionString(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rdb, err := redis.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rdb.Close() })

	mem := diagramtest.NewMemory()
	d := application.NewDiagrams(mem, mem)
	jobs := queue.NewClient(rdb)
	t.Cleanup(func() { jobs.Close() })
	snd := &sender{status: 503}
	d.Hooks = application.NewWebhookDispatcher(mem, jobs, snd, nil)
	d.Events = d.Hooks
	made, err := d.CreateProject(ctx, 1, application.NewDiagramProject{Name: "Queue"})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := d.CreateWebhook(ctx, 1, made.Project.ID, "https://hooks.example.com/in", []string{diagram.WebhookFileUpdated})
	if err != nil {
		t.Fatal(err)
	}

	w := queue.NewWorker(rdb, queue.Handlers{Hooks: d.Hooks})
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Stop)

	// a save queues a delivery; the worker sends it; a 503 schedules the retry
	if _, err := d.SaveFile(ctx, 1, made.File.ID, nil, []byte(`{"version":2,"activePageId":"pg","pages":[{"id":"pg","name":"P","nodes":[],"edges":[]}]}`), `"1"`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for snd.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if snd.count() != 1 {
		t.Fatalf("the worker sent %d requests", snd.count())
	}
	req := snd.got[0]
	if req.Event != "file.updated" || !diagram.VerifySignature(hook.Secret, req.Timestamp, req.Body, req.Signature, time.Now(), 0) {
		t.Errorf("request = %+v", req)
	}
	var dl diagram.Delivery
	for time.Now().Before(deadline) { // the worker stores the outcome right after sending
		if ds, _ := mem.Deliveries(ctx, hook.ID, 10); len(ds) == 1 && ds[0].Attempt == 1 {
			dl = ds[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if dl.Status != diagram.DeliveryPending || dl.NextAttemptAt == nil || dl.Error != "HTTP 503" {
		t.Fatalf("delivery = %+v", dl)
	}
	if wait := time.Until(*dl.NextAttemptAt); wait < 6*time.Second || wait > 14*time.Second {
		t.Errorf("next attempt in %v, want about 10s (+-20%%)", wait)
	}
	insp := asynq.NewInspectorFromRedisClient(rdb)
	t.Cleanup(func() { insp.Close() })
	var scheduled []*asynq.TaskInfo
	for time.Now().Before(deadline) {
		scheduled, _ = insp.ListScheduledTasks("default")
		if len(scheduled) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	found := false
	for _, ti := range scheduled {
		if ti.Type == queue.TypeWebhook && ti.ID == "webhook:"+dl.ID+":2" {
			found = true
			if ti.NextProcessAt.Sub(*dl.NextAttemptAt).Abs() > 2*time.Second {
				t.Errorf("task due %v, delivery says %v", ti.NextProcessAt, dl.NextAttemptAt)
			}
			_ = insp.DeleteTask("default", ti.ID) // do not leave the retry behind
		}
	}
	if !found {
		t.Errorf("no scheduled retry among %d tasks", len(scheduled))
	}
}
