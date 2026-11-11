package category

import (
	"context"
	"regexp"
	"strings"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
)

// Category is a topic node; ParentID 0 is a root. Count is the published
// posts filed directly under it; clients sum subtrees if they need to.
type Category struct {
	ID          int               `json:"id"`
	ParentID    int               `json:"parent_id"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Names       map[string]string `json:"names"` // uz, ru translations of Name
	Description string            `json:"description"`
	Position    int               `json:"position"`
	Count       int               `json:"count"`
}

// Input creates a category.
type Input struct {
	Name        string            `json:"name"`
	ParentID    int               `json:"parent_id"`
	Slug        string            `json:"slug"` // default: from name
	Names       map[string]string `json:"names"`
	Description string            `json:"description"`
	Position    int               `json:"position"`
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Normalize validates in and fills the slug.
func (in Input) Normalize() (Input, error) {
	name, err := NormalizeName(in.Name)
	if err != nil {
		return in, err
	}
	in.Name = name
	in.Slug = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(in.Slug)), "-"), "-")
	if in.Slug == "" {
		in.Slug = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	}
	switch {
	case in.Slug == "" || len(in.Slug) > 60:
		return in, domain.Invalid("bad slug")
	case in.ParentID < 0:
		return in, domain.Invalid("bad parent_id")
	case len(in.Description) > 300:
		return in, domain.Invalid("description too long")
	}
	names := map[string]string{}
	for _, l := range []string{"uz", "ru"} {
		if v := strings.TrimSpace(in.Names[l]); v != "" {
			if len(v) > 50 {
				return in, domain.Invalid("name too long")
			}
			names[l] = v
		}
	}
	in.Names = names
	return in, nil
}

// NormalizeName validates a category name.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", domain.Invalid("name required")
	case len(name) > 50:
		return "", domain.Invalid("name too long")
	}
	return name, nil
}

// Repository returns domain.ErrConflict on duplicate name (case-insensitive)
// or slug, and domain.ErrNotFound for a missing category or parent.
type Repository interface {
	List(ctx context.Context) ([]Category, error)
	Exists(ctx context.Context, id int) (bool, error)
	Create(ctx context.Context, in Input) (Category, error)
	BySlug(ctx context.Context, slug string) (Category, error)
	// Delete removes the subtree; posts stay, without those categories.
	Delete(ctx context.Context, id int) error
}
