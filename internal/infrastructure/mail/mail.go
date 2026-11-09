// Package mail sends email over SMTP: plain (Mailpit in development) or with
// STARTTLS / implicit TLS and AUTH PLAIN (SES, Mailgun, Postmark, Brevo...).
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TLS modes of the SMTP connection.
const (
	TLSNone     = "none"     // plain; upgrades if the server offers STARTTLS (no auth allowed)
	TLSStartTLS = "starttls" // STARTTLS is required
	TLSImplicit = "tls"      // TLS from the first byte (port 465)
)

type SMTP struct {
	Addr string // host:port
	From string
	// Username and Password enable SMTP AUTH (PLAIN). Auth is only sent over
	// an encrypted connection: TLS must be "starttls" or "tls".
	Username, Password string
	TLS                string // TLSNone (default), TLSStartTLS or TLSImplicit
	// TLSConfig overrides the TLS settings (tests); ServerName defaults to the host.
	TLSConfig *tls.Config
}

// Validate reports a configuration that cannot work or would leak the
// password. Call it at start-up.
func (s SMTP) Validate() error {
	switch strings.ToLower(s.TLS) {
	case "", TLSNone:
		if s.Username != "" || s.Password != "" {
			return errors.New("SMTP_USERNAME/SMTP_PASSWORD need SMTP_TLS=starttls or tls (auth is never sent in clear text)")
		}
	case TLSStartTLS, TLSImplicit:
	default:
		return fmt.Errorf("SMTP_TLS %q: want none, starttls or tls", s.TLS)
	}
	return nil
}

func (s SMTP) tlsConfig(host string) *tls.Config {
	if s.TLSConfig != nil {
		c := s.TLSConfig.Clone()
		if c.ServerName == "" {
			c.ServerName = host
		}
		return c
	}
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

// encryptedAuth is PLAIN auth that we only start once the link is
// encrypted (net/smtp cannot tell an implicit-TLS connection from a plain one).
type encryptedAuth struct{ smtp.Auth }

func (a encryptedAuth) Start(si *smtp.ServerInfo) (string, []byte, error) {
	return a.Auth.Start(&smtp.ServerInfo{Name: si.Name, TLS: true, Auth: si.Auth})
}

func (s SMTP) Send(ctx context.Context, m application.Email) (err error) {
	_, span := telemetry.Start(ctx, "smtp.send", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("server.address", s.Addr)))
	defer func() { telemetry.End(span, err) }()
	var h strings.Builder
	fmt.Fprintf(&h, "From: %s\r\nTo: %s\r\nSubject: %s\r\n", header(s.From), header(m.To), mime.QEncoding.Encode("utf-8", header(m.Subject)))
	if m.Unsubscribe != "" {
		fmt.Fprintf(&h, "List-Unsubscribe: <%s>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n", header(m.Unsubscribe))
	}
	msg := h.String() + "Content-Type: text/plain; charset=utf-8\r\n\r\n" + strings.ReplaceAll(m.Text, "\n", "\r\n")
	return s.send(ctx, m.To, []byte(msg))
}

// sendTimeout bounds one whole SMTP exchange so a stalled server cannot pin
// a worker goroutine (smtp.SendMail has no timeout at all).
const sendTimeout = 30 * time.Second

func (s SMTP) send(ctx context.Context, to string, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.Validate(); err != nil {
		return err
	}
	mode := strings.ToLower(s.TLS)
	host, _, _ := net.SplitHostPort(s.Addr)
	var conn net.Conn
	var err error
	if mode == TLSImplicit {
		conn, err = (&tls.Dialer{Config: s.tlsConfig(host)}).DialContext(ctx, "tcp", s.Addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", s.Addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	// Abort blocked I/O if ctx is cancelled before the deadline.
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer c.Close()
	if mode != TLSImplicit {
		ok, _ := c.Extension("STARTTLS")
		switch {
		case ok:
			if err := c.StartTLS(s.tlsConfig(host)); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		case mode == TLSStartTLS:
			return errors.New("smtp: server does not offer STARTTLS")
		}
	}
	if s.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server does not offer AUTH")
		}
		if err := c.Auth(encryptedAuth{smtp.PlainAuth("", s.Username, s.Password, host)}); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	// The server accepted the message at the end of DATA; a failed QUIT must
	// not fail the send, or the job retries and the mail goes out twice.
	_ = c.Quit()
	return nil
}

// header drops line breaks, so values (post titles in subjects) cannot
// inject headers.
func header(v string) string {
	return strings.Join(strings.FieldsFunc(v, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
}

// Memory keeps sent mail in memory (tests, or when SMTP is not configured).
type Memory struct {
	mu   sync.Mutex
	sent []application.Email
}

func (m *Memory) Send(_ context.Context, e application.Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, e)
	return nil
}

func (m *Memory) Sent() []application.Email {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]application.Email(nil), m.sent...)
}
