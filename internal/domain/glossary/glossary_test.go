package glossary

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

func TestNormalize(t *testing.T) {
	in, err := Input{Term: " Cache ", Definition: " fast store ", Lang: " EN ", Aliases: []string{"caching", "CACHE", " ", "caching", "cached"}}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if in.Term != "Cache" || in.Definition != "fast store" || in.Lang != "en" {
		t.Errorf("not trimmed: %+v", in)
	}
	if got := strings.Join(in.Aliases, ","); got != "caching,cached" {
		t.Errorf("aliases = %q", got)
	}
}

func TestNormalizeRejects(t *testing.T) {
	ok := Input{Term: "x", Definition: "y"}
	for name, mod := range map[string]func(*Input){
		"no term":       func(i *Input) { i.Term = " " },
		"long term":     func(i *Input) { i.Term = strings.Repeat("a", maxTerm+1) },
		"no definition": func(i *Input) { i.Definition = "" },
		"long def":      func(i *Input) { i.Definition = strings.Repeat("a", maxDefinition+1) },
		"bad lang":      func(i *Input) { i.Lang = "toolonglang" },
		"many aliases": func(i *Input) {
			for n := 0; n <= maxAliases; n++ {
				i.Aliases = append(i.Aliases, strings.Repeat("a", n+1))
			}
		},
	} {
		in := ok
		mod(&in)
		_, err := in.Normalize()
		var ve *domain.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestNormalizeEntries(t *testing.T) {
	got, err := NormalizeEntries(nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nil -> %#v, %v", got, err)
	}
	got, err = NormalizeEntries([]Entry{{Term: " Cache ", Aliases: []string{"caching", "CACHE"}, Definition: " fast "}})
	if err != nil || len(got) != 1 || got[0].Term != "Cache" || got[0].Definition != "fast" || len(got[0].Aliases) != 1 {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestNormalizeEntriesRejects(t *testing.T) {
	many := make([]Entry, MaxEntries+1)
	for i := range many {
		many[i] = Entry{Term: strings.Repeat("a", i+1), Definition: "d"}
	}
	for name, in := range map[string][]Entry{
		"too many":        many,
		"blank term":      {{Term: " ", Definition: "d"}},
		"blank def":       {{Term: "a"}},
		"repeated term":   {{Term: "Cache", Definition: "d"}, {Term: "cache", Definition: "e"}},
		"alias is a term": {{Term: "Cache", Definition: "d"}, {Term: "Store", Aliases: []string{"CACHE"}, Definition: "e"}},
	} {
		var ve *domain.ValidationError
		if _, err := NormalizeEntries(in); !errors.As(err, &ve) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Decoding onto an existing slice must not merge the new entries into the old.
func TestEntryDecodeDoesNotInherit(t *testing.T) {
	d := struct{ G []Entry }{G: []Entry{{Term: "TTL", Aliases: []string{"time to live"}, Definition: "old"}}}
	if err := json.Unmarshal([]byte(`{"G":[{"term":"x"}]}`), &d); err != nil {
		t.Fatal(err)
	}
	if got := d.G[0]; got.Term != "x" || got.Definition != "" || len(got.Aliases) != 0 {
		t.Errorf("entry inherited old fields: %+v", got)
	}
}
