package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

// covServer2 advertises STARTTLS and answers it with 554; after DATA's 354
// it closes the socket when dropAfterData is set.
func covServer2(t *testing.T, dropAfterData bool) string {
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
				c.Write([]byte("220 hi\r\n"))
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					up := strings.ToUpper(line)
					switch {
					case strings.HasPrefix(up, "EHLO"):
						if dropAfterData {
							c.Write([]byte("250 hi\r\n"))
						} else {
							c.Write([]byte("250-hi\r\n250 STARTTLS\r\n"))
						}
					case strings.HasPrefix(up, "STARTTLS"):
						c.Write([]byte("554 no\r\n"))
					case strings.HasPrefix(up, "DATA"):
						c.Write([]byte("354 go\r\n"))
						if dropAfterData {
							if tc, ok := c.(*net.TCPConn); ok {
								tc.SetLinger(0)
							}
							return
						}
					default:
						c.Write([]byte("250 ok\r\n"))
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestCovStartTLSRejected(t *testing.T) {
	addr := covServer2(t, false)
	err := SMTP{Addr: addr, From: "f@x.y"}.Send(context.Background(), application.Email{To: "a@b.c"})
	if err == nil || !strings.Contains(err.Error(), "starttls") {
		t.Fatalf("got %v", err)
	}
}

func TestCovWriteFails(t *testing.T) {
	addr := covServer2(t, true)
	m := application.Email{To: "a@b.c", Text: strings.Repeat("x", 16<<20)}
	if err := (SMTP{Addr: addr, From: "f@x.y"}).Send(context.Background(), m); err == nil {
		t.Fatal("want write error")
	}
}

func TestCovContextCancelAbortsStall(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(2 * time.Second)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	if err := (SMTP{Addr: ln.Addr().String()}).Send(ctx, application.Email{To: "a"}); err == nil {
		t.Fatal("want error")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancel not honoured")
	}
}
