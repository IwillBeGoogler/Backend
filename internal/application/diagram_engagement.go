package application

import (
	"context"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
)

// Claps and bookmarks on published diagrams, as on posts. They work on
// unlisted and public files; a private or unknown slug is not found.

// DiagramClaps is the answer to a clap: the file's total and the reader's own.
type DiagramClaps struct {
	Claps   int
	MyClaps int
}

func (d *Diagrams) published(ctx context.Context, slug string) (diagram.Listing, error) {
	if d.Engage == nil || !diagram.ValidSlug(slug) {
		return diagram.Listing{}, diagram.NotFound("Diagram")
	}
	l, err := d.Engage.PublicListing(ctx, slug)
	return l, notFoundAs(err, "Diagram")
}

// ClapDiagram adds n claps (capped at diagram.MaxClaps per reader). The
// author is told of the reader's first clap only.
func (d *Diagrams) ClapDiagram(ctx context.Context, actor int, slug string, n int) (DiagramClaps, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ClapDiagram")
	defer span.End()
	if err := diagram.ValidateClaps(n); err != nil {
		return DiagramClaps{}, err
	}
	l, err := d.published(ctx, slug)
	if err != nil {
		return DiagramClaps{}, err
	}
	total, mine, created, err := d.Engage.Clap(ctx, l.FileID, actor, n)
	if err != nil {
		return DiagramClaps{}, notFoundAs(err, "Diagram")
	}
	if created && d.Notices != nil {
		who := "Someone"
		if d.Users != nil {
			if u, err := d.Users.GetByID(ctx, actor); err == nil {
				who = firstNonEmpty(u.DisplayName, u.Username, who)
			}
		}
		d.Notices.NotifyDiagram(ctx, social.Notification{Type: social.NotifyDiagramClap, ActorID: actor,
			DiagramProjectID: l.ProjectID, DiagramFileID: l.FileID, Text: who + " clapped for " + firstNonEmpty(l.Title, l.Name, "your diagram")}, l.AuthorID)
	}
	return DiagramClaps{Claps: total, MyClaps: mine}, nil
}

// BookmarkDiagram saves a published diagram to the reader's library (idempotent).
func (d *Diagrams) BookmarkDiagram(ctx context.Context, actor int, slug string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.BookmarkDiagram")
	defer span.End()
	l, err := d.published(ctx, slug)
	if err != nil {
		return err
	}
	return notFoundAs(d.Engage.Bookmark(ctx, l.FileID, actor), "Diagram")
}

// UnbookmarkDiagram removes a diagram from the library; fine when it was not saved.
func (d *Diagrams) UnbookmarkDiagram(ctx context.Context, actor int, slug string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.UnbookmarkDiagram")
	defer span.End()
	l, err := d.published(ctx, slug)
	if err != nil {
		return err
	}
	return d.Engage.Unbookmark(ctx, l.FileID, actor)
}

// DiagramViewer is what the reader did to a published diagram (zero for an
// anonymous reader).
func (d *Diagrams) DiagramViewer(ctx context.Context, viewer int, slug string) (diagram.Viewer, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.DiagramViewer")
	defer span.End()
	l, err := d.published(ctx, slug)
	if err != nil || viewer == 0 {
		return diagram.Viewer{}, err
	}
	return d.Engage.Viewer(ctx, l.FileID, viewer)
}

// BookmarkedDiagrams lists the reader's saved diagrams, newest saved first.
func (d *Diagrams) BookmarkedDiagrams(ctx context.Context, actor int, p domain.Paging) (PublicDiagramPage, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.BookmarkedDiagrams")
	defer span.End()
	out := PublicDiagramPage{Items: []diagram.Listing{}, Page: p.Page, Limit: p.Limit}
	if d.Engage == nil {
		return out, nil
	}
	items, total, err := d.Engage.ListBookmarked(ctx, actor, p.Page, p.Limit)
	d.withViews(ctx, items)
	if items != nil {
		out.Items = items
	}
	out.Total = total
	return out, err
}

// ViewDiagram counts a unique view of a published (public or unlisted)
// file by viewer (user id or IP). A private or unknown slug is not found.
func (d *Diagrams) ViewDiagram(ctx context.Context, slug, viewer string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.ViewDiagram")
	defer span.End()
	l, err := d.published(ctx, slug)
	if err != nil || d.Views == nil {
		return err
	}
	return d.Views.Hit(ctx, l.FileID, viewer)
}

// withViews fills Views of listings from the view counter.
func (d *Diagrams) withViews(ctx context.Context, ls []diagram.Listing) {
	if d.Views == nil || len(ls) == 0 {
		return
	}
	ids := make([]string, len(ls))
	for i, l := range ls {
		ids[i] = l.FileID
	}
	counts := d.Views.Counts(ctx, ids)
	for i := range ls {
		ls[i].Views = counts[ls[i].FileID]
	}
}
