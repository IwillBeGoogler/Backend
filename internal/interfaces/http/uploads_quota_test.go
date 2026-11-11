package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeLedger struct {
	used      int64
	usageErr  error
	recordErr error
	recorded  map[string]int64
}

func (l *fakeLedger) Usage(context.Context, int) (int64, error) { return l.used, l.usageErr }

func (l *fakeLedger) Record(_ context.Context, key string, _ int, size int64) error {
	if l.recordErr != nil {
		return l.recordErr
	}
	if l.recorded == nil {
		l.recorded = map[string]int64{}
	}
	l.recorded[key] = size
	l.used += size
	return nil
}

func TestUploadQuota(t *testing.T) {
	img := pngBytes(t)
	post := func(g *blogRig) *httptest.ResponseRecorder {
		t.Helper()
		req, ct := multipartReq(t, "file", img)
		req.Header.Set("Content-Type", ct)
		req = req.WithContext(context.WithValue(req.Context(), testUser{}, 1))
		w := httptest.NewRecorder()
		g.mux.ServeHTTP(w, req)
		return w
	}

	// the first image is stored (as WebP) and counted; with the quota then exactly used up, the second is refused
	g := newRichRig(t, nil)
	ledger := &fakeLedger{}
	g.h.Uploads, g.h.UploadQuotaBytes = ledger, 1<<20
	if w := post(g); w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), ".webp") {
		t.Fatalf("first upload: %d %s", w.Code, w.Body)
	}
	if len(ledger.recorded) != 1 || ledger.used <= 0 {
		t.Fatalf("ledger = %+v", ledger)
	}
	g.h.UploadQuotaBytes = ledger.used
	if w := post(g); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "storage is full") {
		t.Fatalf("over quota: %d %s", w.Code, w.Body)
	}
	if len(ledger.recorded) != 1 {
		t.Error("a refused upload was recorded")
	}

	// quota 0 is unlimited, the ledger still counts
	g.h.UploadQuotaBytes = 0
	if w := post(g); w.Code != http.StatusCreated || len(ledger.recorded) != 2 {
		t.Errorf("unlimited: %d, %d recorded", w.Code, len(ledger.recorded))
	}

	// the ledger cannot answer: nothing is stored
	g.h.Uploads, g.h.UploadQuotaBytes = &fakeLedger{usageErr: errors.New("db down")}, 1<<20
	if w := post(g); w.Code != http.StatusInternalServerError {
		t.Errorf("usage error: %d", w.Code)
	}
	// the image is stored but could not be recorded: the upload still succeeds
	g.h.Uploads = &fakeLedger{recordErr: errors.New("db down")}
	if w := post(g); w.Code != http.StatusCreated {
		t.Errorf("record error: %d %s", w.Code, w.Body)
	}
}
