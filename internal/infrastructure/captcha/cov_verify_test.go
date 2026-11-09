package captcha

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCovVerifyBadEndpoint(t *testing.T) {
	if err := (Turnstile{URL: "http://a b\x7f"}).Verify(context.Background(), "tok", ""); err == nil {
		t.Fatal("want build error")
	}
}

func TestCovVerifyDefaultEndpointFailsFast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Turnstile{}.Verify(ctx, "tok", "1.2.3.4")
	if err == nil || err == ErrFailed {
		t.Fatalf("want transport error, got %v", err)
	}
}

func TestCovVerifyStatusAndBadJSON(t *testing.T) {
	st := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer st.Close()
	if err := (Turnstile{URL: st.URL}).Verify(context.Background(), "tok", ""); err == nil || err == ErrFailed {
		t.Fatalf("status: %v", err)
	}
	js := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("nope")) }))
	defer js.Close()
	if err := (Turnstile{URL: js.URL}).Verify(context.Background(), "tok", ""); err == nil || err == ErrFailed {
		t.Fatalf("json: %v", err)
	}
}
