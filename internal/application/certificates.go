package application

import (
	"context"
	"errors"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/challenge"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// Certificates

// ClaimCertificate issues the owner's certificate for an eligible challenge.
// A challenge has at most one: claiming again returns it (created false),
// revoked or not.
func (b *Blog) ClaimCertificate(ctx context.Context, actor user.User, id int) (challenge.Certificate, bool, error) {
	ctx, span := tracer.Start(ctx, "Blog.ClaimCertificate")
	defer span.End()
	var none challenge.Certificate
	c, err := b.Challenges.Get(ctx, id)
	switch {
	case err != nil:
		return none, false, err
	case !b.canSee(ctx, c, actor.ID):
		return none, false, domain.ErrNotFound
	case c.UserID != actor.ID:
		return none, false, domain.ErrForbidden
	}
	got, err := b.Challenges.CertificateOf(ctx, id)
	if !errors.Is(err, domain.ErrNotFound) {
		return got, false, err
	}
	days, err := b.Challenges.Days(ctx, id)
	if err != nil {
		return none, false, err
	}
	done := make([]string, len(days))
	for i, d := range days {
		done[i] = d.Date
	}
	now := b.now()
	stats := challenge.Compute(c.StartDate, c.Days, done, challenge.Today(c.Timezone, now))
	if err := challenge.Eligible(c.Days, stats); err != nil {
		return none, false, err
	}
	name := actor.DisplayName
	if name == "" {
		name = actor.Username
	}
	return b.Challenges.IssueCertificate(ctx, id, challenge.Certificate{
		ID: challenge.NewCertificateID(), UserID: actor.ID, HolderName: name, Username: actor.Username,
		Title: c.Title, Days: c.Days, DaysDone: stats.DaysDone, BestStreak: stats.BestStreak,
		StartDate: c.StartDate, EndDate: stats.EndDate, IssuedAt: now,
	})
}

// Certificate is the public record behind a certificate's QR code.
func (b *Blog) Certificate(ctx context.Context, id string) (challenge.Certificate, error) {
	ctx, span := tracer.Start(ctx, "Blog.Certificate")
	defer span.End()
	if !challenge.ValidCertificateID(id) {
		return challenge.Certificate{}, domain.ErrNotFound
	}
	return b.Challenges.Certificate(ctx, id)
}

// AdminCertificates lists all certificates.
func (b *Blog) AdminCertificates(ctx context.Context, p domain.Paging) (challenge.CertificatePage, error) {
	ctx, span := tracer.Start(ctx, "Blog.AdminCertificates")
	defer span.End()
	if err := b.Authz.Can(ctx, "certificate", "revoke", 0, 0); err != nil {
		return challenge.CertificatePage{}, err
	}
	return b.Challenges.ListCertificates(ctx, p)
}

// RevokeCertificate withdraws a certificate for good.
func (b *Blog) RevokeCertificate(ctx context.Context, actor user.User, id, reason string) (challenge.Certificate, error) {
	ctx, span := tracer.Start(ctx, "Blog.RevokeCertificate")
	defer span.End()
	var none challenge.Certificate
	if err := b.Authz.Can(ctx, "certificate", "revoke", 0, 0); err != nil {
		return none, err
	}
	reason, err := challenge.RevokeReason(reason)
	if err != nil {
		return none, err
	}
	if !challenge.ValidCertificateID(id) {
		return none, domain.ErrNotFound
	}
	c, err := b.Challenges.RevokeCertificate(ctx, id, reason)
	if err != nil {
		return none, err
	}
	b.Audit.Record(ctx, actor.ID, "certificate.revoke", "certificate:"+id, map[string]any{"reason": reason})
	return c, nil
}
