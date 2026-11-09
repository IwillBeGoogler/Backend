package application

import (
	"context"
	"errors"
	"strings"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// Publishing a diagram file to the blog: the owner of the project picks
// unlisted (open by link) or public (also on the author's profile and in
// search). The published page shows the file's live document; editing the
// file updates it.

// PublishRequest is what an author sends when publishing.
type PublishRequest struct {
	Visibility  diagram.Visibility
	Title       string
	Description string
	Tags        []string
}

func (d *Diagrams) needPublish() error {
	if d.Publish == nil {
		return errors.New("diagram publish repository not configured")
	}
	return nil
}

// PublishFile publishes a file, or updates its page. Only the project's
// owner may publish. The slug is made once and kept, so the link survives
// an unpublish and a republish.
func (d *Diagrams) PublishFile(ctx context.Context, actor int, fileID string, in PublishRequest) (diagram.Listing, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.PublishFile")
	defer span.End()
	if err := d.needPublish(); err != nil {
		return diagram.Listing{}, err
	}
	_, f, err := d.fileAccess(ctx, actor, fileID, diagram.RoleOwner)
	if err != nil {
		return diagram.Listing{}, err
	}
	pub, err := diagram.CleanPublication(in.Visibility, in.Title, in.Description, in.Tags, f.Name)
	if err != nil {
		return diagram.Listing{}, err
	}
	cur, err := d.Publish.Listing(ctx, fileID)
	if err != nil {
		return cur, notFoundAs(err, "File")
	}
	pub.Slug, pub.PublishedAt = cur.Slug, cur.PublishedAt
	if pub.PublishedAt.IsZero() {
		pub.PublishedAt = d.clock()
	}
	for attempt := 0; ; attempt++ {
		if pub.Slug == "" {
			pub.Slug = diagram.NewSlug(pub.Title)
		}
		err = d.Publish.SetPublication(ctx, fileID, pub)
		if !errors.Is(err, domain.ErrConflict) || cur.Slug != "" || attempt >= 4 {
			break
		}
		pub.Slug = ""
	}
	if err != nil {
		return cur, notFoundAs(err, "File")
	}
	return d.Publish.Listing(ctx, fileID)
}

// UnpublishFile makes a file private again. It is fine on a file that is
// not published.
func (d *Diagrams) UnpublishFile(ctx context.Context, actor int, fileID string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.UnpublishFile")
	defer span.End()
	if err := d.needPublish(); err != nil {
		return err
	}
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleOwner); err != nil {
		return err
	}
	return notFoundAs(d.Publish.ClearPublication(ctx, fileID), "File")
}

// FilePublication returns a file's blog page to anyone who can read the
// file (private, with no slug, for a file never published).
func (d *Diagrams) FilePublication(ctx context.Context, actor int, fileID string) (diagram.Listing, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.FilePublication")
	defer span.End()
	if err := d.needPublish(); err != nil {
		return diagram.Listing{}, err
	}
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleViewer); err != nil {
		return diagram.Listing{}, err
	}
	l, err := d.Publish.Listing(ctx, fileID)
	return l, notFoundAs(err, "File")
}

func notFoundAs(err error, what string) error {
	if errors.Is(err, domain.ErrNotFound) {
		return diagram.NotFound(what)
	}
	return err
}

// PublicDiagram opens a published file by its slug; unlisted files open too.
func (d *Diagrams) PublicDiagram(ctx context.Context, slug string) (diagram.PublicDoc, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.PublicDiagram")
	defer span.End()
	if d.Publish == nil || !diagram.ValidSlug(slug) {
		return diagram.PublicDoc{}, diagram.NotFound("Diagram")
	}
	doc, err := d.Publish.PublicBySlug(ctx, slug)
	if err == nil && d.Views != nil {
		doc.Views = d.Views.Count(ctx, doc.FileID)
	}
	return doc, notFoundAs(err, "Diagram")
}

// PublicDiagramPage is one page of public diagrams.
type PublicDiagramPage struct {
	Items []diagram.Listing
	Total int
	Page  int
	Limit int
}

// Paginated lets the HTTP layer send {data, meta}.
func (p PublicDiagramPage) Paginated() (any, any) {
	return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, "")
}

// PublicDiagrams lists public diagrams, newest first, filtered by a text
// search over title and description and by tag.
func (d *Diagrams) PublicDiagrams(ctx context.Context, q, tag string, p domain.Paging) (PublicDiagramPage, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.PublicDiagrams")
	defer span.End()
	return d.listPublic(ctx, diagram.PublicQuery{Q: strings.TrimSpace(q), Tag: diagram.NormalizeTag(tag), Page: p.Page, Limit: p.Limit})
}

// UserDiagrams lists a user's public diagrams for their profile.
func (d *Diagrams) UserDiagrams(ctx context.Context, username string, p domain.Paging) (PublicDiagramPage, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.UserDiagrams")
	defer span.End()
	if d.Users == nil {
		return PublicDiagramPage{}, diagram.NotFound("User")
	}
	u, err := d.Users.GetByUsername(ctx, username)
	if err != nil || u.DeletedAt != nil {
		if err == nil {
			err = domain.ErrNotFound
		}
		return PublicDiagramPage{}, err
	}
	return d.listPublic(ctx, diagram.PublicQuery{AuthorID: u.ID, Page: p.Page, Limit: p.Limit})
}

// PublishedDiagrams lists the caller's own unlisted and public files,
// newest published first (the story editor's diagram picker).
func (d *Diagrams) PublishedDiagrams(ctx context.Context, actor, page, limit int) (PublicDiagramPage, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.PublishedDiagrams")
	defer span.End()
	out := PublicDiagramPage{Items: []diagram.Listing{}, Page: page, Limit: limit}
	if d.Publish == nil {
		return out, nil
	}
	items, total, err := d.Publish.ListOwned(ctx, actor, page, limit)
	if items != nil {
		out.Items = items
	}
	out.Total = total
	return out, err
}

func (d *Diagrams) listPublic(ctx context.Context, q diagram.PublicQuery) (PublicDiagramPage, error) {
	page := PublicDiagramPage{Items: []diagram.Listing{}, Page: q.Page, Limit: q.Limit}
	if d.Publish == nil {
		return page, nil
	}
	if len([]rune(q.Q)) > 100 {
		return page, diagram.Validation("q: at most 100 characters")
	}
	items, total, err := d.Publish.ListPublic(ctx, q)
	d.withViews(ctx, items)
	if items != nil {
		page.Items = items
	}
	page.Total = total
	return page, err
}

// EmbeddedDiagram is what a post embed needs of a diagram: where it lives
// and how it looks. ok is false for a file that is missing or not
// published, which an embed then leaves out.
type EmbeddedDiagram struct {
	FileID        string
	Slug          string
	Title         string
	ThumbnailHash *string
}

// EmbeddedDiagrams resolves post embeds: each ref is a file id or a slug,
// and the result is keyed by the ref as given.
func (d *Diagrams) EmbeddedDiagrams(ctx context.Context, refs []string) map[string]EmbeddedDiagram {
	out := map[string]EmbeddedDiagram{}
	if d.Publish == nil || len(refs) == 0 {
		return out
	}
	found, err := d.Publish.ReferencedPublic(ctx, refs)
	if err != nil {
		return out
	}
	for ref, l := range found {
		out[ref] = EmbeddedDiagram{FileID: l.FileID, Slug: l.Slug, Title: firstNonEmpty(l.Title, l.Name, ""), ThumbnailHash: l.ThumbnailHash}
	}
	return out
}
