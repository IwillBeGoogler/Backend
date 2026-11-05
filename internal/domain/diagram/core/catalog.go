package core

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed catalog.json
var catalogJSON []byte

// CatalogHit is one entry of the icon catalog.
type CatalogHit struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Kind       string   `json:"kind"` // icon, group or cluster
	Aliases    []string `json:"aliases"`
	Tags       []string `json:"tags"`
	ProfileID  string   `json:"profileId,omitempty"`
	IconID     string   `json:"iconId,omitempty"`
	Topologies []string `json:"topologies,omitempty"`
}

type entry struct {
	CatalogHit
	hID, hLabel string
	hAliases    []string
}

var (
	catalogOnce sync.Once
	entries     []entry
	byID        map[string]int
)

func loadCatalog() {
	var raw struct {
		Rows     [][]json.RawMessage `json:"rows"`
		Clusters [][]json.RawMessage `json:"clusters"`
	}
	if err := json.Unmarshal(catalogJSON, &raw); err != nil {
		panic("core: catalog.json: " + err.Error())
	}
	lower := func(ss []string) []string {
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = strings.ToLower(s)
		}
		return out
	}
	for _, r := range raw.Rows {
		var id, label string
		var aliases, tags []string
		var group int
		_ = json.Unmarshal(r[0], &id)
		_ = json.Unmarshal(r[1], &label)
		_ = json.Unmarshal(r[2], &aliases)
		_ = json.Unmarshal(r[3], &tags)
		_ = json.Unmarshal(r[4], &group)
		kind := "icon"
		if group != 0 {
			kind = "group"
		}
		entries = append(entries, entry{CatalogHit: CatalogHit{ID: id, Label: label, Kind: kind, Aliases: aliases, Tags: tags},
			hID: id, hLabel: strings.ToLower(label), hAliases: lower(aliases)})
	}
	for _, r := range raw.Clusters {
		var pid, label, tech, icon string
		var topo []string
		_ = json.Unmarshal(r[0], &pid)
		_ = json.Unmarshal(r[1], &label)
		_ = json.Unmarshal(r[2], &tech)
		_ = json.Unmarshal(r[3], &icon)
		_ = json.Unmarshal(r[4], &topo)
		aliases := append([]string{tech + " cluster", pid + " cluster", tech + " ha"}, topo...)
		entries = append(entries, entry{CatalogHit: CatalogHit{ID: "cluster-" + pid, Label: label, Kind: "cluster", Aliases: aliases,
			Tags: []string{"cluster", "ha", strings.ToLower(tech)}, ProfileID: pid, IconID: icon, Topologies: topo},
			hID: pid, hLabel: strings.ToLower(label), hAliases: lower(aliases)})
	}
	byID = make(map[string]int, len(entries))
	for i := range entries { // the last entry wins, like new Map(entries)
		byID[entries[i].ID] = i
	}
}

func catalog() []entry { catalogOnce.Do(loadCatalog); return entries }

var (
	wsUnderscore = regexp.MustCompile(`[\s_]+`)
	nonWord      = regexp.MustCompile(`\W+`)
)

func normQ(s string) string {
	return wsUnderscore.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), " ")
}
func slugQ(s string) string { return strings.ReplaceAll(normQ(s), " ", "-") }

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func hasWord(s, t string) bool { return contains(nonWord.Split(s, -1), t) }

func scoreEntry(e *entry, q string, tokens []string) float64 {
	qs := slugQ(q)
	best := 0.0
	switch {
	case e.hID == qs || e.ID == qs:
		best = 100
	case e.hLabel == q:
		best = 95
	case contains(e.hAliases, q):
		best = 80
	case strings.HasPrefix(e.hID, qs):
		best = 60
	case strings.HasPrefix(e.hLabel, q):
		best = 55
	}
	if best > 0 {
		return best
	}
	total := 0.0
	for _, t := range tokens {
		s := 0.0
		switch {
		case contains(strings.Split(e.hID, "-"), t):
			s = 30
		case hasWord(e.hLabel, t):
			s = 28
		case anyAlias(e.hAliases, func(a string) bool { return a == t || hasWord(a, t) }):
			s = 22
		case strings.Contains(e.hID, t) || strings.Contains(e.hLabel, t):
			s = 14
		case anyAlias(e.hAliases, func(a string) bool { return strings.Contains(a, t) }):
			s = 10
		case contains(e.Tags, t):
			s = 6
		default:
			return 0
		}
		total += s
	}
	n := len(tokens)
	if n < 1 {
		n = 1
	}
	return total / float64(n)
}

func anyAlias(as []string, f func(string) bool) bool {
	for _, a := range as {
		if f(a) {
			return true
		}
	}
	return false
}

// SearchCatalog is the ranked search over icons, groups and cluster profiles
// (core/catalog.ts searchCatalog). kind "" matches every kind; limit is
// clamped to 1..200 (0 means 20).
func SearchCatalog(query, kind string, limit int) []CatalogHit {
	if limit == 0 {
		limit = 20
	}
	limit = max(1, min(200, limit))
	es := catalog()
	q := normQ(query)
	var out []CatalogHit
	if q == "" {
		for i := range es {
			if (kind == "" || es[i].Kind == kind) && len(out) < limit {
				out = append(out, es[i].CatalogHit)
			}
		}
		return out
	}
	tokens := strings.Fields(q)
	type scored struct {
		i int
		s float64
	}
	var hits []scored
	for i := range es {
		if kind != "" && es[i].Kind != kind {
			continue
		}
		if s := scoreEntry(&es[i], q, tokens); s > 0 {
			hits = append(hits, scored{i, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].s > hits[b].s })
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		out = append(out, es[h.i].CatalogHit)
	}
	return out
}

// ResolveIcon maps a catalog id (or a human name or alias) to its entry.
func ResolveIcon(ref string) (CatalogHit, bool) {
	es := catalog()
	if i, ok := byID[ref]; ok {
		return es[i].CatalogHit, true
	}
	if i, ok := byID[slugQ(ref)]; ok {
		return es[i].CatalogHit, true
	}
	q := normQ(ref)
	for i := range es {
		if es[i].Kind != "cluster" && (es[i].hLabel == q || contains(es[i].hAliases, q)) {
			return es[i].CatalogHit, true
		}
	}
	return CatalogHit{}, false
}

// CatalogSize is the number of catalog entries.
func CatalogSize() int { return len(catalog()) }
