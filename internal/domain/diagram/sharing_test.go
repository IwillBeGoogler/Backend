package diagram_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func TestShareToken(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		token, hash := diagram.NewShareToken()
		if len(token) != 22 || !diagram.ValidShareToken(token) || seen[token] {
			t.Fatalf("token %q", token)
		}
		seen[token] = true
		if len(hash) != 64 || hash != diagram.HashToken(token) || strings.Contains(hash, token) {
			t.Fatalf("hash %q", hash)
		}
		if !diagram.HashEqual(hash, diagram.HashToken(token)) || diagram.HashEqual(hash, diagram.HashToken(token+"x")) {
			t.Fatal("HashEqual")
		}
	}
	// the sha256 of "abc", lowercase hex
	if got := diagram.HashToken("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("sha256 = %s", got)
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 129), "has space 0123456789", "../../../../etc/passwd"} {
		if diagram.ValidShareToken(bad) {
			t.Errorf("%q must not be a token", bad)
		}
	}
}

func TestShareLinkExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	past, future := now.Add(-time.Second), now.Add(time.Second)
	if (diagram.ShareLink{}).Expired(now) || (diagram.ShareLink{ExpiresAt: &future}).Expired(now) {
		t.Error("open links must not be expired")
	}
	if !(diagram.ShareLink{ExpiresAt: &past}).Expired(now) || !(diagram.ShareLink{ExpiresAt: &now}).Expired(now) {
		t.Error("a link expires at its expiry instant")
	}
}

func TestDetectMime(t *testing.T) {
	for want, b := range map[string]string{
		diagram.MimePNG:  "\x89PNG\r\n\x1a\nxxxx",
		diagram.MimeJPEG: "\xff\xd8\xff\xe0xx",
		diagram.MimeWebP: "RIFF\x04\x00\x00\x00WEBPVP8 ",
		"":               "GIF89a",
	} {
		if got := diagram.DetectMime([]byte(b)); got != want {
			t.Errorf("DetectMime(%q) = %q, want %q", b[:4], got, want)
		}
	}
	for _, b := range []string{"", "\x89PNG", "RIFF\x04\x00\x00\x00WAVEfmt ", "<svg xmlns='http://www.w3.org/2000/svg'/>", "<html></html>"} {
		if got := diagram.DetectMime([]byte(b)); got != "" {
			t.Errorf("DetectMime(%q) = %q, want none", b, got)
		}
	}
	if !diagram.ValidHash(diagram.HashBytes([]byte("x"))) || diagram.ValidHash("ABC") || diagram.ValidHash(strings.Repeat("A", 64)) {
		t.Error("ValidHash")
	}
}

func TestCheckLibrary(t *testing.T) {
	if err := diagram.CheckLibrary(diagram.EmptyLibrary); err != nil {
		t.Errorf("the empty library: %v", err)
	}
	if err := diagram.CheckLibrary(json.RawMessage(`{"components":{},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{},"future":[1]}`)); err != nil {
		t.Errorf("unknown keys: %v", err)
	}
	for name, doc := range map[string]string{
		"null":          `null`,
		"string":        `"x"`,
		"components":    `{"components":[],"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{}}`,
		"styles":        `{"components":{},"tokens":{}}`,
		"tokens":        `{"components":{},"styles":{"colors":{},"texts":{},"effects":{}}}`,
		"long name":     `{"components":{"c":{"id":"c","name":"` + strings.Repeat("x", 201) + `","rev":1,"width":1,"height":1,"nodes":[]}},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{}}`,
		"node type len": `{"components":{"c":{"id":"c","name":"n","rev":1,"width":1,"height":1,"nodes":[{"key":"k","type":"` + strings.Repeat("t", 33) + `","x":0,"y":0,"data":{}}]}},"styles":{"colors":{},"texts":{},"effects":{}},"tokens":{}}`,
		"bold":          `{"components":{},"styles":{"colors":{},"texts":{"t":{"id":"t","name":"n","bold":"yes"}},"effects":{}},"tokens":{}}`,
	} {
		err := diagram.CheckLibrary(json.RawMessage(doc))
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), ":") {
			t.Errorf("%s: message %q has no path", name, err)
		}
	}
}

func TestPreparePrivate(t *testing.T) {
	out, err := diagram.PreparePrivate(json.RawMessage(
		`{"nodes":[{"id":"a","type":"sticky","position":{"x":1,"y":2},"extra":true,"data":{"label":"hi","authorId":"someone else"}}],"edges":[{"id":"e","source":"a","target":"b","note":1}]}`), "42")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Nodes []struct {
			Extra bool
			Data  map[string]any
		}
		Edges []map[string]any
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if n := got.Nodes[0]; !n.Extra || n.Data["private"] != true || n.Data["authorId"] != "42" || n.Data["label"] != "hi" || got.Edges[0]["note"] != float64(1) {
		t.Errorf("prepared = %s", out)
	}
}
