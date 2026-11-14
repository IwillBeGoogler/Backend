// Package glossary holds the terms the article reader explains in a tooltip.
package glossary

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

const (
	maxTerm       = 80
	maxAliases    = 10
	maxDefinition = 600
)

// Term explains Term (and its Aliases) in Lang; an empty Lang applies to
// every language.
type Term struct {
	ID         int       `json:"id"`
	Term       string    `json:"term"`
	Aliases    []string  `json:"aliases"`
	Definition string    `json:"definition"`
	Lang       string    `json:"lang"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Input creates or replaces a term.
type Input struct {
	Term       string   `json:"term"`
	Aliases    []string `json:"aliases"`
	Definition string   `json:"definition"`
	Lang       string   `json:"lang"`
}

// Normalize validates in and trims it; aliases are de-duplicated.
func (in Input) Normalize() (Input, error) {
	in.Term = strings.TrimSpace(in.Term)
	in.Definition = strings.TrimSpace(in.Definition)
	in.Lang = strings.ToLower(strings.TrimSpace(in.Lang))
	switch {
	case in.Term == "":
		return in, domain.Invalid("term required")
	case utf8.RuneCountInString(in.Term) > maxTerm:
		return in, domain.Invalid("term too long")
	case in.Definition == "":
		return in, domain.Invalid("definition required")
	case utf8.RuneCountInString(in.Definition) > maxDefinition:
		return in, domain.Invalid("definition too long")
	case len(in.Lang) > 8:
		return in, domain.Invalid("bad lang")
	}
	seen := map[string]bool{strings.ToLower(in.Term): true}
	aliases := []string{}
	for _, a := range in.Aliases {
		a = strings.TrimSpace(a)
		key := strings.ToLower(a)
		if a == "" || seen[key] {
			continue
		}
		if utf8.RuneCountInString(a) > maxTerm {
			return in, domain.Invalid("alias too long")
		}
		seen[key] = true
		aliases = append(aliases, a)
	}
	if len(aliases) > maxAliases {
		return in, domain.Invalid("too many aliases")
	}
	in.Aliases = aliases
	return in, nil
}

// Repository returns domain.ErrConflict when the term already exists in that
// language (case-insensitive) and domain.ErrNotFound for a missing id.
type Repository interface {
	// List returns terms for lang plus the language-neutral ones; an empty
	// lang returns every term. Ordered by term.
	List(ctx context.Context, lang string) ([]Term, error)
	Create(ctx context.Context, in Input) (Term, error)
	Update(ctx context.Context, id int, in Input) (Term, error)
	Delete(ctx context.Context, id int) error
}

// MaxEntries is how many terms one article may define.
const MaxEntries = 50

// Entry is a term defined by one article; it applies in the article's own
// language, so it has no Lang.
type Entry struct {
	Term       string   `json:"term"`
	Aliases    []string `json:"aliases"`
	Definition string   `json:"definition"`
}

// UnmarshalJSON decodes into a fresh Entry. Post updates decode the request
// onto the stored draft, and encoding/json reuses existing slice elements, so
// without this a sent {"term":"x"} would inherit the old entry's definition.
func (e *Entry) UnmarshalJSON(b []byte) error {
	type plain Entry
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*e = Entry(p)
	return nil
}

// NormalizeEntries validates an article's terms. Nil becomes an empty list;
// a term (or alias) may appear only once.
func NormalizeEntries(in []Entry) ([]Entry, error) {
	if len(in) > MaxEntries {
		return nil, domain.Invalid("too many glossary terms")
	}
	out := make([]Entry, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		n, err := Input{Term: e.Term, Aliases: e.Aliases, Definition: e.Definition}.Normalize()
		if err != nil {
			return nil, err
		}
		for _, k := range append([]string{n.Term}, n.Aliases...) {
			if seen[strings.ToLower(k)] {
				return nil, domain.Invalid("glossary term repeated: " + k)
			}
			seen[strings.ToLower(k)] = true
		}
		out = append(out, Entry{Term: n.Term, Aliases: n.Aliases, Definition: n.Definition})
	}
	return out, nil
}
