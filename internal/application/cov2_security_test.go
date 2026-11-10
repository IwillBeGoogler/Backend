package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/clienterror"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/feedback"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/mfa"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/sanction"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

var errC2 = errors.New("c2 boom")

// c2MFA is a scriptable two-factor repository.
type c2MFA struct {
	mfa.Repository
	state                                                      mfa.State
	getErr, pendErr, enableErr, disableErr, stepErr, backupErr error
	replaceErr, leftErr                                        error
	stepFresh, backupOK                                        bool
	left                                                       int
	enabled, disabled, replaced                                bool
}

func (m *c2MFA) Get(context.Context, int) (mfa.State, error)    { return m.state, m.getErr }
func (m *c2MFA) SavePending(context.Context, int, string) error { return m.pendErr }
func (m *c2MFA) Enable(context.Context, int, int64, []string) error {
	m.enabled = m.enableErr == nil
	return m.enableErr
}
func (m *c2MFA) Disable(context.Context, int) error { m.disabled = true; return m.disableErr }
func (m *c2MFA) UseStep(context.Context, int, int64) (bool, error) {
	return m.stepFresh, m.stepErr
}
func (m *c2MFA) UseBackupCode(context.Context, int, string) (bool, error) {
	return m.backupOK, m.backupErr
}
func (m *c2MFA) ReplaceBackupCodes(context.Context, int, []string) error {
	m.replaced = true
	return m.replaceErr
}
func (m *c2MFA) BackupCodesLeft(context.Context, int) (int, error) { return m.left, m.leftErr }

type c2Challenges struct {
	issueErr, attemptErr, consumeErr error
}

func (c c2Challenges) Issue(context.Context, int, time.Duration) (string, error) {
	return "chal", c.issueErr
}
func (c c2Challenges) Attempt(context.Context, string) (int, error) { return 1, c.attemptErr }
func (c c2Challenges) Consume(context.Context, string) error        { return c.consumeErr }

type c2Box struct{ sealErr, openErr error }

func (b c2Box) Seal(p string) (string, error) { return p, b.sealErr }
func (b c2Box) Open(s string) (string, error) { return s, b.openErr }

type c2Sanctions struct {
	sanction.Repository
	getErr, saveErr, delErr, expErr, listErr error
	expired                                  []int
	deleted                                  []int
}

func (s *c2Sanctions) Save(_ context.Context, x sanction.Sanction) (sanction.Sanction, error) {
	return x, s.saveErr
}
func (s *c2Sanctions) Get(_ context.Context, id int) (sanction.Sanction, error) {
	return sanction.Sanction{UserID: id}, s.getErr
}
func (s *c2Sanctions) Delete(_ context.Context, id int) error {
	s.deleted = append(s.deleted, id)
	return s.delErr
}
func (s *c2Sanctions) Expired(context.Context, time.Time) ([]int, error) { return s.expired, s.expErr }
func (s *c2Sanctions) List(context.Context, domain.Paging) ([]sanction.Sanction, int, error) {
	return []sanction.Sanction{{UserID: 1}}, 1, s.listErr
}

type c2Ident struct {
	*memIdentity
	verifyErr, startErr, sessErr, revokeErr, blockErr error
	sessions                                          []SessionInfo
	blocked                                           []bool
}

func (i *c2Ident) VerifyPassword(context.Context, string, string) error { return i.verifyErr }
func (i *c2Ident) StartSession(context.Context, int, Meta) (Credentials, error) {
	return Credentials{Token: "S"}, i.startErr
}
func (i *c2Ident) Sessions(context.Context, int) ([]SessionInfo, error) {
	return i.sessions, i.sessErr
}
func (i *c2Ident) RevokeSession(context.Context, int, string) error  { return i.revokeErr }
func (i *c2Ident) SessionID(context.Context, string) (string, error) { return "b", nil }
func (i *c2Ident) SetBlocked(_ context.Context, _, _ int, b bool) error {
	i.blocked = append(i.blocked, b)
	return i.blockErr
}

type c2Realtime struct{ n int }

func (r *c2Realtime) AccountBlocked(context.Context, int) { r.n++ }

type c2Env struct {
	a  *Accounts
	w  *world
	m  *c2MFA
	id *c2Ident
	sa *c2Sanctions
	rt *c2Realtime
	us *memUsers
}

func newC2(perms ...string) *c2Env {
	us := newMemUsers(user.User{ID: 1, Username: "ali", Email: "ali@x.io"}, user.User{ID: 2, Username: "vali", Email: "vali@x.io"})
	id := &c2Ident{memIdentity: &memIdentity{}}
	w := newWorld(1, perms...)
	a := NewAccounts(us, id, memTokens{}, memTokens{}, w.blog, "http://site")
	a.now = w.blog.now
	e := &c2Env{a: a, w: w, m: &c2MFA{}, id: id, sa: &c2Sanctions{}, rt: &c2Realtime{}, us: us}
	a.MFA, a.Challenges, a.Secrets, a.Sanctions, a.Realtime = e.m, c2Challenges{}, c2Box{}, e.sa, e.rt
	return e
}

func (e *c2Env) enable(secret string) {
	now := e.w.blog.now()
	e.m.state = mfa.State{Secret: secret, EnabledAt: &now}
}

func TestC2MFAEnabledAndLogin(t *testing.T) {
	e := newC2()
	e.a.MFA = nil
	if on, err := e.a.mfaEnabled(ctx, 1); on || err != nil {
		t.Fatal("nil repo must be off")
	}
	e.a.MFA = e.m
	e.m.getErr = domain.ErrNotFound
	if on, err := e.a.mfaEnabled(ctx, 1); on || err != nil {
		t.Fatal("not found must be off")
	}
	e.m.getErr = errC2
	if _, err := e.a.Login(ctx, "ali", "pw", Meta{}); !errors.Is(err, errC2) {
		t.Fatalf("login err = %v", err)
	}
	e.m.getErr = nil
	e.enable("S")
	s, err := e.a.Login(ctx, "ali", "pw", Meta{})
	if err != nil || !s.MFARequired || s.MFAToken != "chal" {
		t.Fatalf("challenge = %+v %v", s, err)
	}
	e.id.verifyErr = errC2
	if _, err := e.a.mfaChallenge(ctx, e.us.byID[1], "pw"); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
	e.id.verifyErr = nil
	e.a.Challenges = c2Challenges{issueErr: errC2}
	if _, err := e.a.mfaChallenge(ctx, e.us.byID[1], "pw"); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
}

func TestC2LoginMFA(t *testing.T) {
	secret := mfa.NewSecret()
	code, _ := mfa.Code(secret, mfa.Step(newC2().w.blog.now()))
	cases := []struct {
		name  string
		setup func(e *c2Env)
		code  string
		ok    bool
	}{
		{"ok totp", func(e *c2Env) { e.enable(secret); e.m.stepFresh = true }, code, true},
		{"ok deleted account restored", func(e *c2Env) {
			e.enable(secret)
			e.m.stepFresh = true
			now := e.w.blog.now()
			e.us.byID[1] = user.User{ID: 1, Username: "ali", Email: "ali@x.io", DeletedAt: &now}
		}, code, true},
		{"no challenges", func(e *c2Env) { e.a.Challenges = nil }, code, false},
		{"attempt err", func(e *c2Env) { e.a.Challenges = c2Challenges{attemptErr: errC2} }, code, false},
		{"not enabled", func(e *c2Env) {}, code, false},
		{"consume err", func(e *c2Env) {
			e.enable(secret)
			e.m.stepFresh = true
			e.a.Challenges = c2Challenges{consumeErr: errC2}
		}, code, false},
		{"user missing", func(e *c2Env) { e.enable(secret); e.m.stepFresh = true; delete(e.us.byID, 1) }, code, false},
		{"start err", func(e *c2Env) { e.enable(secret); e.m.stepFresh = true; e.id.startErr = errC2 }, code, false},
		{"roles err", func(e *c2Env) { e.enable(secret); e.m.stepFresh = true; e.id.failRoles = true }, code, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newC2()
			c.setup(e)
			s, err := e.a.LoginMFA(ctx, "chal", c.code, Meta{IP: "1.1.1.1"})
			if (err == nil) != c.ok {
				t.Fatalf("err = %v", err)
			}
			if c.ok && s.Token != "S" {
				t.Fatalf("session = %+v", s)
			}
		})
	}
}

func TestC2CheckSecondFactor(t *testing.T) {
	secret := mfa.NewSecret()
	now := newC2().w.blog.now()
	good, _ := mfa.Code(secret, mfa.Step(now))
	bc := mfa.NewBackupCodes()[0]
	cases := []struct {
		name  string
		setup func(m *c2MFA)
		code  string
		want  error
	}{
		{"not found", func(m *c2MFA) { m.getErr = domain.ErrNotFound }, good, mfa.ErrNotEnabled},
		{"disabled", func(m *c2MFA) {}, good, mfa.ErrNotEnabled},
		{"get err", func(m *c2MFA) { m.getErr = errC2 }, good, errC2},
		{"backup ok", func(m *c2MFA) { m.state.EnabledAt = &now; m.backupOK = true }, bc, nil},
		{"backup bad", func(m *c2MFA) { m.state.EnabledAt = &now }, bc, mfa.ErrBadCode},
		{"backup err", func(m *c2MFA) { m.state.EnabledAt = &now; m.backupErr = errC2 }, bc, errC2},
		{"bad totp", func(m *c2MFA) { m.state = mfa.State{Secret: secret, EnabledAt: &now} }, "000000x", mfa.ErrBadCode},
		{"replayed", func(m *c2MFA) { m.state = mfa.State{Secret: secret, EnabledAt: &now} }, good, mfa.ErrBadCode},
		{"totp ok", func(m *c2MFA) { m.state = mfa.State{Secret: secret, EnabledAt: &now}; m.stepFresh = true }, good, nil},
		{"step err", func(m *c2MFA) { m.state = mfa.State{Secret: secret, EnabledAt: &now}; m.stepErr = errC2 }, good, errC2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newC2()
			c.setup(e.m)
			if err := e.a.checkSecondFactor(ctx, 1, c.code); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	e := newC2()
	e.m.state = mfa.State{Secret: secret, EnabledAt: &now}
	e.a.Secrets = c2Box{openErr: errC2}
	if err := e.a.checkSecondFactor(ctx, 1, good); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
}

func TestC2MFAStatusSetup(t *testing.T) {
	e := newC2()
	now := e.w.blog.now()
	for _, c := range []struct {
		name  string
		setup func()
		on    bool
		err   error
	}{
		{"off", func() {}, false, nil},
		{"not found", func() { e.m.getErr = domain.ErrNotFound }, false, nil},
		{"get err", func() { e.m.getErr = errC2 }, false, errC2},
		{"on", func() { e.m.getErr = nil; e.m.state.EnabledAt = &now; e.m.left = 4 }, true, nil},
		{"left err", func() { e.m.leftErr = errC2 }, true, errC2},
	} {
		c.setup()
		st, err := e.a.MFAStatus(ctx, e.us.byID[1])
		if st.Enabled != c.on || !errors.Is(err, c.err) {
			t.Fatalf("%s: %+v %v", c.name, st, err)
		}
	}
	for _, c := range []struct {
		name string
		u    user.User
		box  c2Box
		pend error
		want error
	}{
		{"email", user.User{ID: 1, Email: "a@b.io"}, c2Box{}, nil, nil},
		{"username", user.User{ID: 1, Username: "ali"}, c2Box{}, nil, nil},
		{"seal", user.User{ID: 1}, c2Box{sealErr: errC2}, nil, errC2},
		{"pending", user.User{ID: 1}, c2Box{}, errC2, errC2},
	} {
		e.a.Secrets, e.m.pendErr = c.box, c.pend
		s, err := e.a.SetupMFA(ctx, c.u)
		if !errors.Is(err, c.want) || (err == nil && s.URI == "") {
			t.Fatalf("%s: %+v %v", c.name, s, err)
		}
	}
}

func TestC2EnableDisableRegenerate(t *testing.T) {
	secret := mfa.NewSecret()
	now := newC2().w.blog.now()
	good, _ := mfa.Code(secret, mfa.Step(now))
	actor := user.User{ID: 1, Username: "ali", Email: "ali@x.io"}
	pending := mfa.State{Secret: secret}
	on := mfa.State{Secret: secret, EnabledAt: &now}
	for _, c := range []struct {
		name  string
		setup func(e *c2Env)
		code  string
		want  error
	}{
		{"ok", func(e *c2Env) { e.m.state = pending }, good, nil},
		{"no setup", func(e *c2Env) { e.m.getErr = domain.ErrNotFound }, good, mfa.ErrNoSetup},
		{"get err", func(e *c2Env) { e.m.getErr = errC2 }, good, errC2},
		{"already", func(e *c2Env) { e.m.state = on }, good, mfa.ErrEnabled},
		{"open err", func(e *c2Env) { e.m.state = pending; e.a.Secrets = c2Box{openErr: errC2} }, good, errC2},
		{"bad code", func(e *c2Env) { e.m.state = pending }, "bad", mfa.ErrBadCode},
		{"enable err", func(e *c2Env) { e.m.state = pending; e.m.enableErr = errC2 }, good, errC2},
	} {
		t.Run("enable "+c.name, func(t *testing.T) {
			e := newC2()
			c.setup(e)
			codes, err := e.a.EnableMFA(ctx, actor, c.code)
			if !errors.Is(err, c.want) || (err == nil && len(codes) == 0) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	for _, c := range []struct {
		name  string
		setup func(e *c2Env)
		want  error
	}{
		{"ok", func(e *c2Env) { e.m.state = on; e.m.stepFresh = true }, nil},
		{"password", func(e *c2Env) { e.id.verifyErr = errC2 }, errC2},
		{"factor", func(e *c2Env) { e.m.state = on }, mfa.ErrBadCode},
		{"disable err", func(e *c2Env) { e.m.state = on; e.m.stepFresh = true; e.m.disableErr = errC2 }, errC2},
	} {
		t.Run("disable "+c.name, func(t *testing.T) {
			e := newC2()
			c.setup(e)
			if err := e.a.DisableMFA(ctx, actor, "pw", good); !errors.Is(err, c.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	for _, c := range []struct {
		name  string
		setup func(e *c2Env)
		want  error
	}{
		{"ok", func(e *c2Env) { e.m.state = on; e.m.stepFresh = true }, nil},
		{"factor", func(e *c2Env) { e.m.state = on }, mfa.ErrBadCode},
		{"replace err", func(e *c2Env) { e.m.state = on; e.m.stepFresh = true; e.m.replaceErr = errC2 }, errC2},
	} {
		t.Run("regen "+c.name, func(t *testing.T) {
			e := newC2()
			c.setup(e)
			codes, err := e.a.RegenerateBackupCodes(ctx, actor, good)
			if !errors.Is(err, c.want) || (err == nil && len(codes) == 0) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

type c2FailJobs struct{ Jobs }

func (c2FailJobs) SendEmail(context.Context, Email) error { return errC2 }

func TestC2SecurityEmailFailureAndNoEmail(t *testing.T) {
	e := newC2()
	e.a.securityEmail(ctx, user.User{ID: 1}, "s", "t")
	e.w.blog.Jobs = c2FailJobs{}
	e.a.securityEmail(ctx, user.User{ID: 1, Email: "a@b.io"}, "s", "t")
}

func TestC2Sessions(t *testing.T) {
	e := newC2()
	actor := e.us.byID[1]
	t0 := e.w.blog.now()
	e.id.sessions = []SessionInfo{{ID: "a", LastSeenAt: t0}, {ID: "b", LastSeenAt: t0.Add(time.Hour)}}
	l, err := e.a.Sessions(ctx, actor, "tok")
	if err != nil || len(l) != 2 || l[0].ID != "b" || !l[0].Current || l[1].Current {
		t.Fatalf("%+v %v", l, err)
	}
	e.id.sessions = nil
	if l, _ := e.a.Sessions(ctx, actor, "tok"); l == nil || len(l) != 0 {
		t.Fatal("want empty non-nil")
	}
	e.id.sessErr = errC2
	if _, err := e.a.Sessions(ctx, actor, "tok"); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
	if err := e.a.RevokeSession(ctx, actor, "a"); err != nil {
		t.Fatal(err)
	}
	e.id.revokeErr = errC2
	if err := e.a.RevokeSession(ctx, actor, "a"); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
}

func TestC2Sanctions(t *testing.T) {
	future := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	actor := user.User{ID: 1, Username: "ali"}
	for _, c := range []struct {
		name   string
		perms  []string
		who    string
		reason string
		until  *time.Time
		setup  func(e *c2Env)
		want   error
	}{
		{"ban", []string{"user.ban"}, "vali", "spam", nil, nil, nil},
		{"suspend", []string{"user.ban"}, "vali", "spam", &future, nil, nil},
		{"forbidden", nil, "vali", "spam", nil, nil, domain.ErrForbidden},
		{"unknown", []string{"user.ban"}, "ghost", "spam", nil, nil, domain.ErrNotFound},
		{"invalid", []string{"user.ban"}, "vali", "", nil, nil, &domain.ValidationError{}},
		{"block err", []string{"user.ban"}, "vali", "spam", nil, func(e *c2Env) { e.id.blockErr = errC2 }, errC2},
		{"save err", []string{"user.ban"}, "vali", "spam", nil, func(e *c2Env) { e.sa.saveErr = errC2 }, errC2},
		{"no realtime", []string{"user.ban"}, "vali", "spam", nil, func(e *c2Env) { e.a.Realtime = nil }, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newC2(c.perms...)
			if c.setup != nil {
				c.setup(e)
			}
			_, err := e.a.Sanction(ctx, actor, c.who, c.reason, c.until)
			var ve *domain.ValidationError
			if _, isV := any(c.want).(*domain.ValidationError); isV {
				if !errors.As(err, &ve) {
					t.Fatalf("err = %v", err)
				}
			} else if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestC2GetListLiftSanctions(t *testing.T) {
	actor := user.User{ID: 1}
	e := newC2()
	if _, err := e.a.GetSanction(ctx, "vali"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := e.a.ListSanctions(ctx, domain.Paging{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	if err := e.a.LiftSanction(ctx, actor, "vali"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	e = newC2("user.ban")
	if s, err := e.a.GetSanction(ctx, "vali"); err != nil || s.UserID != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := e.a.GetSanction(ctx, "ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	if p, err := e.a.ListSanctions(ctx, domain.Paging{Page: 1, Limit: 5}); err != nil || p.Total != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if err := e.a.LiftSanction(ctx, actor, "vali"); err != nil {
		t.Fatal(err)
	}
	if err := e.a.LiftSanction(ctx, actor, "ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	e.sa.getErr = domain.ErrNotFound
	if err := e.a.LiftSanction(ctx, actor, "vali"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	e.sa.getErr = nil
	e.id.blockErr = errC2
	if err := e.a.lift(ctx, 1, 2); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
	e.id.blockErr = nil
	e.sa.delErr = errC2
	if err := e.a.lift(ctx, 1, 2); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
	e.sa.delErr = domain.ErrNotFound
	if err := e.a.lift(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
}

func TestC2LiftExpired(t *testing.T) {
	e := newC2()
	e.sa.expired = []int{2, 3}
	if err := e.a.LiftExpired(ctx); err != nil || len(e.sa.deleted) != 2 {
		t.Fatalf("%v %v", err, e.sa.deleted)
	}
	e.id.blockErr = errC2
	if err := e.a.LiftExpired(ctx); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
	e.sa.expErr = errC2
	if err := e.a.LiftExpired(ctx); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
	e.a.Sanctions = nil
	if err := e.a.LiftExpired(ctx); err != nil {
		t.Fatal(err)
	}
}

type c2Feedback struct {
	feedback.Repository
	err error
}

func (f c2Feedback) Create(_ context.Context, x feedback.Feedback) (feedback.Feedback, error) {
	return x, f.err
}
func (f c2Feedback) List(context.Context, feedback.Filter) (feedback.Page, error) {
	return feedback.Page{Total: 1}, f.err
}
func (f c2Feedback) SetStatus(_ context.Context, id int, s feedback.Status, _ int) (feedback.Feedback, error) {
	return feedback.Feedback{ID: id, Status: s}, f.err
}

func TestC2Feedback(t *testing.T) {
	w := newWorld(1)
	w.blog.Feedback = c2Feedback{}
	if _, err := w.blog.SubmitFeedback(ctx, 0, feedback.Input{Kind: "nope"}); err == nil {
		t.Fatal("want invalid")
	}
	if f, err := w.blog.SubmitFeedback(ctx, 0, feedback.Input{Kind: feedback.KindBug, Message: "x"}); err != nil || f.Message != "x" {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := w.blog.ListFeedback(ctx, feedback.Filter{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := w.blog.DecideFeedback(ctx, 1, 1, feedback.StatusOpen); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	w = newWorld(1, "report.moderate")
	w.blog.Feedback = c2Feedback{}
	if _, err := w.blog.ListFeedback(ctx, feedback.Filter{Status: "zzz"}); err == nil {
		t.Fatal("bad status")
	}
	if _, err := w.blog.ListFeedback(ctx, feedback.Filter{Kind: "zzz"}); err == nil {
		t.Fatal("bad kind")
	}
	if p, err := w.blog.ListFeedback(ctx, feedback.Filter{}); err != nil || p.Total != 1 {
		t.Fatal(err)
	}
	if _, err := w.blog.DecideFeedback(ctx, 1, 1, "zzz"); err == nil {
		t.Fatal("bad status")
	}
	if _, err := w.blog.DecideFeedback(ctx, 1, 1, feedback.StatusOpen); err != nil || len(w.audit.actions) != 1 {
		t.Fatal(err)
	}
	w.blog.Feedback = c2Feedback{err: errC2}
	if _, err := w.blog.DecideFeedback(ctx, 1, 1, feedback.StatusOpen); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
}

type c2ClientErrors struct {
	clienterror.Repository
	err error
}

func (c c2ClientErrors) Record(context.Context, clienterror.ClientError, int) error { return c.err }
func (c c2ClientErrors) List(context.Context, clienterror.Filter) (clienterror.Page, error) {
	return clienterror.Page{Total: 1}, c.err
}
func (c c2ClientErrors) SetStatus(_ context.Context, id int, s clienterror.Status) (clienterror.ClientError, error) {
	return clienterror.ClientError{ID: id, Status: s}, c.err
}
func (c c2ClientErrors) PurgeBefore(context.Context, time.Time) (int64, error) { return 0, c.err }

func TestC2ClientErrors(t *testing.T) {
	w := newWorld(1)
	w.blog.ClientErrors = c2ClientErrors{}
	if err := w.blog.ReportClientError(ctx, clienterror.Input{}); err == nil {
		t.Fatal("empty message")
	}
	if err := w.blog.ReportClientError(ctx, clienterror.Input{Message: "boom"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.blog.ListClientErrors(ctx, clienterror.Filter{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := w.blog.DecideClientError(ctx, 1, 1, clienterror.StatusOpen); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	w = newWorld(1, "report.moderate")
	w.blog.ClientErrors = c2ClientErrors{}
	if _, err := w.blog.ListClientErrors(ctx, clienterror.Filter{Status: "zzz"}); err == nil {
		t.Fatal("bad status")
	}
	if p, err := w.blog.ListClientErrors(ctx, clienterror.Filter{}); err != nil || p.Total != 1 {
		t.Fatal(err)
	}
	if _, err := w.blog.DecideClientError(ctx, 1, 1, "zzz"); err == nil {
		t.Fatal("bad status")
	}
	if _, err := w.blog.DecideClientError(ctx, 1, 1, clienterror.StatusResolved); err != nil || len(w.audit.actions) != 1 {
		t.Fatal(err)
	}
	if err := w.blog.PurgeClientErrors(ctx); err != nil {
		t.Fatal(err)
	}
	w.blog.ClientErrors = c2ClientErrors{err: errC2}
	if _, err := w.blog.DecideClientError(ctx, 1, 1, clienterror.StatusResolved); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
	if err := w.blog.PurgeClientErrors(ctx); !errors.Is(err, errC2) {
		t.Fatal(err)
	}
}
