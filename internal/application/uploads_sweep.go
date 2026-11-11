package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// UploadSweeper deletes images that were uploaded but are not used anywhere:
// the story was never saved, the image was replaced or cut out of the text.
// An image counts as unused when no story (or revision), comment, profile,
// publication, series or list mentions its key, and it is older than Grace,
// which leaves time for a draft that has not been saved yet. Only images in
// the upload ledger are swept; older ones are left alone.
type UploadSweeper struct {
	Ledger  UnusedUploads
	Storage ObjectRemover
	// Grace is how old an image must be before it can be removed.
	Grace time.Duration
	// Batch caps the images handled per run.
	Batch int
	Now   func() time.Time
}

// Sweep removes one batch: the original, its thumbnail and WebP variants,
// then the ledger row. A failure leaves the row, so the next run retries.
// A nil sweeper (the feature is off) does nothing.
func (s *UploadSweeper) Sweep(ctx context.Context) error {
	if s == nil || s.Grace <= 0 {
		return nil
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	keys, err := s.Ledger.Unused(ctx, now().Add(-s.Grace), max(s.Batch, 1))
	if err != nil || len(keys) == 0 {
		return err
	}
	var errs []error
	removed := make([]string, 0, len(keys))
	for _, key := range keys {
		objects := []string{key, ThumbKey(key)}
		for _, w := range VariantWidths {
			objects = append(objects, VariantKey(key, w))
		}
		var failed error
		for _, o := range objects {
			if err := s.Storage.Remove(ctx, o); err != nil {
				failed = err
				break
			}
		}
		if failed != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", key, failed))
			continue
		}
		removed = append(removed, key)
	}
	if len(removed) > 0 {
		if err := s.Ledger.Forget(ctx, removed); err != nil {
			errs = append(errs, err)
		} else {
			zap.L().Info("removed unused uploads", zap.Int("count", len(removed)))
		}
	}
	return errors.Join(errs...)
}
