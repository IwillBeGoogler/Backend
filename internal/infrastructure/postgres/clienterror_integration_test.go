package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/clienterror"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
)

func TestClientErrorRepo(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	repo := postgres.NewClientErrorRepo(db)
	mk := func(msg string) clienterror.ClientError {
		e, err := clienterror.New(clienterror.Input{Message: msg, PageURL: "https://x.test/" + msg})
		must(t, err)
		return e
	}
	list := func(s clienterror.Status) clienterror.Page {
		p, err := repo.List(ctx, clienterror.Filter{Status: s, Paging: domain.NewPaging(1, 50)})
		must(t, err)
		return p
	}

	// Same error counts up in one row; page and agent follow the latest.
	must(t, repo.Record(ctx, mk("ce-a"), 100))
	must(t, repo.Record(ctx, mk("ce-a"), 100))
	must(t, repo.Record(ctx, mk("ce-a"), 100))
	p := list("")
	if p.Total != 1 || p.Items[0].Count != 3 || p.Items[0].Status != clienterror.StatusOpen {
		t.Fatalf("after 3 = %+v", p)
	}
	id := p.Items[0].ID

	// Resolved reopens on recurrence; ignored stays ignored but still counts.
	_, err := repo.SetStatus(ctx, id, clienterror.StatusResolved)
	must(t, err)
	must(t, repo.Record(ctx, mk("ce-a"), 100))
	if got := list("").Items[0]; got.Status != clienterror.StatusOpen || got.Count != 4 {
		t.Errorf("resolved then seen = %+v", got)
	}
	_, err = repo.SetStatus(ctx, id, clienterror.StatusIgnored)
	must(t, err)
	must(t, repo.Record(ctx, mk("ce-a"), 100))
	if got := list("").Items[0]; got.Status != clienterror.StatusIgnored || got.Count != 5 {
		t.Errorf("ignored then seen = %+v", got)
	}
	if _, err := repo.SetStatus(ctx, 999999, clienterror.StatusOpen); err == nil {
		t.Error("missing id: want error")
	}

	// Cap: at the limit a new fingerprint is dropped, a known one still counts.
	must(t, repo.Record(ctx, mk("ce-b"), 2))
	must(t, repo.Record(ctx, mk("ce-c"), 2))
	p = list("")
	if p.Total != 2 {
		t.Fatalf("rows = %d, want 2 (cap)", p.Total)
	}
	must(t, repo.Record(ctx, mk("ce-b"), 2))
	if got := list("").Items[0]; got.Message != "ce-b" || got.Count != 2 {
		t.Errorf("known fingerprint at cap = %+v", got)
	}

	// Newest last_seen first; status filter.
	if p := list(""); p.Items[0].Message != "ce-b" || p.Items[1].Message != "ce-a" {
		t.Errorf("order = %q, %q", p.Items[0].Message, p.Items[1].Message)
	}
	if p := list(clienterror.StatusIgnored); p.Total != 1 || p.Items[0].Message != "ce-a" {
		t.Errorf("ignored filter = %+v", p)
	}

	// Purge removes only old rows.
	n, err := repo.PurgeBefore(ctx, time.Now().Add(-time.Hour))
	must(t, err)
	if n != 0 {
		t.Errorf("purged %d fresh rows", n)
	}
	_, err = db.Exec(ctx, `UPDATE client_errors SET last_seen = now() - interval '91 days' WHERE id = $1`, id)
	must(t, err)
	n, err = repo.PurgeBefore(ctx, time.Now().Add(-clienterror.Retention))
	must(t, err)
	if n != 1 || list("").Total != 1 {
		t.Errorf("purged %d, left %d; want 1 and 1", n, list("").Total)
	}
}
