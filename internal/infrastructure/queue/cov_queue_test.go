package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/clienterror"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// covDeadRedis never connects: enough to build a worker and run handlers.
func covDeadRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { rdb.Close() })
	return rdb
}

type covClientErrors struct{ clienterror.Repository }

func (covClientErrors) PurgeBefore(context.Context, time.Time) (int64, error) { return 0, nil }

type covMailer struct {
	mu   sync.Mutex
	n    int
	err  error
	done chan struct{}
}

func (m *covMailer) Send(context.Context, application.Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	err := m.err
	if m.done != nil {
		select {
		case m.done <- struct{}{}:
		default:
		}
	}
	return err
}

func (m *covMailer) count() int { m.mu.Lock(); defer m.mu.Unlock(); return m.n }

func covHandlers(posts duePosts, users dueUsers, mailer application.Mailer, st application.Storage) Handlers {
	var cutoff time.Time
	users.cutoff = &cutoff
	return Handlers{
		Blog:     application.NewBlog(application.Repos{Posts: posts, Users: users, ClientErrors: covClientErrors{}}, application.Ports{}),
		Accounts: application.NewAccounts(users, nil, nil, nil, nil, ""),
		Mailer:   mailer,
		Storage:  st,
	}
}

func TestCovMuxEmailAndThumbnail(t *testing.T) {
	ctx := context.Background()
	m := &covMailer{}
	st := &memStorage{objs: map[string][]byte{}}
	w := NewWorker(covDeadRedis(t), covHandlers(duePosts{}, dueUsers{}, m, st))

	for _, typ := range []string{TypeSendEmail, TypeThumbnail, TypePublishPost, TypeFanoutPost, TypePurgeAccount} {
		if err := w.mux.ProcessTask(ctx, asynq.NewTask(typ, []byte("not json"))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: err = %v, want SkipRetry", typ, err)
		}
	}
	body, _ := json.Marshal(application.Email{To: "a@example.com"})
	if err := w.mux.ProcessTask(ctx, asynq.NewTask(TypeSendEmail, body)); err != nil || m.count() != 1 {
		t.Fatalf("email: %v, sent %d", err, m.count())
	}
	// payload wrapped in the trace envelope, as the client sends it
	wrapped, _ := wrap(ctx, body)
	if err := w.mux.ProcessTask(ctx, asynq.NewTask(TypeSendEmail, wrapped)); err != nil || m.count() != 2 {
		t.Fatalf("wrapped email: %v, sent %d", err, m.count())
	}
	key, _ := json.Marshal(keyPayload{"missing"})
	if err := w.mux.ProcessTask(ctx, asynq.NewTask(TypeThumbnail, key)); !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("thumbnail: %v", err)
	}
	// no hooks configured: the webhook task has no handler
	if err := w.mux.ProcessTask(ctx, asynq.NewTask(TypeWebhook, nil)); err == nil {
		t.Error("webhook task handled without Hooks")
	}
}

func TestCovMuxSweep(t *testing.T) {
	ctx := context.Background()
	// idle: nothing to do, nothing invalidated
	inv := 0
	h := covHandlers(duePosts{}, dueUsers{}, &covMailer{}, nil)
	h.InvalidateCache = func(context.Context) error { inv++; return nil }
	w := NewWorker(covDeadRedis(t), h)
	if err := w.mux.ProcessTask(ctx, asynq.NewTask(TypeSweep, nil)); err != nil || inv != 0 {
		t.Fatalf("idle: %v inv=%d", err, inv)
	}
	// work (a failing lookup counts as work): the error surfaces and the
	// cache is invalidated anyway
	h = covHandlers(duePosts{err: errors.New("db down")}, dueUsers{}, &covMailer{}, nil)
	h.InvalidateCache = func(context.Context) error { inv++; return nil }
	w = NewWorker(covDeadRedis(t), h)
	if err := w.mux.ProcessTask(ctx, asynq.NewTask(TypeSweep, nil)); err == nil || inv != 1 {
		t.Fatalf("work: %v inv=%d", err, inv)
	}
	// default invalidation hits the (unreachable) Redis and only logs
	h = covHandlers(duePosts{err: errors.New("db down")}, dueUsers{}, &covMailer{}, nil)
	w = NewWorker(covDeadRedis(t), h)
	_ = w.mux.ProcessTask(ctx, asynq.NewTask(TypeSweep, nil))
	_ = post.StatusDraft
	_ = user.DeletionGrace
}

type covFailLog struct{ sentErr, markErr error }

func (l covFailLog) Sent(context.Context, string) (bool, error) { return false, l.sentErr }
func (l covFailLog) MarkSent(context.Context, string) error     { return l.markErr }

func TestCovSendEmailPaths(t *testing.T) {
	ctx := context.Background()
	m := &covMailer{}
	// no id: sent directly
	if err := sendEmail(ctx, covFailLog{}, m, "", application.Email{}); err != nil || m.count() != 1 {
		t.Fatalf("no id: %v", err)
	}
	// sent-check fails: retry later, nothing sent
	if err := sendEmail(ctx, covFailLog{sentErr: errors.New("redis down")}, m, "x", application.Email{}); err == nil || m.count() != 1 {
		t.Fatalf("check err: %v", err)
	}
	// mark fails after the send: still success
	if err := sendEmail(ctx, covFailLog{markErr: errors.New("redis down")}, m, "x", application.Email{}); err != nil || m.count() != 2 {
		t.Fatalf("mark err: %v", err)
	}
}

func TestCovZapDebug(t *testing.T) { zapLogger{}.Debug("x") }

func TestCovEnqueueMarshalError(t *testing.T) {
	c := NewClient(covDeadRedis(t))
	if err := c.enqueue(context.Background(), "x", make(chan int)); err == nil {
		t.Fatal("want marshal error")
	}
	if err := c.Thumbnail(context.Background(), "k"); err == nil {
		t.Fatal("want redis error")
	}
	_ = c.Close
}

var (
	covRedisOnce sync.Once
	covRedisURL  string
	covRedisErr  error
)

func covRedis(t *testing.T) *redis.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	covRedisOnce.Do(func() {
		rd, err := tcredis.Run(context.Background(), "redis:8-alpine")
		if err != nil {
			covRedisErr = err
			return
		}
		_ = testcontainers.TerminateContainer // reaped by ryuk at exit
		covRedisURL, covRedisErr = rd.ConnectionString(context.Background())
	})
	if covRedisErr != nil {
		t.Skipf("docker unavailable: %v", covRedisErr)
	}
	opt, err := redis.ParseURL(covRedisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	if err := rdb.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rdb.Close() })
	return rdb
}

func TestCovClientEnqueues(t *testing.T) {
	ctx := context.Background()
	rdb := covRedis(t)
	c := NewClient(rdb)
	defer c.Close()
	at := time.Now().Add(time.Hour)
	steps := []struct {
		name string
		fn   func() error
	}{
		{"publish", func() error { return c.PublishPostAt(ctx, 1, at) }},
		{"fanout", func() error { return c.FanoutNewPost(ctx, 1) }},
		{"purge", func() error { return c.PurgeAccountAt(ctx, 2, at) }},
		{"email", func() error { return c.SendEmail(ctx, application.Email{To: "a@b.c"}) }},
		{"emailOnce", func() error { return c.SendEmailOnce(ctx, "id1", application.Email{To: "a@b.c"}) }},
		{"thumb", func() error { return c.Thumbnail(ctx, "k") }},
		{"webhook", func() error { return c.DeliverWebhookAt(ctx, "d1", 1, at) }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
	}
	// duplicates (same task id) are not an error
	for _, s := range []int{0, 1, 2, 4, 6} {
		if err := steps[s].fn(); err != nil {
			t.Errorf("%s again: %v", steps[s].name, err)
		}
	}
	insp := asynq.NewInspectorFromRedisClient(rdb)
	defer insp.Close()
	q, err := insp.GetQueueInfo("default")
	if err != nil {
		t.Fatal(err)
	}
	// scheduled: publish, purge, webhook; pending: fanout->(retained pending), email x1, emailOnce, thumb
	if q.Scheduled != 3 || q.Pending != 4 {
		t.Errorf("scheduled=%d pending=%d", q.Scheduled, q.Pending)
	}
}

func TestCovRedisSentLog(t *testing.T) {
	ctx := context.Background()
	l := redisSentLog{covRedis(t)}
	if ok, err := l.Sent(ctx, "a"); err != nil || ok {
		t.Fatalf("before: %v %v", ok, err)
	}
	if err := l.MarkSent(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.Sent(ctx, "a"); err != nil || !ok {
		t.Fatalf("after: %v %v", ok, err)
	}
}

func TestCovWorkerRunsTasks(t *testing.T) {
	ctx := context.Background()
	rdb := covRedis(t)
	m := &covMailer{done: make(chan struct{}, 8)}
	w := NewWorker(rdb, covHandlers(duePosts{}, dueUsers{}, m, &memStorage{objs: map[string][]byte{}}))
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			w.Stop()
		}
	}()
	// a second Start fails (scheduler or server already running)
	if err := w.Start(); err == nil {
		t.Error("second Start succeeded")
	}
	c := NewClient(rdb)
	defer c.Close()
	if err := c.SendEmailOnce(ctx, "e1", application.Email{To: "a@b.c"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.done:
	case <-time.After(15 * time.Second):
		t.Fatal("email not sent by the worker")
	}
	// a failing job reaches the error handler (no retries left)
	m.mu.Lock()
	m.err = errors.New("smtp down")
	m.mu.Unlock()
	raw := asynq.NewClientFromRedisClient(rdb)
	defer raw.Close()
	if _, err := raw.Enqueue(asynq.NewTask(TypeSendEmail, []byte(`{"to":"x@y.z"}`)), asynq.MaxRetry(0)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.done:
	case <-time.After(15 * time.Second):
		t.Fatal("failing email not attempted")
	}
	time.Sleep(300 * time.Millisecond) // error handler runs after the attempt
	w.Stop()
	stopped = true
}

func TestCovWithIDGoodPayload(t *testing.T) {
	var got int
	h := withID(func(_ context.Context, id int) error { got = id; return nil })
	if err := h(context.Background(), asynq.NewTask("x", []byte(`{"id":7}`))); err != nil || got != 7 {
		t.Fatalf("%v %d", err, got)
	}
}

type covPutFail struct{ memStorage }

func (s *covPutFail) Put(ctx context.Context, key string, r io.Reader, n int64, ct string) error {
	return errors.New("storage down")
}

func TestCovThumbnailPutFails(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	st := &covPutFail{memStorage{objs: map[string][]byte{"ok": buf.Bytes()}}}
	if err := thumbnail(context.Background(), st, "ok"); err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want retryable storage error, got %v", err)
	}
	// the final (thumbnail) Put failing is also returned
	if err := thumbnail(context.Background(), &covPutFailLast{memStorage{objs: map[string][]byte{"ok": buf.Bytes()}}}, "ok"); err == nil {
		t.Fatal("want error from the thumbnail Put")
	}
}

type covPutFailLast struct{ memStorage }

func (s *covPutFailLast) Put(ctx context.Context, key string, r io.Reader, n int64, ct string) error {
	if ct == "image/jpeg" {
		return errors.New("storage down")
	}
	return s.memStorage.Put(ctx, key, r, n, ct)
}

func TestCovStartServerAlreadyRunning(t *testing.T) {
	rdb := covRedis(t)
	w := NewWorker(rdb, covHandlers(duePosts{}, dueUsers{}, &covMailer{}, nil))
	if err := w.srv.Start(w.mux); err != nil {
		t.Fatal(err)
	}
	defer w.srv.Shutdown()
	if err := w.Start(); err == nil {
		t.Fatal("want server already running error")
	}
	w.sched.Shutdown()
}
