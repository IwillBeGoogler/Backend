package application

import (
	"context"
	"errors"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

type c2Social struct {
	uid       int
	lookupErr error
	linkErr   error
	linked    int
}

func (s *c2Social) UserBySubject(context.Context, string, string) (int, error) {
	return s.uid, s.lookupErr
}
func (s *c2Social) Link(context.Context, string, string, int) error {
	s.linked++
	return s.linkErr
}

// c2Users scripts the user repository calls SSO makes.
type c2Users struct {
	*memUsers
	loginU     user.User
	loginErr   error
	createErrs []error // consumed in order, then success
	markErr    error
	creates    int
}

func (u *c2Users) GetByLogin(context.Context, string) (user.User, error) {
	return u.loginU, u.loginErr
}
func (u *c2Users) Create(ctx context.Context, n, e string) (user.User, error) {
	u.creates++
	if len(u.createErrs) > 0 {
		err := u.createErrs[0]
		u.createErrs = u.createErrs[1:]
		if err != nil {
			return user.User{}, err
		}
	}
	return u.memUsers.Create(ctx, n, e)
}
func (u *c2Users) MarkEmailVerified(context.Context, int) error { return u.markErr }

type c2SSOIdent struct {
	*c2Ident
	setPwErr, createErr error
}

func (i *c2SSOIdent) SetPassword(context.Context, int, string) error { return i.setPwErr }
func (i *c2SSOIdent) CreateAccount(context.Context, int, string, string) error {
	return i.createErr
}

func ssoErrCode(err error) string {
	var se *SSOError
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

func TestC2LoginExternal(t *testing.T) {
	verified := ExternalIdentity{Subject: "sub", Email: "New.User@example.com", EmailVerified: true, Name: "New User"}
	existing := user.User{ID: 7, Username: "old", Email: "new.user@example.com", EmailVerified: true}
	cases := []struct {
		name  string
		id    ExternalIdentity
		setup func(s *c2Social, u *c2Users, i *c2SSOIdent)
		want  string // ok, or SSO code, or "err"
	}{
		{"no subject", ExternalIdentity{}, nil, "failed"},
		{"linked", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.uid = 1
		}, "ok"},
		{"lookup err", verified, func(s *c2Social, _ *c2Users, _ *c2SSOIdent) { s.lookupErr = errC2 }, "err"},
		{"link user gone then create", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.uid = 999
			u.loginErr = domain.ErrNotFound
		}, "ok"},
		{"existing verified", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr = domain.ErrNotFound
			u.loginU = existing
		}, "ok"},
		{"link err", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr, s.linkErr = domain.ErrNotFound, errC2
			u.loginU = existing
		}, "err"},
		{"no email", ExternalIdentity{Subject: "s"}, func(s *c2Social, _ *c2Users, _ *c2SSOIdent) {
			s.lookupErr = domain.ErrNotFound
		}, "failed"},
		{"unverified at provider", ExternalIdentity{Subject: "s", Email: "a@example.com"}, func(s *c2Social, _ *c2Users, _ *c2SSOIdent) {
			s.lookupErr = domain.ErrNotFound
		}, "unverified"},
		{"squatter takeover", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr = domain.ErrNotFound
			u.loginU = user.User{ID: 7, Email: "new.user@example.com"}
		}, "ok"},
		{"takeover setpw err", verified, func(s *c2Social, u *c2Users, i *c2SSOIdent) {
			s.lookupErr = domain.ErrNotFound
			u.loginU = user.User{ID: 7}
			i.setPwErr = errC2
		}, "err"},
		{"takeover mark err", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr = domain.ErrNotFound
			u.loginU = user.User{ID: 7}
			u.markErr = errC2
		}, "err"},
		{"login lookup err", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr = domain.ErrNotFound
			u.loginErr = errC2
		}, "err"},
		{"provision", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr, u.loginErr = domain.ErrNotFound, domain.ErrNotFound
		}, "ok"},
		{"provision bad email", ExternalIdentity{Subject: "s", Email: "a@@b", EmailVerified: true}, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr, u.loginErr = domain.ErrNotFound, domain.ErrNotFound
		}, "failed"},
		{"provision retry on conflict", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr, u.loginErr = domain.ErrNotFound, domain.ErrNotFound
			u.createErrs = []error{domain.ErrConflict, domain.ErrConflict}
		}, "ok"},
		{"provision create err", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr, u.loginErr = domain.ErrNotFound, domain.ErrNotFound
			u.createErrs = []error{errC2}
		}, "err"},
		{"provision account err rolls back", verified, func(s *c2Social, u *c2Users, i *c2SSOIdent) {
			s.lookupErr, u.loginErr = domain.ErrNotFound, domain.ErrNotFound
			i.createErr = errC2
		}, "err"},
		{"provision mark err", verified, func(s *c2Social, u *c2Users, _ *c2SSOIdent) {
			s.lookupErr, u.loginErr = domain.ErrNotFound, domain.ErrNotFound
			u.markErr = errC2
		}, "err"},
		{"session refused", verified, func(s *c2Social, u *c2Users, i *c2SSOIdent) {
			s.uid = 1
			i.startErr = errC2
		}, "failed"},
		{"roles err", verified, func(s *c2Social, _ *c2Users, i *c2SSOIdent) {
			s.uid = 1
			i.failRoles = true
		}, "err"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newC2()
			soc := &c2Social{}
			us := &c2Users{memUsers: e.us}
			ident := &c2SSOIdent{c2Ident: e.id}
			if c.setup != nil {
				c.setup(soc, us, ident)
			}
			e.a.Social, e.a.users, e.a.identity = soc, us, ident
			s, err := e.a.LoginExternal(ctx, "google", c.id, Meta{IP: "1.1.1.1"})
			switch c.want {
			case "ok":
				if err != nil || s.Token != "S" {
					t.Fatalf("%+v %v", s, err)
				}
			case "err":
				if err == nil || ssoErrCode(err) != "" {
					t.Fatalf("err = %v", err)
				}
			default:
				if ssoErrCode(err) != c.want {
					t.Fatalf("err = %v, want code %s", err, c.want)
				}
			}
		})
	}
}

func TestC2ProvisionConflictRaceAndExhaustion(t *testing.T) {
	e := newC2()
	us := &c2Users{memUsers: e.us, loginU: user.User{ID: 9}}
	e.a.users = us
	us.createErrs = []error{domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict}
	// retries exhausted: falls back to the account that won the race
	u, err := e.a.provisionExternal(ctx, "race@example.com", "Racer")
	if err != nil || u.ID != 9 {
		t.Fatalf("%+v %v", u, err)
	}
	// exhausted and lookup fails too
	us.createErrs = []error{domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict}
	us.loginErr = errC2
	if _, err := e.a.provisionExternal(ctx, "race@example.com", "Racer"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
}

func TestC2SSOHelpers(t *testing.T) {
	e := newC2()
	e.a.RecordSSOFailure(ctx, "google", "denied", Meta{IP: "1.1.1.1"})
	if len(e.w.audit.actions) != 1 {
		t.Fatal("no audit")
	}
	if s, err := randomSecret(); err != nil || len(s) != 64 {
		t.Fatalf("%q %v", s, err)
	}
	for _, c := range []struct{ name, email, want string }{
		{"Ab", "x.y@z.io", "x_y"},
		{"", "a@z.io", "auuu"[:3]},
		{"Hello World", "q@z.io", "Hello_World"},
		{"!!", "b@z.io", "buu"},
	} {
		if got := externalUsername(c.name, c.email); got != c.want {
			t.Errorf("externalUsername(%q,%q) = %q, want %q", c.name, c.email, got, c.want)
		}
	}
	long := externalUsername("abcdefghijklmnopqrstuvwxyzabcdefghij", "a@b.io")
	if len(long) != 28 {
		t.Errorf("len = %d", len(long))
	}
	if n := randInt(10); n < 0 || n >= 10 {
		t.Errorf("randInt = %d", n)
	}
}
