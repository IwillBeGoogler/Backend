package application

import (
	"context"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/clienterror"
)

// ReportClientError stores one JavaScript error from the web app. Anyone may
// send one, so there is no permission check; the sender is not recorded.
func (b *Blog) ReportClientError(ctx context.Context, in clienterror.Input) error {
	ctx, span := tracer.Start(ctx, "Blog.ReportClientError")
	defer span.End()
	e, err := clienterror.New(in)
	if err != nil {
		return err
	}
	return b.ClientErrors.Record(ctx, e, clienterror.MaxRows)
}

// ListClientErrors lists reported errors for moderators; empty status means all.
func (b *Blog) ListClientErrors(ctx context.Context, f clienterror.Filter) (clienterror.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.ListClientErrors")
	defer span.End()
	if err := b.Authz.Can(ctx, "report", "moderate", 0, 0); err != nil {
		return clienterror.Page{}, err
	}
	if err := clienterror.ValidateStatus(f.Status, true); err != nil {
		return clienterror.Page{}, err
	}
	return b.ClientErrors.List(ctx, f)
}

// DecideClientError sets an entry's status; open reopens it.
func (b *Blog) DecideClientError(ctx context.Context, actorID, id int, s clienterror.Status) (clienterror.ClientError, error) {
	ctx, span := tracer.Start(ctx, "Blog.DecideClientError")
	defer span.End()
	if err := b.Authz.Can(ctx, "report", "moderate", 0, 0); err != nil {
		return clienterror.ClientError{}, err
	}
	if err := clienterror.ValidateStatus(s, false); err != nil {
		return clienterror.ClientError{}, err
	}
	e, err := b.ClientErrors.SetStatus(ctx, id, s)
	if err == nil {
		b.Audit.Record(ctx, actorID, "client_error."+string(s), "client_error:"+itoa(id), nil)
	}
	return e, err
}

// PurgeClientErrors drops entries not seen for clienterror.Retention (worker sweep).
func (b *Blog) PurgeClientErrors(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "Blog.PurgeClientErrors")
	defer span.End()
	_, err := b.ClientErrors.PurgeBefore(ctx, b.now().Add(-clienterror.Retention))
	return err
}
