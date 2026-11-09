package webhooks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func req(url string) application.WebhookRequest {
	body := []byte(`{"event":"file.updated"}`)
	ts := time.Now().Unix()
	return application.WebhookRequest{URL: url, Event: "file.updated", DeliveryID: "dlv_1", Timestamp: ts,
		Signature: diagram.Sign("whsec_x", ts, body), Body: body}
}

func TestSendHeadersAndSignature(t *testing.T) {
	var got http.Header
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	r := req(srv.URL)
	status, err := New(true, nil).Send(context.Background(), r)
	if err != nil || status != 204 {
		t.Fatalf("send = %d, %v", status, err)
	}
	if got.Get(diagram.HeaderEvent) != "file.updated" || got.Get(diagram.HeaderDelivery) != "dlv_1" {
		t.Errorf("headers = %v", got)
	}
	ts, _ := strconv.ParseInt(got.Get(diagram.HeaderTimestamp), 10, 64)
	if !diagram.VerifySignature("whsec_x", ts, []byte(body), got.Get(diagram.HeaderSignature), time.Now(), 0) {
		t.Error("the signature does not verify")
	}
	if diagram.VerifySignature("whsec_other", ts, []byte(body), got.Get(diagram.HeaderSignature), time.Time{}, 0) {
		t.Error("a wrong secret must not verify")
	}
}

func TestSendBlocksPrivateTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("must not be reached") }))
	defer srv.Close()
	_, err := New(false, nil).Send(context.Background(), req(srv.URL))
	if !errors.Is(err, application.ErrWebhookURL) {
		t.Fatalf("loopback target = %v", err)
	}
	if err := New(false, nil).CheckURL(context.Background(), srv.URL); err == nil {
		t.Error("CheckURL must reject loopback")
	}
}

func TestSendDoesNotFollowRedirects(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()
	status, err := New(true, nil).Send(context.Background(), req(srv.URL))
	if err != nil || status != 302 || hit {
		t.Fatalf("status %d err %v followed %v", status, err, hit)
	}
}

func TestSendTimesOutAndCapsTheResponse(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 4<<20))
	}))
	defer big.Close()
	if status, err := New(true, nil).Send(context.Background(), req(big.URL)); err != nil || status != 200 {
		t.Fatalf("big response: %d %v", status, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client hanging up
		<-r.Context().Done()
	}))
	defer slow.Close()
	if _, err := New(true, nil).Send(ctx, req(slow.URL)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow endpoint = %v", err)
	}
}
