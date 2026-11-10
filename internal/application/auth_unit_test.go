package application

import (
	"errors"
	"testing"
	"time"

	"context"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

var errBoom = errors.New("boom")

type memUsers struct {
	user.Repository
	byID     map[int]user.User
	err      error // returned by Create/MarkEmailVerified/Follow when set
	deleted  []int
	verified []int
	purged   map[int]bool
	followed bool
	listErr  error
}

func newMemUsers(us ...user.User) *memUsers {
	m := &memUsers{byID: map[int]user.User{}, purged: map[int]bool{}}
	for _, u := range us {
		m.byID[u.ID] = u
	}
	return m
}

func (m *memUsers) find(f func(user.User) bool) (user.User, error) {
	for _, u := range m.byID {
		if f(u) {
			return u, nil
		}
	}
	return user.User{}, domain.ErrNotFound
}
func (m *memUsers) Create(_ context.Context, name, email string) (user.User, error) {
	if m.err != nil {
		return user.User{}, m.err
	}
	u := user.User{ID: len(m.byID) + 100, Username: name, Email: email}
	m.byID[u.ID] = u
	return u, nil
}
func (m *memUsers) Ensure(ctx context.Context, name, email string) (user.User, error) {
	if m.err != nil {
		return user.User{}, m.err
	}
	return m.Create(ctx, name, email)
}
func (m *memUsers) Delete(_ context.Context, id int) error {
	m.deleted = append(m.deleted, id)
	delete(m.byID, id)
	return nil
}
func (m *memUsers) MarkEmailVerified(_ context.Context, id int) error {
	m.verified = append(m.verified, id)
	return m.err
}
func (m *memUsers) GetByID(_ context.Context, id int) (user.User, error) {
	if u, ok := m.byID[id]; ok {
		return u, nil
	}
	return user.User{}, domain.ErrNotFound
}
func (m *memUsers) GetByUsername(_ context.Context, n string) (user.User, error) {
	return m.find(func(u user.User) bool { return u.Username == n })
}
func (m *memUsers) GetByLogin(_ context.Context, n string) (user.User, error) {
	return m.find(func(u user.User) bool { return u.Username == n || u.Email == n })
}
func (m *memUsers) Restore(_ context.Context, id int) error {
	u := m.byID[id]
	u.DeletedAt = nil
	m.byID[id] = u
	return nil
}
func (m *memUsers) Profile(_ context.Context, n string, _ int) (user.Profile, error) {
	return user.Profile{Username: n}, nil
}
func (m *memUsers) UpdateProfile(_ context.Context, id int, p user.ProfileUpdate) (user.User, error) {
	return user.User{ID: id, DisplayName: p.DisplayName}, nil
}
func (m *memUsers) Follow(context.Context, int, int) (bool, error) { return m.followed, m.err }
func (m *memUsers) Unfollow(context.Context, int, int) error       { return m.err }
func (m *memUsers) Followers(context.Context, int, domain.Paging) (user.SummaryPage, error) {
	return user.SummaryPage{Total: 1}, nil
}
func (m *memUsers) Following(context.Context, int, domain.Paging) (user.SummaryPage, error) {
	return user.SummaryPage{Total: 2}, nil
}
func (m *memUsers) List(context.Context, string, domain.Paging) (user.Page, error) {
	if m.listErr != nil {
		return user.Page{}, m.listErr
	}
	return user.Page{Items: []user.User{{ID: 1}, {ID: 2}}}, nil
}
func (m *memUsers) DuePurges(context.Context, time.Time) ([]int, error) { return []int{1, 2}, m.err }
func (m *memUsers) Purge(_ context.Context, id int, _ time.Time) (bool, error) {
	m.purged[id] = true
	if id == 2 {
		return false, errBoom
	}
	return true, nil
}

type memIdentity struct {
	Identity
	failLogin, failRoles, failChange, failLogoutAll, failAuth bool
	setPw                                                     int
	ensured                                                   bool
}

func (i *memIdentity) CreateAccount(context.Context, int, string, string) error {
	if i.failLogin {
		return errBoom
	}
	return nil
}
func (i *memIdentity) EnsureSuperAdmin(context.Context, int, string, string) error {
	i.ensured = true
	return nil
}
func (i *memIdentity) Login(context.Context, string, string, Meta) (Credentials, error) {
	if i.failLogin {
		return Credentials{}, ErrInvalidCredentials
	}
	return Credentials{Token: "T"}, nil
}
func (i *memIdentity) Refresh(context.Context, string) (Credentials, error) {
	return Credentials{Token: "T2"}, nil
}
func (i *memIdentity) Logout(context.Context, string, Meta) error { return nil }
func (i *memIdentity) LogoutAll(context.Context, int) error {
	if i.failLogoutAll {
		return errBoom
	}
	return nil
}
func (i *memIdentity) Authenticate(_ context.Context, tok string) (int, error) {
	if i.failAuth || tok == "bad" {
		return 0, errBoom
	}
	return 1, nil
}
func (i *memIdentity) VerifyPassword(context.Context, string, string) error { return nil }
func (i *memIdentity) ChangePassword(context.Context, int, string, string) error {
	if i.failChange {
		return errBoom
	}
	return nil
}
func (i *memIdentity) SetPassword(context.Context, int, string) error { i.setPw++; return nil }
func (i *memIdentity) Roles(context.Context, int) ([]string, error) {
	if i.failRoles {
		return nil, errBoom
	}
	return []string{"user"}, nil
}

type memTokens struct {
	issueErr, consumeErr error
}

func (t memTokens) Issue(context.Context, int, time.Duration) (string, error) {
	return "tok", t.issueErr
}
func (t memTokens) Consume(context.Context, string) (int, error) { return 1, t.consumeErr }

func accts(t *testing.T, users *memUsers, id *memIdentity, tok memTokens, perms ...string) (*Accounts, *world) {
	t.Helper()
	w := newWorld(1, perms...)
	w.posts.posts[1] = post.Post{ID: 1}
	a := NewAccounts(users, id, tok, tok, w.blog, "http://site")
	a.now = w.blog.now
	return a, w
}

func TestAccountsRegisterAndVerify(t *testing.T) {
	users := newMemUsers()
	id := &memIdentity{}
	a, w := accts(t, users, id, memTokens{})
	s, err := a.Register(ctx, "newbie", "newbie@gmail.com", "S3cure-pass-77!", Meta{})
	if err != nil || s.Token != "T" || len(w.jobs.mail) != 1 {
		t.Fatalf("register: %+v %v mail=%d", s, err, len(w.jobs.mail))
	}
	if _, err := a.Register(ctx, "x", "bad", "pw", Meta{}); err == nil {
		t.Error("invalid reg accepted")
	}
	users.err = errBoom
	if _, err := a.Register(ctx, "newbie2", "newbie2@gmail.com", "S3cure-pass-77!", Meta{}); !errors.Is(err, errBoom) {
		t.Errorf("create err: %v", err)
	}
	users.err = nil
	id.failLogin = true // CreateAccount fails -> rollback
	if _, err := a.Register(ctx, "newbie3", "newbie3@gmail.com", "S3cure-pass-77!", Meta{}); !errors.Is(err, errBoom) || len(users.deleted) != 1 {
		t.Errorf("rollback: %v %v", err, users.deleted)
	}
	// verification email failure is non-fatal
	id.failLogin = false
	a2, _ := accts(t, users, id, memTokens{issueErr: errBoom})
	if _, err := a2.Register(ctx, "newbie4", "newbie4@gmail.com", "S3cure-pass-77!", Meta{}); err != nil {
		t.Errorf("verification failure fatal: %v", err)
	}
	if err := a.ResendVerification(ctx, user.User{EmailVerified: true}); err == nil {
		t.Error("resend for verified")
	}
	if err := a.ResendVerification(ctx, user.User{ID: 1, Email: "a@b.c"}); err != nil {
		t.Error(err)
	}
	if err := a.VerifyEmail(ctx, "tok"); err != nil {
		t.Error(err)
	}
	bad, _ := accts(t, users, id, memTokens{consumeErr: errBoom})
	if err := bad.VerifyEmail(ctx, "tok"); err == nil {
		t.Error("bad token")
	}
	users.err = errBoom
	if err := a.VerifyEmail(ctx, "tok"); !errors.Is(err, errBoom) {
		t.Error("mark err")
	}
}

func TestAccountsLoginRefreshAuth(t *testing.T) {
	del := time.Now()
	users := newMemUsers(
		user.User{ID: 1, Username: "ali", Email: "ali@x.io"},
		user.User{ID: 2, Username: "gone", Email: "gone@x.io", DeletedAt: &del},
		user.User{ID: 3, Username: "noemail"},
		user.User{ID: 4, Username: "gone4", Email: "g4@x.io", DeletedAt: &del},
	)
	id := &memIdentity{}
	a, w := accts(t, users, id, memTokens{})
	if s, err := a.Login(ctx, "ali", "pw", Meta{}); err != nil || s.Token != "T" || len(s.User.Roles) != 1 {
		t.Fatalf("login %+v %v", s, err)
	}
	if _, err := a.Login(ctx, "nobody", "pw", Meta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Error(err)
	}
	if _, err := a.Login(ctx, "noemail", "pw", Meta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Error(err)
	}
	if s, err := a.Login(ctx, "gone", "pw", Meta{}); err != nil || s.User.DeletedAt != nil || users.byID[2].DeletedAt != nil {
		t.Errorf("restore: %+v %v", s, err)
	}
	id.failRoles = true
	if _, err := a.Login(ctx, "ali", "pw", Meta{}); err == nil {
		t.Error("roles err")
	}
	id.failRoles = false
	id.failLogin = true
	if _, err := a.Login(ctx, "ali", "pw", Meta{}); err == nil {
		t.Error("login err")
	}
	id.failLogin = false

	if s, err := a.Refresh(ctx, "ok"); err != nil || s.Token != "T2" {
		t.Errorf("refresh %+v %v", s, err)
	}
	if _, err := a.Refresh(ctx, "bad"); err == nil {
		t.Error("refresh bad")
	}
	if err := a.Logout(ctx, "t", Meta{}); err != nil {
		t.Error(err)
	}
	if err := a.LogoutAll(ctx, ali); err != nil {
		t.Error(err)
	}
	if _, err := a.Authenticate(ctx, "bad"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Error(err)
	}
	if u, err := a.Authenticate(ctx, "ok"); err != nil || u.ID != 1 {
		t.Error(u, err)
	}
	if _, err := a.User(ctx, 99); !errors.Is(err, domain.ErrUnauthorized) {
		t.Error(err)
	}
	if _, err := a.User(ctx, 4); err == nil {
		t.Error("deleted user allowed")
	}
	_ = w
}

func TestAccountsPasswords(t *testing.T) {
	users := newMemUsers(user.User{ID: 1, Username: "ali", Email: "ali@x.io"})
	id := &memIdentity{}
	a, w := accts(t, users, id, memTokens{})
	good := "S3cure-pass-77!"
	if _, err := a.ChangePassword(ctx, ali, "old", "short", Meta{}); err == nil {
		t.Error("weak")
	}
	if s, err := a.ChangePassword(ctx, ali, "old", good, Meta{}); err != nil || s.Token == "" {
		t.Error(s, err)
	}
	id.failChange = true
	if _, err := a.ChangePassword(ctx, ali, "old", good, Meta{}); err == nil {
		t.Error("change err")
	}
	id.failChange, id.failLogoutAll = false, true
	if _, err := a.ChangePassword(ctx, ali, "old", good, Meta{}); err == nil {
		t.Error("logoutall err")
	}
	id.failLogoutAll = false

	if err := a.ForgotPassword(ctx, "bad"); err == nil {
		t.Error("bad email")
	}
	if err := a.ForgotPassword(ctx, "ghost@gmail.com"); err != nil {
		t.Error(err)
	}
	if err := a.ForgotPassword(ctx, "ali@x.io"); err != nil || len(w.jobs.mail) != 1 {
		t.Error(err, w.jobs.mail)
	}
	bad, _ := accts(t, users, id, memTokens{issueErr: errBoom})
	if err := bad.ForgotPassword(ctx, "ali@x.io"); err == nil {
		t.Error("issue err")
	}
	del := time.Now()
	users.byID[1] = user.User{ID: 1, Email: "ali@x.io", DeletedAt: &del}
	if err := a.ForgotPassword(ctx, "ali@x.io"); err != nil {
		t.Error(err)
	}

	if err := a.ResetPassword(ctx, "t", "short"); err == nil {
		t.Error("weak")
	}
	if err := a.ResetPassword(ctx, "t", good); err != nil || id.setPw != 1 {
		t.Error(err)
	}
	cb, _ := accts(t, users, id, memTokens{consumeErr: errBoom})
	if err := cb.ResetPassword(ctx, "t", good); err == nil {
		t.Error("consume err")
	}
}

func TestAccountsPurgeAndAdmin(t *testing.T) {
	users := newMemUsers()
	id := &memIdentity{}
	a, w := accts(t, users, id, memTokens{})
	if err := a.Purge(ctx, 1); err != nil || len(w.audit.actions) != 1 {
		t.Error(err)
	}
	if err := a.PurgeDue(ctx); err == nil || !users.purged[1] || !users.purged[2] {
		t.Errorf("purgedue %v", err)
	}
	users.err = errBoom
	if err := a.PurgeDue(ctx); err == nil {
		t.Error("due err")
	}
	users.err = nil
	if err := a.EnsureAdmin(ctx, "root", "root@gmail.com", "S3cure-pass-77!"); err != nil || !id.ensured {
		t.Error(err)
	}
	if err := a.EnsureAdmin(ctx, "x", "bad", "pw"); err == nil {
		t.Error("bad admin")
	}
	users.err = errBoom
	if err := a.EnsureAdmin(ctx, "root", "root@gmail.com", "S3cure-pass-77!"); err == nil {
		t.Error("ensure err")
	}
}

func TestAccountsProfilesAndFollows(t *testing.T) {
	del := time.Now()
	users := newMemUsers(user.User{ID: 1, Username: "ali"}, user.User{ID: 2, Username: "vali"}, user.User{ID: 3, Username: "gone", DeletedAt: &del})
	id := &memIdentity{}
	a, _ := accts(t, users, id, memTokens{}, "follow.create", "user.read")
	w := newWorld(1, "follow.create", "user.read")
	w.blog.Social = &fakeSocial{}
	_ = w
	if p, err := a.Profile(ctx, "ali", 0); err != nil || p.Username != "ali" {
		t.Error(p, err)
	}
	if _, err := a.UpdateProfile(ctx, user.User{ID: 1, Roles: []string{"r"}}, user.ProfileUpdate{Bio: string(make([]byte, 400))}); err == nil {
		t.Error("long bio")
	}
	if u, err := a.UpdateProfile(ctx, user.User{ID: 1, Roles: []string{"r"}}, user.ProfileUpdate{DisplayName: "A"}); err != nil || len(u.Roles) != 1 {
		t.Error(u, err)
	}
	w2 := newWorld(1)
	w2.posts.posts[1] = post.Post{}
	if _, err := a.UserPosts(ctx, "gone", domain.Paging{}); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	if _, err := a.UserPosts(ctx, "nobody", domain.Paging{}); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	for _, f := range []func(context.Context, string, domain.Paging) (user.SummaryPage, error){a.Followers, a.Following} {
		if p, err := f(ctx, "ali", domain.Paging{}); err != nil || p.Total == 0 {
			t.Error(p, err)
		}
		if _, err := f(ctx, "gone", domain.Paging{}); !errors.Is(err, domain.ErrNotFound) {
			t.Error(err)
		}
	}
	if err := a.Follow(ctx, ali, "gone"); !errors.Is(err, domain.ErrNotFound) {
		t.Error(err)
	}
	users.followed = true
	if err := a.Follow(ctx, ali, "vali"); err != nil {
		t.Error(err)
	}
	if err := a.Follow(ctx, ali, "ali"); err == nil {
		t.Error("self follow")
	}
	if err := a.Unfollow(ctx, ali, "nobody"); err == nil {
		t.Error("unfollow missing")
	}
	if err := a.Unfollow(ctx, ali, "vali"); err != nil {
		t.Error(err)
	}
	if pg, err := a.ListUsers(ctx, "", domain.Paging{}); err != nil || pg.Items[0].Roles == nil {
		t.Error(pg, err)
	}
	users.listErr = errBoom
	if _, err := a.ListUsers(ctx, "", domain.Paging{}); err == nil {
		t.Error("list err")
	}
	users.listErr = nil
	id.failRoles = true
	if _, err := a.ListUsers(ctx, "", domain.Paging{}); err == nil {
		t.Error("roles err")
	}
	noPerm, _ := accts(t, users, id, memTokens{})
	if _, err := noPerm.ListUsers(ctx, "", domain.Paging{}); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if err := noPerm.Follow(ctx, ali, "vali"); !errors.Is(err, domain.ErrForbidden) {
		t.Error(err)
	}
	if got := notFoundOr(errBoom); got != errBoom {
		t.Error(got)
	}
}
