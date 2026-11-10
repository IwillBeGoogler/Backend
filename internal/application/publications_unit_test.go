package application

import (
	"context"
	"errors"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/publication"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// memPubs holds one publication "pub" and per-user roles.
type memPubs struct {
	publication.Repository
	roles   map[int]publication.Role
	absent  bool
	err     error
	roleErr error
	members int
}

func (r *memPubs) Create(_ context.Context, in publication.PublicationInput, owner int) (publication.Publication, error) {
	r.roles[owner] = publication.RoleOwner
	return publication.Publication{ID: 1, Slug: in.Slug}, r.err
}
func (r *memPubs) GetBySlug(_ context.Context, slug string, viewer int) (publication.Publication, error) {
	if r.absent {
		return publication.Publication{}, domain.ErrNotFound
	}
	return publication.Publication{ID: 1, Slug: slug, MyRole: r.roles[viewer]}, nil
}
func (r *memPubs) Update(context.Context, int, publication.PublicationInput) error { return r.err }
func (r *memPubs) Delete(context.Context, int) error                               { return r.err }
func (r *memPubs) ListForUser(context.Context, int) ([]publication.Publication, error) {
	return nil, r.err
}
func (r *memPubs) Role(_ context.Context, _, uid int) (publication.Role, error) {
	return r.roles[uid], r.roleErr
}
func (r *memPubs) Members(context.Context, int) ([]publication.Member, error) { return nil, r.err }
func (r *memPubs) SetMember(_ context.Context, _, uid int, role publication.Role) error {
	r.roles[uid] = role
	return r.err
}
func (r *memPubs) RemoveMember(_ context.Context, _, uid int) error {
	delete(r.roles, uid)
	return r.err
}

func TestBlogPublications(t *testing.T) {
	w := newFat(1, "publication.create")
	users := newMemUsers(user.User{ID: 1, Username: "ali"}, user.User{ID: 2, Username: "vali"}, user.User{ID: 3, Username: "sam"})
	pubs := &memPubs{roles: map[int]publication.Role{}}
	w.b.Users, w.b.Publications = users, pubs
	in := publication.PublicationInput{Slug: "daily-news", Name: "Daily"}

	if _, err := w.b.CreatePublication(ctx, ali, publication.PublicationInput{}); err == nil {
		t.Error("invalid input")
	}
	if _, err := newFat(1).b.CreatePublication(ctx, ali, in); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	w.b.RequireVerifiedEmail = true
	if _, err := w.b.CreatePublication(ctx, ali, in); !errors.Is(err, domain.ErrEmailNotVerified) {
		t.Error(err)
	}
	w.b.RequireVerifiedEmail = false
	if _, err := w.b.CreatePublication(ctx, ali, in); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.GetPublication(ctx, "daily-news", 1); err != nil {
		t.Error(err)
	}
	if _, err := w.b.MyPublications(ctx, ali); err != nil {
		t.Error(err)
	}
	if _, err := w.b.PublicationPosts(ctx, "daily-news", domain.Paging{}); err != nil {
		t.Error(err)
	}
	if _, err := w.b.PublicationMembers(ctx, "daily-news"); err != nil {
		t.Error(err)
	}

	// update: owners and editors only
	if _, err := w.b.UpdatePublication(ctx, ali, "daily-news", publication.PublicationInput{}); err == nil {
		t.Error("empty name")
	}
	if _, err := w.b.UpdatePublication(ctx, ali, "daily-news", in); err != nil {
		t.Error(err)
	}
	vali := user.User{ID: 2, Username: "vali"}
	if _, err := w.b.UpdatePublication(ctx, vali, "daily-news", in); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	pubs.err = errBoom
	if _, err := w.b.UpdatePublication(ctx, ali, "daily-news", in); err == nil {
		t.Error("update err")
	}
	pubs.err = nil

	// members: owner manages; editor manages writers only; nobody touches the owner
	if err := w.b.SetPublicationMember(ctx, ali, "daily-news", "vali", "boss"); err == nil {
		t.Error("bad role")
	}
	if err := w.b.SetPublicationMember(ctx, ali, "daily-news", "nobody", publication.RoleEditor); err == nil {
		t.Error("missing user")
	}
	if err := w.b.SetPublicationMember(ctx, ali, "daily-news", "vali", publication.RoleEditor); err != nil {
		t.Error(err)
	}
	if err := w.b.SetPublicationMember(ctx, vali, "daily-news", "sam", publication.RoleWriter); err != nil {
		t.Error(err)
	}
	sam := user.User{ID: 3, Username: "sam"}
	if err := w.b.SetPublicationMember(ctx, sam, "daily-news", "vali", publication.RoleWriter); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	pubs.roles[2] = publication.RoleEditor
	if err := w.b.SetPublicationMember(ctx, vali, "daily-news", "ali", publication.RoleWriter); !errors.Is(err, domain.ErrForbidden) {
		t.Error("editor touching the owner:", err)
	}
	pubs.roleErr = errBoom
	if err := w.b.SetPublicationMember(ctx, ali, "daily-news", "vali", publication.RoleWriter); err == nil {
		t.Error("role err")
	}
	pubs.roleErr = nil

	// remove
	if err := w.b.RemovePublicationMember(ctx, ali, "daily-news", "nobody"); err == nil {
		t.Error("missing user")
	}
	if err := w.b.RemovePublicationMember(ctx, ali, "daily-news", "ali"); !errors.Is(err, domain.ErrForbidden) {
		t.Error("owner cannot leave", err)
	}
	pubs.roles[3] = publication.RoleWriter
	if err := w.b.RemovePublicationMember(ctx, sam, "daily-news", "vali"); !errors.Is(err, domain.ErrForbidden) {
		t.Error("writer removing an editor", err)
	}
	delete(pubs.roles, 3)
	if err := w.b.RemovePublicationMember(ctx, ali, "daily-news", "sam"); !errors.Is(err, domain.ErrNotFound) {
		t.Error("not a member", err)
	}
	pubs.roles[3] = publication.RoleWriter
	if err := w.b.RemovePublicationMember(ctx, sam, "daily-news", "sam"); err != nil {
		t.Error("leaving", err)
	}
	pubs.roles[3] = publication.RoleWriter
	if err := w.b.RemovePublicationMember(ctx, ali, "daily-news", "sam"); err != nil {
		t.Error(err)
	}
	pubs.roleErr = errBoom
	if err := w.b.RemovePublicationMember(ctx, ali, "daily-news", "sam"); err == nil {
		t.Error("role err")
	}
	pubs.roleErr = nil

	// delete: owner only
	if err := w.b.DeletePublication(ctx, vali, "daily-news"); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	pubs.err = errBoom
	if err := w.b.DeletePublication(ctx, ali, "daily-news"); err == nil {
		t.Error("delete err")
	}
	pubs.err = nil
	if err := w.b.DeletePublication(ctx, ali, "daily-news"); err != nil || len(w.a.actions) != 1 {
		t.Error(err, w.a.actions)
	}

	// missing publication everywhere
	pubs.absent = true
	if _, err := w.b.PublicationPosts(ctx, "x", domain.Paging{}); err == nil {
		t.Error("missing")
	}
	if _, err := w.b.PublicationMembers(ctx, "x"); err == nil {
		t.Error("missing")
	}
	if err := w.b.RemovePublicationMember(ctx, ali, "x", "vali"); err == nil {
		t.Error("missing")
	}
	if err := w.b.DeletePublication(ctx, ali, "x"); err == nil {
		t.Error("missing")
	}
}

func TestBlogRequireWriterAndEditor(t *testing.T) {
	w := newFat(1, "post.create")
	pubs := &memPubs{roles: map[int]publication.Role{1: publication.RoleWriter, 2: publication.RoleEditor}}
	w.b.Publications = pubs
	if err := w.b.requireWriter(ctx, ali, 0); err != nil {
		t.Error(err)
	}
	if err := w.b.requireWriter(ctx, ali, 1); err != nil {
		t.Error(err)
	}
	if err := w.b.requireWriter(ctx, user.User{ID: 9}, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	pubs.roleErr = errBoom
	if err := w.b.requireWriter(ctx, ali, 1); err == nil {
		t.Error("role err")
	}
	pubs.roleErr = nil
	if w.b.isPublicationEditor(ctx, ali, 1) || !w.b.isPublicationEditor(ctx, user.User{ID: 2}, 1) ||
		w.b.isPublicationEditor(ctx, ali, 0) || w.b.isPublicationEditor(ctx, user.User{}, 1) {
		t.Error("isPublicationEditor")
	}

	// CreatePost / UpdatePost into a publication, and an editor updating a writer's post
	p := pub(1, 7)
	p.PublicationID = 1
	w.p.posts[1] = p
	cp := &createPosts{fatPosts: w.p}
	w.b.Posts = &updPosts{createPosts: cp}
	if _, err := w.b.CreatePost(ctx, ali, post.Draft{Title: "t", Body: "b", PublicationID: 1}); err != nil {
		t.Error(err)
	}
	if _, err := w.b.CreatePost(ctx, user.User{ID: 9}, post.Draft{Title: "t", Body: "b", PublicationID: 1}); err == nil {
		t.Error("non-member posted")
	}
}

type updPosts struct{ *createPosts }

func (u *updPosts) Update(_ context.Context, id int, d post.Draft) (post.Post, error) {
	p := u.posts[id]
	p.Title = d.Title
	u.posts[id] = p
	return p, nil
}
