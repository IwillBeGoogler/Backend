package application

import (
	"context"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/feedback"
)

// SubmitFeedback stores a bug report or idea. Anyone may send one, so there
// is no permission check; userID is 0 for an anonymous sender.
func (b *Blog) SubmitFeedback(ctx context.Context, userID int, in feedback.Input) (feedback.Feedback, error) {
	ctx, span := tracer.Start(ctx, "Blog.SubmitFeedback")
	defer span.End()
	f, err := feedback.New(userID, in)
	if err != nil {
		return f, err
	}
	return b.Feedback.Create(ctx, f)
}

// ListFeedback lists feedback for moderators; empty status or kind means all.
func (b *Blog) ListFeedback(ctx context.Context, f feedback.Filter) (feedback.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.ListFeedback")
	defer span.End()
	if err := b.Authz.Can(ctx, "report", "moderate", 0, 0); err != nil {
		return feedback.Page{}, err
	}
	if err := feedback.ValidateStatus(f.Status, true); err != nil {
		return feedback.Page{}, err
	}
	if err := feedback.ValidateKind(f.Kind); err != nil {
		return feedback.Page{}, err
	}
	return b.Feedback.List(ctx, f)
}

// DecideFeedback sets a feedback entry's status; open reopens it.
func (b *Blog) DecideFeedback(ctx context.Context, actorID, id int, s feedback.Status) (feedback.Feedback, error) {
	ctx, span := tracer.Start(ctx, "Blog.DecideFeedback")
	defer span.End()
	if err := b.Authz.Can(ctx, "report", "moderate", 0, 0); err != nil {
		return feedback.Feedback{}, err
	}
	if err := feedback.ValidateStatus(s, false); err != nil {
		return feedback.Feedback{}, err
	}
	f, err := b.Feedback.SetStatus(ctx, id, s, actorID)
	if err == nil {
		b.Audit.Record(ctx, actorID, "feedback."+string(s), "feedback:"+itoa(id), nil)
	}
	return f, err
}
