package post

import (
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/glossary"
)

func TestDraftGlossary(t *testing.T) {
	now := time.Now()
	d, err := Draft{Title: "T"}.Normalize(now)
	if err != nil || d.Glossary == nil || len(d.Glossary) != 0 {
		t.Fatalf("no glossary -> %#v, %v", d.Glossary, err)
	}
	d, err = Draft{Title: "T", Glossary: []glossary.Entry{{Term: " Cache ", Definition: "fast"}}}.Normalize(now)
	if err != nil || d.Glossary[0].Term != "Cache" {
		t.Fatalf("got %#v, %v", d.Glossary, err)
	}
	if _, err = (Draft{Title: "T", Glossary: []glossary.Entry{{Term: "x"}}}).Normalize(now); err == nil ||
		!strings.Contains(err.Error(), "definition") {
		t.Errorf("blank definition accepted: %v", err)
	}
}

func TestPostDraftKeepsGlossary(t *testing.T) {
	g := []glossary.Entry{{Term: "Cache", Definition: "fast"}}
	if got := (Post{Glossary: g}).Draft().Glossary; len(got) != 1 || got[0].Term != "Cache" {
		t.Errorf("Draft().Glossary = %#v", got)
	}
}
