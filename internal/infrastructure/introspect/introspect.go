// Package introspect reverse-engineers a live database into the schema
// model (diagram.SchemaIntrospector). It is read only by construction: only
// the fixed catalog SELECTs below ever run (never user SQL), PostgreSQL and
// MySQL sessions are opened read only with timeouts, a SQLite file is
// opened read only from a private temp file. The server connects out on a
// user's behalf, so targets go through netguard (private, loopback and
// link-local addresses are refused unless the administrator allows them;
// the connection is made to the address that was checked). Credentials are
// used for the one connection and scrubbed from error messages.
package introspect

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/netguard"
)

const (
	// Deadline bounds a whole introspection, connect to last row.
	Deadline = 20 * time.Second
	// ConnectTimeout bounds opening the connection.
	ConnectTimeout = 5 * time.Second
	// StatementTimeout bounds each catalog query on the server.
	StatementTimeout = 10 * time.Second
)

// Service introspects databases.
type Service struct {
	// AllowPrivate (SCHEMA_ALLOW_PRIVATE) lets targets be private,
	// loopback or link-local.
	AllowPrivate bool
	// Lookup resolves host names (nil: the system resolver).
	Lookup netguard.Lookup
}

var _ diagram.SchemaIntrospector = (*Service)(nil)

// New builds a service.
func New(allowPrivate bool) *Service { return &Service{AllowPrivate: allowPrivate} }

// CheckTarget refuses a host and port the server must not connect to.
func (s *Service) CheckTarget(ctx context.Context, host string, port int) error {
	if host == "" || strings.ContainsAny(host, " \t\r\n/\\@") {
		return &diagram.TargetBlockedError{Reason: "Enter a host name or IP address"}
	}
	if port < 1 || port > 65535 {
		return &diagram.TargetBlockedError{Reason: "The port must be between 1 and 65535"}
	}
	if s.AllowPrivate {
		return nil
	}
	if netguard.PrivateHost(host) {
		return &diagram.TargetBlockedError{Reason: "Private, loopback and link-local addresses are not allowed (the administrator can set SCHEMA_ALLOW_PRIVATE=true)"}
	}
	if _, err := netguard.Resolve(ctx, host, false, s.Lookup); err != nil {
		if errors.Is(err, netguard.ErrBlocked) {
			return &diagram.TargetBlockedError{Reason: "The host resolves to a private address (the administrator can set SCHEMA_ALLOW_PRIVATE=true)"}
		}
		return &diagram.TargetBlockedError{Reason: "The host name could not be resolved"}
	}
	return nil
}

func (s *Service) dialer() netguard.Dialer {
	return netguard.Dialer{AllowPrivate: s.AllowPrivate, Lookup: s.Lookup, Timeout: ConnectTimeout}
}

// Introspect reads a live PostgreSQL or MySQL database.
func (s *Service) Introspect(ctx context.Context, t diagram.DBTarget) (diagram.Introspected, error) {
	if t.Port == 0 {
		t.Port = map[string]int{diagram.DialectPostgres: 5432, diagram.DialectMySQL: 3306}[t.Dialect]
	}
	if err := s.CheckTarget(ctx, t.Host, t.Port); err != nil {
		return diagram.Introspected{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	var out diagram.Introspected
	var err error
	switch t.Dialect {
	case diagram.DialectPostgres:
		out, err = s.postgres(ctx, t)
	case diagram.DialectMySQL:
		out, err = s.mysql(ctx, t)
	default:
		return out, &diagram.IntrospectFailedError{Msg: "unknown dialect"}
	}
	if err != nil {
		var blocked *diagram.TargetBlockedError
		if errors.As(err, &blocked) {
			return out, err
		}
		if errors.Is(err, netguard.ErrBlocked) {
			return out, &diagram.TargetBlockedError{Reason: "The host resolves to a private address (the administrator can set SCHEMA_ALLOW_PRIVATE=true)"}
		}
		msg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			msg = fmt.Sprintf("Introspection took longer than %ds", int(Deadline.Seconds()))
		}
		return out, &diagram.IntrospectFailedError{Msg: truncate(Redact(msg, t.Password, t.User), 400)}
	}
	return out, nil
}

var (
	urlCreds = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^@\s/]*@`)
	pwAssign = regexp.MustCompile(`(?i)(password\s*[=:]\s*)\S+`)
)

// Redact removes credentials from an error message: a connection URL's
// user info, "password=..." and the given secrets themselves.
func Redact(msg string, secrets ...string) string {
	out := urlCreds.ReplaceAllString(msg, "${1}***@")
	out = pwAssign.ReplaceAllString(out, "${1}***")
	for _, s := range secrets {
		if len(s) >= 3 {
			out = strings.ReplaceAll(out, s, "***")
		}
	}
	return out
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// nonNil keeps empty lists as [] in JSON.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
