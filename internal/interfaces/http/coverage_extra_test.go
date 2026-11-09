package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bakhod1r/errorx"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
)

func TestWriteErrorStatuses(t *testing.T) {
	for status, want := range map[int]int{
		http.StatusBadRequest:         400,
		http.StatusUnauthorized:       401,
		http.StatusForbidden:          403,
		http.StatusNotFound:           404,
		http.StatusConflict:           409,
		http.StatusTooManyRequests:    429,
		http.StatusServiceUnavailable: 503,
		http.StatusTeapot:             500,
	} {
		w := httptest.NewRecorder()
		writeError(w, httptest.NewRequest("GET", "/x", nil), status, "msg")
		if w.Code != want {
			t.Errorf("status %d: got %d, want %d", status, w.Code, want)
		}
	}
}

type failWriter struct{ http.ResponseWriter }

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestProblemWriteFailureIsLogged(t *testing.T) {
	problem(failWriter{httptest.NewRecorder()}, httptest.NewRequest("GET", "/x", nil), errorx.New(errorx.ErrInternal, "x"))
}

func TestMQTTKind(t *testing.T) {
	if mqttKind("publish") != "publish" || mqttKind("subscribe") != "subscribe" || mqttKind("x") != "other" {
		t.Fatal("mqttKind")
	}
}

func TestToolArgHelpers(t *testing.T) {
	if truncate("héllo", 2) != "hé" || truncate("ab", 5) != "ab" {
		t.Error("truncate")
	}
	if checkID("id", "") == nil || checkID("id", strings.Repeat("a", 65)) == nil || checkID("id", "ok") != nil {
		t.Error("checkID")
	}
	var v struct{ A int }
	if err := decodeArgs(nil, &v); err != nil {
		t.Error(err)
	}
	if err := decodeArgs(json.RawMessage(`{"A":"x"}`), &v); err == nil || !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("decodeArgs: %v", err)
	}
}

func TestSmallPureHelpers(t *testing.T) {
	if itoa(42) != "42" {
		t.Error("itoa")
	}
	// pathVersion and limitParam
	mux := http.NewServeMux()
	var got int
	var ok bool
	mux.HandleFunc("GET /v/{version}", func(w http.ResponseWriter, r *http.Request) { got, ok = pathVersion(w, r) })
	for path, want := range map[string]bool{"/v/3": true, "/v/0": false, "/v/abc": false} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if ok != want || (want && got != 3) || (!want && w.Code != 400) {
			t.Errorf("%s: %d %v (%d)", path, got, ok, w.Code)
		}
	}
	if limitParam(httptest.NewRequest("GET", "/x?limit=7", nil), 20) != 7 || limitParam(httptest.NewRequest("GET", "/x?limit=z", nil), 20) != 20 {
		t.Error("limitParam")
	}
	if sddFileName(" a/b:c ", "fb") != "a_b_c.sdd" || sddFileName("   ", "fb") != "fb.sdd" || len(sddFileName(strings.Repeat("a", 150), "fb")) != 104 {
		t.Error("sddFileName")
	}
	if trimSlash("a//") != "a" || trimSlash("") != "" {
		t.Error("trimSlash")
	}
	h := &Handler{}
	h.SiteURL = "https://site.test/"
	if h.siteName() != "IWillBeGoogler" {
		t.Error("default site name")
	}
	h.SiteName = "Mine"
	if h.siteName() != "Mine" {
		t.Error("site name")
	}
	if h.absURL("") != "" || h.absURL("https://x/y") != "https://x/y" || h.absURL("/api/u/1") != "https://site.test/api/u/1" {
		t.Error("absURL")
	}
	if got := plainText("# Title **bold** [link](http://x) ![img](y)\n\n text"); got != "Title bold link img text" {
		t.Errorf("plainText = %q", got)
	}
	if len(plainText(strings.Repeat("a", 5000))) != 4000 {
		t.Error("plainText cap")
	}
	if clipRunes("héllo wörld", 6) != "héllo…" || clipRunes("ab", 5) != "ab" {
		t.Error("clipRunes")
	}
	if personJSON(application.Person{Name: "x"}).ID != "" || personJSON(application.Person{ID: 3}).ID != "3" {
		t.Error("personJSON")
	}
}
