package emqx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCovKickBadURL(t *testing.T) {
	c := &Client{URL: "http://a b\x7f", Key: "k"}
	if err := c.Kick(context.Background(), "x"); err == nil {
		t.Fatal("want request build error")
	}
}

func TestCovKickTransportErrorAndDefaultClient(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c := &Client{URL: url, Key: "k"} // nil HTTP -> default client
	if err := c.Kick(context.Background(), "x"); err == nil {
		t.Fatal("want dial error")
	}
}
