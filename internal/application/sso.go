package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
)

// SSOError is a sign-in refusal the browser can show; Code is failed or
// unverified.
type SSOError struct{ Code, Msg string }

func (e *SSOError) Error() string { return e.Msg }

// SocialLinks map a provider's stable subject id to a user.
type SocialLinks interface {
	// UserBySubject returns domain.ErrNotFound for an unknown subject.
	UserBySubject(ctx context.Context, provider, subject string) (int, error)
	// Link is idempotent.
	Link(ctx context.Context, provider, subject string, userID int) error
}

// ExternalIdentity is what a provider vouches for after authentication.
type ExternalIdentity struct {
	// Subject is the provider's stable user id.
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// LoginExternal maps a provider identity to a local account: by the stored
// provider+subject link first (so an email change at the provider does not
// matter), else by verified email (linking it), else it creates the account.
// Then it opens a normal Guard session. The provider counts as the first
// factor, so local two-factor is not asked.
func (a *Accounts) LoginExternal(ctx context.Context, provider string, id ExternalIdentity, m Meta) (Session, error) {
	ctx, span := tracer.Start(ctx, "Accounts.LoginExternal")
	defer span.End()
	if id.Subject == "" {
		return Session{}, &SSOError{"failed", "the identity provider did not return a user id"}
	}
	var u user.User
	linked := false
	uid, err := a.Social.UserBySubject(ctx, provider, id.Subject)
	if err == nil {
		u, err = a.users.GetByID(ctx, uid)
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Session{}, err
	}
	if err != nil { // no link yet: match or create by verified email
		if u, linked, err = a.resolveByEmail(ctx, provider, id); err != nil {
			return Session{}, err
		}
		if err := a.Social.Link(ctx, provider, id.Subject, u.ID); err != nil {
			return Session{}, err
		}
	}
	c, err := a.identity.StartSession(ctx, u.ID, m)
	if err != nil {
		return Session{}, &SSOError{"failed", "the account cannot sign in"}
	}
	if u.Roles, err = a.identity.Roles(ctx, u.ID); err != nil {
		return Session{}, err
	}
	a.blog.Audit.Record(ctx, u.ID, "auth.login_sso", "user:"+itoa(u.ID), map[string]any{"provider": provider, "ip": m.IP, "linked": linked})
	return a.finishLogin(ctx, u, Session{Token: c.Token, ExpiresAt: c.ExpiresAt, User: u})
}

func (a *Accounts) resolveByEmail(ctx context.Context, provider string, id ExternalIdentity) (user.User, bool, error) {
	email := strings.ToLower(strings.TrimSpace(id.Email))
	if email == "" || !strings.Contains(email, "@") {
		return user.User{}, false, &SSOError{"failed", "the identity provider did not return an email address"}
	}
	// An unverified address may belong to someone else: never map it.
	if !id.EmailVerified {
		return user.User{}, false, &SSOError{"unverified", "the email address is not verified at the identity provider"}
	}
	u, err := a.users.GetByLogin(ctx, email)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		if u, err = a.provisionExternal(ctx, email, id.Name); err != nil {
			return u, false, err
		}
		a.blog.Audit.Record(ctx, u.ID, "auth.sso_provision", "user:"+itoa(u.ID), map[string]any{"provider": provider})
		return u, false, nil
	case err != nil:
		return u, false, err
	case !u.EmailVerified:
		// Anyone could have registered this address before its owner:
		// verify it and replace the password the squatter may know.
		if err := a.takeOver(ctx, u.ID); err != nil {
			return u, false, err
		}
		u.EmailVerified = true
	}
	return u, true, nil
}

// RecordSSOFailure audits a refused or failed external sign-in.
func (a *Accounts) RecordSSOFailure(ctx context.Context, provider, reason string, m Meta) {
	a.blog.Audit.Record(ctx, 0, "auth.sso_failed", "sso:"+provider, map[string]any{"reason": reason, "ip": m.IP})
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (a *Accounts) takeOver(ctx context.Context, id int) error {
	pw, err := randomSecret()
	if err != nil {
		return err
	}
	if err := a.identity.SetPassword(ctx, id, pw); err != nil { // also ends sessions and API keys
		return err
	}
	return a.users.MarkEmailVerified(ctx, id)
}

// provisionExternal creates a user with an unguessable password (the
// owner can set one later through "forgot password").
func (a *Accounts) provisionExternal(ctx context.Context, email, name string) (user.User, error) {
	addr, err := user.ValidateEmail(email)
	if err != nil {
		return user.User{}, &SSOError{"failed", "the email address is not acceptable"}
	}
	pw, err := randomSecret()
	if err != nil {
		return user.User{}, err
	}
	base := externalUsername(name, addr)
	var u user.User
	for i := 0; ; i++ {
		cand := base
		if i > 0 {
			sfx := fmt.Sprintf("_%04d", randInt(10000))
			cand = base[:min(len(base), 32-len(sfx))] + sfx
		}
		u, err = a.users.Create(ctx, cand, addr)
		if err == nil {
			break
		}
		if !errors.Is(err, domain.ErrConflict) || i >= 6 {
			if errors.Is(err, domain.ErrConflict) { // lost a race on the email
				if again, gerr := a.users.GetByLogin(ctx, addr); gerr == nil {
					return again, nil
				}
			}
			return user.User{}, err
		}
	}
	if err := a.identity.CreateAccount(ctx, u.ID, addr, pw); err != nil {
		if derr := a.users.Delete(ctx, u.ID); derr != nil {
			zap.L().Error("rollback user", zap.Any("ctx", ctx), zap.Int("id", u.ID), zap.Error(derr))
		}
		return user.User{}, err
	}
	if err := a.users.MarkEmailVerified(ctx, u.ID); err != nil {
		return user.User{}, err
	}
	u.EmailVerified = true
	return u, nil
}

func randInt(n int) int {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return int((uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % uint32(n))
}

// externalUsername derives a valid username (3-32 of letters, digits, _).
func externalUsername(name, email string) string {
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
				b.WriteRune(r)
			case r == ' ' || r == '.' || r == '-':
				b.WriteByte('_')
			}
		}
		return strings.Trim(b.String(), "_")
	}
	s := clean(name)
	if len(s) < 3 {
		s = clean(email[:strings.Index(email, "@")])
	}
	for len(s) < 3 {
		s += "u"
	}
	return s[:min(len(s), 28)]
}
