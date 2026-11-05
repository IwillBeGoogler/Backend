package emqx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKick(t *testing.T) {
	var method, path, user, pass string
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		user, pass, _ = r.BasicAuth()
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL + "/", Key: "k", Secret: "s"}
	if err := c.Kick(context.Background(), "g.abc"); err != nil {
		t.Fatal(err)
	}
	if method != "DELETE" || path != "/api/v5/clients/g.abc" || user != "k" || pass != "s" {
		t.Fatalf("got %s %s %s:%s", method, path, user, pass)
	}
	status = http.StatusNotFound
	if err := c.Kick(context.Background(), "x"); err != nil {
		t.Fatal("404 must be fine", err)
	}
	status = http.StatusInternalServerError
	if err := c.Kick(context.Background(), "x"); err == nil {
		t.Fatal("500 must fail")
	}
	if (&Client{}).Enabled() || (&Client{}).Kick(context.Background(), "x") != nil {
		t.Fatal("zero client must be a no-op")
	}
}
