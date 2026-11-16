package guardauth_test

import (
	"context"
	"testing"
	"time"

	sessionapp "github.com/bakhod1r/guard/session/application"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/guardauth"
)

// Sessions are stored in Postgres (guard_session): a Redis flush does not end them.
func TestSessionsLiveInPostgres(t *testing.T) {
	db, rdb := infra(t)
	ctx := context.Background()
	opts := fastHash
	opts.SessionIdle = time.Hour
	opts.Migrate = true
	opts.AccessRules = guardauth.NewAccessRules("", "")
	must(t, guardauth.Migrate(ctx, db, rdb, opts)) // Guard's migrations, including guard_session
	g, err := guardauth.Open(ctx, db, rdb, opts)
	must(t, err)
	t.Cleanup(func() { g.Close(context.Background()) })

	sess, tok, err := g.Sessions.Start(ctx, sessionapp.StartInput{UserID: "42", IP: "203.0.113.5", UserAgent: "test", Locale: "uz", LocaleSource: "header"})
	must(t, err)
	var n int
	must(t, db.QueryRow(ctx, `SELECT count(*) FROM guard_session WHERE id = $1 AND status = 'active'`, string(sess.ID)).Scan(&n))
	if n != 1 {
		t.Fatalf("session rows = %d", n)
	}
	if keys, _ := rdb.Keys(ctx, "guard:session:*").Result(); len(keys) != 0 {
		t.Errorf("session written to Redis: %v", keys)
	}
	must(t, rdb.FlushAll(ctx).Err())
	got, err := g.Sessions.Resolve(ctx, tok)
	if err != nil || got.IP != "203.0.113.5" || got.Locale != "uz" {
		t.Fatalf("after Redis flush: %+v %v", got, err)
	}
}
