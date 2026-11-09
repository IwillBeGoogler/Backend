package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
)

func TestReplayedProjectRebuildsMissingFile(t *testing.T) {
	d, _ := newDiagrams(t)
	in := NewDiagramProject{ID: "prj_replay001", Name: "R", File: &NewDiagramFile{ID: "fil_replay001"}}
	first := must1[DiagramProject](t)(d.CreateProject(dctx, alice, in))
	if err := d.DeleteFile(dctx, alice, first.File.ID); err != nil {
		t.Fatal(err)
	}
	again, err := d.CreateProject(dctx, alice, in)
	if err != nil || again.Created || again.File.ID == "" {
		t.Errorf("replay without file: %+v %v", again, err)
	}
	// somebody else's id is taken
	if _, err := d.CreateProject(dctx, bob, in); err == nil {
		t.Error("another user reused the id")
	}
}

func TestIDTakenMapsConflict(t *testing.T) {
	if idTaken(domain.ErrConflict) != diagram.ErrIDTaken {
		t.Error("conflict")
	}
	other := errors.New("x")
	if idTaken(other) != other {
		t.Error("passthrough")
	}
}

func TestTokenLimitHidesOtherProjects(t *testing.T) {
	d, _ := newDiagrams(t)
	a := project(t, d, alice, "A")
	b := project(t, d, alice, "B")
	ctx := WithTokenLimit(dctx, []string{a.Project.ID})
	if _, err := d.GetProject(ctx, alice, a.Project.ID); err != nil {
		t.Errorf("allowed project: %v", err)
	}
	if _, err := d.GetProject(ctx, alice, b.Project.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("limited project: %v", err)
	}
	if WithTokenLimit(dctx, nil) != dctx {
		t.Error("nil limit must not wrap")
	}
}

func TestDeleteFolderRespectsFileOverrides(t *testing.T) {
	d, mem := newDiagrams(t)
	p := project(t, d, alice, "A")
	pid := p.Project.ID
	mem.AddMember(pid, bob, diagram.RoleEditor)
	folder, _, _ := d.CreateFolder(dctx, alice, pid, NewFolder{Name: "F"})
	f, _, err := d.CreateFile(dctx, alice, pid, NewDiagramFile{Name: "In", FolderID: &folder.ID})
	if err != nil {
		t.Fatal(err)
	}
	mem.SetOverride(f.ID, bob, diagram.RoleViewer)
	if _, _, err := d.DeleteFolder(dctx, bob, folder.ID); err == nil {
		t.Error("editor deleted a folder holding a file they may not delete")
	}
	n, files, err := d.DeleteFolder(dctx, alice, folder.ID)
	if err != nil || n != 1 || files != 1 {
		t.Errorf("owner delete: %d %d %v", n, files, err)
	}
	if _, _, err := d.DeleteFolder(dctx, alice, "fld_missing"); err == nil {
		t.Error("missing folder")
	}
}

func TestWebhooksNotConfigured(t *testing.T) {
	d, _ := newDiagrams(t)
	d.Webhooks, d.Hooks = nil, nil
	p := project(t, d, alice, "A")
	pid := p.Project.ID
	if _, err := d.ListWebhooks(dctx, alice, pid); err == nil {
		t.Error("list")
	}
	if _, err := d.CreateWebhook(dctx, alice, pid, "https://x.test", nil); err == nil {
		t.Error("create")
	}
	if _, err := d.UpdateWebhook(dctx, alice, pid, "whk_x", WebhookPatch{}); err == nil {
		t.Error("update")
	}
	if _, err := d.WebhookDeliveries(dctx, alice, pid, "whk_x"); err == nil {
		t.Error("deliveries")
	}
	if _, err := d.TestWebhook(dctx, alice, pid, "whk_x"); err == nil {
		t.Error("test")
	}
	w := NewWebhookDispatcher(nil, nil, nil, nil)
	if w.checkURL(dctx, "https://x.test") != nil {
		t.Error("nil check must allow")
	}
	w.Check = func(context.Context, string) error { return errors.New("blocked") }
	if w.checkURL(dctx, "https://x.test") == nil {
		t.Error("check ignored")
	}
	var sink []DiagramEvent
	f := EventsFanout{nil, eventFunc(func(e DiagramEvent) { sink = append(sink, e) })}
	f.Emit(dctx, DiagramEvent{Kind: EventFileDeleted})
	if len(sink) != 1 {
		t.Error("fanout")
	}
}

type eventFunc func(DiagramEvent)

func (f eventFunc) Emit(_ context.Context, e DiagramEvent) { f(e) }

type purgeRepo struct {
	AnalyticsRepo
	before time.Time
}

func (p *purgeRepo) PurgeBefore(_ context.Context, t time.Time) (int64, error) {
	p.before = t
	return 1, nil
}

func TestAnalyticsPurgeAndHelpers(t *testing.T) {
	repo := &purgeRepo{}
	a := NewAnalytics(repo, nil)
	if err := a.PurgeOld(dctx); err != nil || repo.before.IsZero() {
		t.Errorf("purge: %v", err)
	}
	var nilA *Analytics
	if nilA.PurgeOld(dctx) != nil {
		t.Error("nil purge")
	}
	nilA.Record(dctx, "x", 1, 1)
	if refHost(" https://News.Example.com/a?b=1 ") != "news.example.com" || refHost("") != "" || refHost("host/x?y") != "host" {
		t.Error("refHost")
	}
	if clip("  abcdef ", 3) != "abc" || clip("ab", 5) != "ab" {
		t.Error("clip")
	}
	if err := a.Track(dctx, AnalyticsEvent{Name: "nope"}); err == nil {
		t.Error("unknown event")
	}
}

var _ = diagramtest.NewMemory
