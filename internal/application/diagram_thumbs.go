package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// ThumbnailQueue schedules drawing a missing file preview in the background.
type ThumbnailQueue interface {
	// DiagramThumbnail enqueues at most one pending job per file.
	DiagramThumbnail(ctx context.Context, fileID string) error
}

// ErrThumbnailUnreadable marks a file whose document cannot be drawn:
// retrying gives the same answer.
var ErrThumbnailUnreadable = errors.New("thumbnail: unreadable document")

// queueThumbnails asks the worker to draw previews for files that have none.
// It runs detached from the request (a read must not wait on Redis, and a
// cancelled request must not drop the jobs) and only logs failures.
func (d *Diagrams) queueThumbnails(ctx context.Context, fileIDs []string) {
	if d.ThumbJobs == nil || len(fileIDs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	go func() {
		defer cancel()
		for _, id := range fileIDs {
			if err := d.ThumbJobs.DiagramThumbnail(ctx, id); err != nil {
				zap.L().Warn("queue diagram thumbnail", zap.String("file", id), zap.Error(err))
				return // Redis is down: the next read tries again
			}
		}
	}()
}

// RenderThumbnail draws and stores the preview of a file that has none (the
// worker's job). A file that got one meanwhile, or is gone, is left alone.
func (d *Diagrams) RenderThumbnail(ctx context.Context, fileID string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.RenderThumbnail")
	defer span.End()
	if d.Sharing == nil || d.Blobs == nil {
		return nil
	}
	f, err := d.Repo.File(ctx, fileID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if f.ThumbnailHash != nil {
		return nil
	}
	svg, err := diagram.RenderThumbnailSVG(f.Data)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrThumbnailUnreadable, fileID, err)
	}
	// Our own markup (escaped ids, checked colours): stored as is, not run
	// through the upload sanitizer.
	a, err := d.putAsset(ctx, 0, svg, diagram.MimeSVG)
	if err != nil {
		return err
	}
	_, err = d.Sharing.FillThumbnail(ctx, fileID, a.Hash)
	return err
}
