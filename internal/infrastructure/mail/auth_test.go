package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

// srvOpts shapes the fake server: what it offers and what it expects.
type srvOpts struct {
	implicitTLS bool // TLS from the first byte
	startTLS    bool // advertise STARTTLS
	auth        bool // advertise AUTH PLAIN
	user, pass  string
}

type fake struct {
	addr   string
	client *tls.Config
	mu     sync.Mutex
	log    []string // commands received, with AUTH payloads redacted to "AUTH"
	data   chan string
}

func (f *fake) cmds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func startFake(t *testing.T, o srvOpts) *fake {
	t.Helper()
	cert, pool := selfSigned(t)
	scfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fake{addr: ln.Addr().String(), client: &tls.Config{RootCAs: pool}, data: make(chan string, 1)}
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		var c net.Conn = raw
		encrypted := false
		if o.implicitTLS {
			c, encrypted = tls.Server(raw, scfg), true
		}
		r := bufio.NewReader(c)
		say := func(s string) { c.Write([]byte(s + "\r\n")) }
		say("220 fake")
		authed := o.user == ""
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			cmd := strings.ToUpper(line)
			f.mu.Lock()
			if strings.HasPrefix(cmd, "AUTH") {
				f.log = append(f.log, "AUTH")
			} else {
				f.log = append(f.log, cmd)
			}
			f.mu.Unlock()
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				c.Write([]byte("250-fake\r\n"))
				if o.startTLS && !encrypted {
					c.Write([]byte("250-STARTTLS\r\n"))
				}
				if o.auth {
					c.Write([]byte("250-AUTH PLAIN\r\n"))
				}
				say("250 8BITMIME")
			case cmd == "STARTTLS":
				say("220 go")
				c, encrypted = tls.Server(raw, scfg), true
				r = bufio.NewReader(c)
			case strings.HasPrefix(cmd, "AUTH PLAIN "):
				p, _ := base64.StdEncoding.DecodeString(line[len("AUTH PLAIN "):])
				if !encrypted || string(p) != "\x00"+o.user+"\x00"+o.pass {
					say("535 bad credentials")
					continue
				}
				authed = true
				say("235 ok")
			case strings.HasPrefix(cmd, "MAIL"):
				if !authed {
					say("530 auth required")
					continue
				}
				say("250 ok")
			case cmd == "DATA":
				say("354 go")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				f.data <- b.String()
				say("250 ok")
			case cmd == "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return f
}

func send(s SMTP) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.Send(ctx, application.Email{To: "a@example.com", Subject: "s", Text: "body"})
}

func TestSMTPAuth(t *testing.T) {
	creds := srvOpts{startTLS: true, auth: true, user: "apikey", pass: "s3cret"}
	tests := []struct {
		name    string
		o       srvOpts
		mode    string
		user    string
		pass    string
		wantErr string
		want    []string // commands that must appear, in order
		deny    []string // commands that must not appear
	}{
		{name: "starttls then auth", o: creds, mode: TLSStartTLS, user: "apikey", pass: "s3cret",
			want: []string{"STARTTLS", "AUTH", "MAIL FROM:<BLOG@EXAMPLE.COM>"}},
		{name: "implicit tls then auth", o: srvOpts{implicitTLS: true, auth: true, user: "apikey", pass: "s3cret"}, mode: TLSImplicit, user: "apikey", pass: "s3cret",
			want: []string{"AUTH", "MAIL FROM:<BLOG@EXAMPLE.COM>"}, deny: []string{"STARTTLS"}},
		{name: "wrong password", o: creds, mode: TLSStartTLS, user: "apikey", pass: "nope", wantErr: "535"},
		{name: "starttls required but not offered", o: srvOpts{auth: true, user: "apikey", pass: "s3cret"}, mode: TLSStartTLS,
			user: "apikey", pass: "s3cret", wantErr: "STARTTLS", deny: []string{"AUTH", "MAIL"}},
		{name: "auth not offered", o: srvOpts{startTLS: true}, mode: TLSStartTLS, user: "apikey", pass: "x", wantErr: "AUTH"},
		{name: "starttls without auth", o: srvOpts{startTLS: true}, mode: TLSStartTLS,
			want: []string{"STARTTLS", "MAIL FROM:<BLOG@EXAMPLE.COM>"}, deny: []string{"AUTH"}},
		{name: "none upgrades opportunistically", o: srvOpts{startTLS: true}, mode: TLSNone, want: []string{"STARTTLS"}},
		{name: "none stays plain", o: srvOpts{}, mode: "", deny: []string{"STARTTLS", "AUTH"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := startFake(t, tc.o)
			err := send(SMTP{Addr: f.addr, From: "blog@example.com", Username: tc.user, Password: tc.pass, TLS: tc.mode, TLSConfig: f.client})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if got := <-f.data; !strings.Contains(got, "body") {
				t.Errorf("data = %q", got)
			}
			log := strings.Join(f.cmds(), "|")
			pos := -1
			for _, w := range tc.want {
				i := strings.Index(log, w)
				if i < 0 || i < pos {
					t.Errorf("want %q in order in %s", w, log)
				}
				pos = i
			}
			for _, d := range tc.deny {
				if strings.Contains(log, d) {
					t.Errorf("must not send %q: %s", d, log)
				}
			}
		})
	}
}

// Auth over a connection that never upgraded must not reach the wire.
func TestSMTPNeverSendsCredentialsInClear(t *testing.T) {
	f := startFake(t, srvOpts{auth: true, user: "u", pass: "p"})
	err := send(SMTP{Addr: f.addr, From: "blog@example.com", Username: "u", Password: "p", TLS: TLSNone})
	if err == nil || !strings.Contains(err.Error(), "SMTP_TLS") {
		t.Fatalf("err = %v", err)
	}
	if cmds := f.cmds(); len(cmds) != 0 {
		t.Errorf("connected anyway: %v", cmds)
	}
}

func TestSMTPValidate(t *testing.T) {
	for tls, ok := range map[string]bool{"": true, "none": true, "STARTTLS": true, "tls": true, "ssl": false} {
		if err := (SMTP{TLS: tls}).Validate(); (err == nil) != ok {
			t.Errorf("TLS %q: %v", tls, err)
		}
	}
	if err := (SMTP{Username: "u", Password: "p"}).Validate(); err == nil {
		t.Error("auth over none accepted")
	}
	if err := (SMTP{Username: "u", Password: "p", TLS: "starttls"}).Validate(); err != nil {
		t.Error(err)
	}
}
