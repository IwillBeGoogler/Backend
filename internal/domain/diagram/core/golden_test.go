package core

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
)

// The golden file is produced by the TypeScript core (testdata/gen-golden.ts,
// run gen/gen-golden.sh); these tests check the Go port gives the same output.

type golden struct {
	Layout []struct {
		Name string
		Page Obj
		Algo string
		Opts struct {
			Direction string
			Gap       *float64
			Seed      *float64
			Only      []string
			RootID    string
		}
		Expected struct {
			Nodes []any
			Edges []any
			Moved int
		}
	}
	DslApply []struct {
		Name     string
		Text     string
		Page     Obj
		Layout   *bool
		Expected Obj
	}
	DslParse []struct {
		Name     string
		Text     string
		Expected []Diagnostic
	}
	Print []struct {
		Name     string
		Page     Obj
		Expected string
	}
	Lint []struct {
		Name string
		Page Obj
		Cfg  struct {
			Rules map[string]struct {
				Enabled  *bool
				Severity string
			}
			Naming string
		}
		Expected []any
	}
	Export []struct {
		Name     string
		Page     Obj
		Format   string
		Expected string
	}
	Search []struct {
		Name     string
		Query    string
		Kind     string
		Limit    int
		Expected []any
	}
	Resolve []struct {
		Ref      string
		Expected any
	}
}

func loadGolden(t *testing.T) *golden {
	t.Helper()
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return &g
}

// same compares two JSON values; numbers within tol.
func same(a, b any, tol float64, path string) error {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want object, got %T (%v)", path, b, b)
		}
		for k, v := range x {
			if _, ok := y[k]; !ok {
				return fmt.Errorf("%s.%s: missing in got (want %v)", path, k, v)
			}
			if err := same(v, y[k], tol, path+"."+k); err != nil {
				return err
			}
		}
		for k, v := range y {
			if _, ok := x[k]; !ok {
				return fmt.Errorf("%s.%s: unexpected in got (%v)", path, k, v)
			}
		}
		return nil
	case []any:
		y, ok := b.([]any)
		if !ok {
			if b == nil && len(x) == 0 {
				return nil
			}
			return fmt.Errorf("%s: want array, got %T", path, b)
		}
		if len(x) != len(y) {
			return fmt.Errorf("%s: want %d items, got %d", path, len(x), len(y))
		}
		for i := range x {
			if err := same(x[i], y[i], tol, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case float64:
		y, ok := b.(float64)
		if !ok || math.Abs(x-y) > tol {
			return fmt.Errorf("%s: want %v, got %v", path, a, b)
		}
		return nil
	case nil:
		if b != nil {
			return fmt.Errorf("%s: want null, got %v", path, b)
		}
		return nil
	}
	if a != b {
		return fmt.Errorf("%s: want %v, got %v", path, a, b)
	}
	return nil
}

func TestGoldenLayout(t *testing.T) {
	for _, c := range loadGolden(t).Layout {
		t.Run(c.Name, func(t *testing.T) {
			page := NormalizePage(c.Page)
			o := LayoutOptions{Direction: c.Opts.Direction, Gap: c.Opts.Gap, Seed: c.Opts.Seed, Only: c.Opts.Only, RootID: c.Opts.RootID}
			moved := ApplyLayout(page, c.Algo, o)
			if moved != c.Expected.Moved {
				t.Errorf("moved %d, want %d", moved, c.Expected.Moved)
			}
			tol := 1e-6 // force included: trig.go and hypot2 reproduce V8 bit for bit
			if err := same(NormalizeAny(c.Expected.Nodes), page["nodes"], tol, "nodes"); err != nil {
				t.Error(err)
			}
			if err := same(NormalizeAny(c.Expected.Edges), page["edges"], tol, "edges"); err != nil {
				t.Error(err)
			}
		})
	}
}

// NormalizeAny is Normalize for tests (nil slices become empty arrays).
func NormalizeAny(v any) any { return Normalize(v) }

func TestGoldenPrint(t *testing.T) {
	for _, c := range loadGolden(t).Print {
		t.Run(c.Name, func(t *testing.T) {
			page := NormalizePage(c.Page)
			if got := PrintPage(page, jsStr(page["name"])); got != c.Expected {
				t.Errorf("text differs\n--- want\n%s\n--- got\n%s", c.Expected, got)
			}
		})
	}
}

func TestGoldenParse(t *testing.T) {
	for _, c := range loadGolden(t).DslParse {
		t.Run(c.Name, func(t *testing.T) {
			got := ParseDSL(c.Text).Diagnostics
			if err := same(Normalize(c.Expected), Normalize(got), 0, "diagnostics"); err != nil {
				t.Errorf("%v\n got %v", err, got)
			}
		})
	}
}

func TestGoldenApply(t *testing.T) {
	for _, c := range loadGolden(t).DslApply {
		t.Run(c.Name, func(t *testing.T) {
			layout := c.Layout == nil || *c.Layout
			res, _ := ApplyDSL(c.Page, c.Text, layout)
			got := Obj{"page": res.Page, "diagnostics": res.Diagnostics, "added": orEmpty(res.Added), "removed": orEmpty(res.Removed)}
			tol := 1e-6
			if err := same(c.Expected, Normalize(got), tol, "result"); err != nil {
				t.Error(err)
			}
		})
	}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestGoldenLint(t *testing.T) {
	for _, c := range loadGolden(t).Lint {
		t.Run(c.Name, func(t *testing.T) {
			cfg := LintConfig{Naming: c.Cfg.Naming, Rules: map[string]RuleCfg{}}
			for k, v := range c.Cfg.Rules {
				cfg.Rules[k] = RuleCfg{Enabled: v.Enabled, Severity: v.Severity}
			}
			var got []any
			for _, p := range LintPage(NormalizePage(c.Page), cfg) {
				m := Obj{"id": p.ID, "rule": p.Rule, "severity": p.Severity, "message": p.Message, "nodeIds": p.NodeIDs}
				if p.EdgeIDs != nil {
					m["edgeIds"] = p.EdgeIDs
				}
				if p.Fix != "" {
					m["fix"] = p.Fix
				}
				got = append(got, m)
			}
			if got == nil {
				got = []any{}
			}
			if err := same(c.Expected, Normalize(got), 0, "problems"); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestGoldenExport(t *testing.T) {
	for _, c := range loadGolden(t).Export {
		t.Run(c.Name, func(t *testing.T) {
			got, err := ExportPage(NormalizePage(c.Page), c.Format)
			if err != nil {
				t.Fatal(err)
			}
			if c.Format == "json" {
				var a, b any
				_ = json.Unmarshal([]byte(c.Expected), &a)
				_ = json.Unmarshal([]byte(got), &b)
				if err := same(a, b, 0, "json"); err != nil {
					t.Error(err)
				}
				return
			}
			if got != c.Expected {
				t.Errorf("export differs\n--- want\n%s\n--- got\n%s", c.Expected, got)
			}
		})
	}
}

func TestGoldenSearch(t *testing.T) {
	for _, c := range loadGolden(t).Search {
		t.Run(c.Name, func(t *testing.T) {
			got := SearchCatalog(c.Query, c.Kind, c.Limit)
			if err := same(c.Expected, Normalize(orEmptyHits(got)), 0, "hits"); err != nil {
				t.Error(err)
			}
		})
	}
	for _, c := range loadGolden(t).Resolve {
		t.Run("resolve/"+c.Ref, func(t *testing.T) {
			h, ok := ResolveIcon(c.Ref)
			if !ok {
				if c.Expected != nil {
					t.Errorf("not found, want %v", c.Expected)
				}
				return
			}
			if err := same(c.Expected, Normalize(h), 0, "hit"); err != nil {
				t.Error(err)
			}
		})
	}
}

func orEmptyHits(h []CatalogHit) []CatalogHit {
	if h == nil {
		return []CatalogHit{}
	}
	return h
}

func TestCatalogSize(t *testing.T) {
	if n := CatalogSize(); n < 1000 {
		t.Errorf("catalog has only %d entries", n)
	}
	if !strings.Contains(string(catalogJSON), `"rows"`) {
		t.Error("catalog.json malformed")
	}
}
