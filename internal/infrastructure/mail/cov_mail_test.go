package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

// covServer is a scripted SMTP server: the command prefix in failOn gets a
// 554 reply; "" never fails; dropAfter closes the socket on that command.
func covServer(t *testing.T, failOn, dropOn string, greet bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if !greet {
					return
				}
				c.Write([]byte("220 hi\r\n"))
				r := bufio.NewReader(c)
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if line == ".\r\n" {
							inData = false
							if failOn == "END" {
								c.Write([]byte("554 no\r\n"))
								continue
							}
							c.Write([]byte("250 ok\r\n"))
						}
						continue
					}
					up := strings.ToUpper(line)
					if dropOn != "" && strings.HasPrefix(up, dropOn) {
						return
					}
					if failOn != "" && strings.HasPrefix(up, failOn) {
						c.Write([]byte("554 no\r\n"))
						continue
					}
					switch {
					case strings.HasPrefix(up, "EHLO"):
						c.Write([]byte("250 hi\r\n"))
					case strings.HasPrefix(up, "DATA"):
						inData = true
						c.Write([]byte("354 go\r\n"))
					case strings.HasPrefix(up, "QUIT"):
						c.Write([]byte("221 bye\r\n"))
						return
					default:
						c.Write([]byte("250 ok\r\n"))
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestCovTLSConfigDefault(t *testing.T) {
	c := SMTP{}.tlsConfig("h")
	if c.ServerName != "h" || c.MinVersion == 0 {
		t.Fatalf("%+v", c)
	}
}

func TestCovSendFailures(t *testing.T) {
	ctx := context.Background()
	m := application.Email{To: "a@b.c", Subject: "s", Text: "x"}
	cases := []struct {
		name         string
		failOn, drop string
		greet        bool
	}{
		{"handshake", "", "", false},
		{"mail", "MAIL", "", true},
		{"rcpt", "RCPT", "", true},
		{"data", "DATA", "", true},
		{"end", "END", "", true},
		{"dropDuringData", "", "DATA", true},
	}
	for _, tc := range cases {
		addr := covServer(t, tc.failOn, tc.drop, tc.greet)
		if err := (SMTP{Addr: addr, From: "f@x.y"}).Send(ctx, m); err == nil {
			t.Errorf("%s: want error", tc.name)
		}
	}
	// dial failure
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if err := (SMTP{Addr: addr}).Send(ctx, m); err == nil {
		t.Error("dial: want error")
	}
	// success path with unsubscribe header
	addr = covServer(t, "", "", true)
	m.Unsubscribe = "http://u"
	if err := (SMTP{Addr: addr, From: "f@x.y"}).Send(ctx, m); err != nil {
		t.Errorf("ok: %v", err)
	}
}

func TestCovMemory(t *testing.T) {
	var m Memory
	if err := m.Send(context.Background(), application.Email{To: "a"}); err != nil {
		t.Fatal(err)
	}
	got := m.Sent()
	if len(got) != 1 || got[0].To != "a" {
		t.Fatalf("%v", got)
	}
}
