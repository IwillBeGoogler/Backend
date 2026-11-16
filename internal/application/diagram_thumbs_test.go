package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

type fakeThumbQueue struct {
	mu  sync.Mutex
	ids []string
	err error
}

func (q *fakeThumbQueue) DiagramThumbnail(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, id)
	return q.err
}

func (q *fakeThumbQueue) queued() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.ids)
}

func TestGalleryQueuesMissingThumbnailsAndWorkerDrawsThem(t *testing.T) {
	d, mem, _ := adminWorld(t, 9, "templates.manage", "diagrams.read")
	blobs := &memBlobs{data: map[string][]byte{}}
	q := &fakeThumbQueue{}
	d.Blobs, d.ThumbJobs = blobs, q
	f := project(t, d, 9, "Source").File.ID
	if _, err := d.CreateTemplate(dctx, 9, NewTemplate{Title: "T", Kind: "system", FileID: f, Published: true}); err != nil {
		t.Fatal(err)
	}

	gal := must1[[]diagram.Template](t)(d.PublicTemplates(dctx))
	if len(gal) != 1 || gal[0].ThumbnailHash != nil {
		t.Fatalf("gallery = %+v", gal)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(q.queued()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := q.queued(); !slices.Equal(got, []string{f}) {
		t.Fatalf("queued = %v", got)
	}

	// the job draws an SVG preview and points the file at it
	if err := d.RenderThumbnail(dctx, f); err != nil {
		t.Fatal(err)
	}
	doc := must1[diagram.Doc](t)(d.Repo.File(dctx, f))
	if doc.ThumbnailHash == nil {
		t.Fatal("no thumbnail after render")
	}
	if b := blobs.data[blobKey(*doc.ThumbnailHash)]; !strings.HasPrefix(string(b), "<svg") {
		t.Errorf("blob = %.40q", b)
	}
	if a := must1[diagram.Asset](t)(mem.Asset(dctx, *doc.ThumbnailHash)); a.Mime != diagram.MimeSVG {
		t.Errorf("mime = %s", a.Mime)
	}

	// the next read has it and queues nothing more
	n := len(q.queued())
	gal = must1[[]diagram.Template](t)(d.PublicTemplates(dctx))
	if gal[0].ThumbnailHash == nil || *gal[0].ThumbnailHash != *doc.ThumbnailHash {
		t.Errorf("gallery thumb = %v", gal[0].ThumbnailHash)
	}
	time.Sleep(20 * time.Millisecond)
	if len(q.queued()) != n {
		t.Errorf("queued again: %v", q.queued())
	}
}

func TestRenderThumbnailKeepsExistingAndSkipsGone(t *testing.T) {
	d, mem := newDiagrams(t)
	d.Blobs = &memBlobs{data: map[string][]byte{}}
	f := project(t, d, alice, "P").File.ID
	if err := mem.SetThumbnail(dctx, f, "editor-made"); err != nil {
		t.Fatal(err)
	}
	if err := d.RenderThumbnail(dctx, f); err != nil {
		t.Fatal(err)
	}
	if doc := must1[diagram.Doc](t)(d.Repo.File(dctx, f)); *doc.ThumbnailHash != "editor-made" {
		t.Errorf("replaced the editor's preview: %s", *doc.ThumbnailHash)
	}
	if err := d.RenderThumbnail(dctx, "fil_gone"); err != nil {
		t.Errorf("gone file: %v", err)
	}
}

func TestQueueThumbnailsStopsOnQueueError(t *testing.T) {
	d, _ := newDiagrams(t)
	q := &fakeThumbQueue{err: errors.New("redis down")}
	d.ThumbJobs = q
	d.queueThumbnails(dctx, []string{"a", "b"})
	deadline := time.Now().Add(2 * time.Second)
	for len(q.queued()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if got := q.queued(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("queued = %v", got)
	}
	d.ThumbJobs = nil
	d.queueThumbnails(dctx, []string{"c"}) // no queue: nothing happens
}
