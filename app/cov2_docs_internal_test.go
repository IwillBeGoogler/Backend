package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/config"
)

func TestMountDocs(t *testing.T) {
	// Production without a key: console disabled.
	mux := http.NewServeMux()
	mountDocs(mux, config.Config{Env: "production"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled docs = %d", rec.Code)
	}

	// With a key: redirect keeps the query, subtree is handled.
	mux = http.NewServeMux()
	mountDocs(mux, config.Config{Env: "production", DocsKey: "k", DocsDir: "..", PublicURL: "http://localhost"})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs?key=k", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/api/docs/?key=k" {
		t.Fatalf("redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/", nil))
	if rec.Code == http.StatusNotFound && rec.Body.Len() == 0 {
		t.Fatal("docs subtree not mounted")
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if rec.Header().Get("Location") != "/api/docs/" {
		t.Fatalf("plain redirect = %q", rec.Header().Get("Location"))
	}
}
