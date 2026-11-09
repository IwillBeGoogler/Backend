package http

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/core"
)

// The tools backed by the Go port of the TypeScript core (internal/domain/
// diagram/core): layout, icon catalog, text export, .sdd text import, lint.

// readPage loads a file and picks one page, as plain JSON the core expects.
func (c *mcpCall) readPage(a pageArgs) (application.Obj, core.Obj, error) {
	if err := checkID("fileId", a.FileID); err != nil {
		return nil, nil, err
	}
	f, err := c.h.Diagrams.GetFile(c.ctx, c.uid, a.FileID)
	if err != nil {
		return nil, nil, err
	}
	doc, err := application.DecodeDoc(f.Data)
	if err != nil {
		return nil, nil, err
	}
	page, err := application.PickPage(doc, a.ref())
	if err != nil {
		return nil, nil, &toolError{msg: err.Error()}
	}
	return doc, core.NormalizePage(page), nil
}

func toolApplyLayout(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		pageArgs
		Algorithm string   `json:"algorithm"`
		Direction string   `json:"direction"`
		Gap       *float64 `json:"gap"`
		Seed      *float64 `json:"seed"`
		Only      []string `json:"only"`
		RootID    string   `json:"rootId"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	known := false
	for _, l := range core.Layouts {
		known = known || l == a.Algorithm
	}
	switch {
	case !known:
		return nil, &toolError{msg: "invalid arguments: algorithm must be one of " + strings.Join(core.Layouts, ", ")}
	case a.Direction != "" && a.Direction != "LR" && a.Direction != "TB":
		return nil, &toolError{msg: "invalid arguments: direction must be LR or TB"}
	case !finite(a.Gap, a.Seed) || (a.Gap != nil && (*a.Gap < 0 || *a.Gap > 400)) || (a.Seed != nil && *a.Seed != float64(int64(*a.Seed))):
		return nil, &toolError{msg: "invalid arguments: gap must be 0-400 and seed an integer"}
	case len(a.Only) > 500:
		return nil, &toolError{msg: "invalid arguments: only must have at most 500 ids"}
	}
	for _, id := range append([]string{a.RootID}, a.Only...) {
		if len(id) > 64 {
			return nil, &toolError{msg: "invalid arguments: ids must be 1-64 characters"}
		}
	}
	moved := 0
	version, err := c.edit(a.pageArgs, "apply_layout "+a.Algorithm, func(page application.Obj) error {
		work := core.NormalizePage(page)
		moved = core.ApplyLayout(work, a.Algorithm, core.LayoutOptions{Direction: a.Direction, Gap: a.Gap, Seed: a.Seed, Only: a.Only, RootID: a.RootID})
		page["nodes"], page["edges"] = work["nodes"], work["edges"]
		return nil
	})
	if err != nil {
		return nil, editFailure(err)
	}
	return map[string]any{"moved": moved, "version": version}, nil
}

func toolSearchCatalog(_ *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		Query string `json:"query"`
		Kind  string `json:"kind"`
		Limit *int   `json:"limit"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if len([]rune(a.Query)) > 100 || (a.Kind != "" && a.Kind != "icon" && a.Kind != "group" && a.Kind != "cluster") || (a.Limit != nil && (*a.Limit < 1 || *a.Limit > 100)) {
		return nil, &toolError{msg: "invalid arguments: query is at most 100 characters, kind icon|group|cluster, limit 1-100"}
	}
	limit := 20
	if a.Limit != nil {
		limit = *a.Limit
	}
	results := []any{}
	for _, h := range core.SearchCatalog(a.Query, a.Kind, limit) {
		r := application.Obj{"id": h.ID, "label": h.Label, "kind": h.Kind, "tags": h.Tags[:min(6, len(h.Tags))]}
		if h.ProfileID != "" {
			r["profileId"] = h.ProfileID
		}
		results = append(results, r)
	}
	return map[string]any{"results": results}, nil
}

func toolExport(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		pageArgs
		Format string `json:"format"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	known := false
	for _, f := range core.ExportFormats {
		known = known || f == a.Format
	}
	if !known {
		return nil, &toolError{msg: "invalid arguments: format must be one of " + strings.Join(core.ExportFormats, ", ")}
	}
	_, page, err := c.readPage(a.pageArgs)
	if err != nil {
		return nil, err
	}
	content, err := core.ExportPage(page, a.Format)
	if err != nil {
		return nil, err
	}
	return map[string]any{"format": a.Format, "content": content}, nil
}

var (
	reSddHeader = regexp.MustCompile(`^#!sdd.*\n?`)
	reDiagram   = regexp.MustCompile(`(?m)^diagram .*\n?`)
)

func toolImportDSL(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		pageArgs
		DSL  string `json:"dsl"`
		Mode string `json:"mode"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.DSL == "" || len([]rune(a.DSL)) > 500_000 || (a.Mode != "" && a.Mode != "sync" && a.Mode != "append") {
		return nil, &toolError{msg: "invalid arguments: dsl must be 1-500000 characters and mode sync or append"}
	}
	mode := a.Mode
	if mode == "" {
		mode = "sync"
	}
	report := map[string]any{"errors": []core.Diagnostic{}, "warnings": []core.Diagnostic{}, "added": []string{}, "removed": []string{}}
	version, err := c.edit(a.pageArgs, "import_dsl "+mode, func(page application.Obj) error {
		work := core.NormalizePage(page)
		text := a.DSL
		if mode == "append" {
			body := reSddHeader.ReplaceAllString(a.DSL, "")
			if loc := reDiagram.FindStringIndex(body); loc != nil {
				body = body[:loc[0]] + body[loc[1]:]
			}
			text = core.PrintPage(work, fmt.Sprint(work["name"])) + "\n" + body
		}
		res, _ := core.ApplyDSL(work, text, true)
		errs, warns := []core.Diagnostic{}, []core.Diagnostic{}
		for _, d := range res.Diagnostics {
			if d.Severity == "error" {
				errs = append(errs, d)
			} else {
				warns = append(warns, d)
			}
		}
		report["errors"], report["warnings"] = errs, warns
		report["added"], report["removed"] = nonNil(res.Added), nonNil(res.Removed)
		if len(errs) > 0 {
			return &toolError{msg: fmt.Sprintf("the text has %d error(s); nothing was changed", len(errs)), details: errs}
		}
		page["nodes"], page["edges"] = res.Page["nodes"], res.Page["edges"]
		return nil
	})
	if err != nil {
		return nil, editFailure(err)
	}
	report["version"] = version
	return report, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toolRunLint(c *mcpCall, raw json.RawMessage) (any, error) {
	var a struct {
		pageArgs
		Disable  []string          `json:"disable"`
		Severity map[string]string `json:"severity"`
		Naming   string            `json:"naming"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Disable) > 50 || (a.Naming != "" && a.Naming != "snake_case" && a.Naming != "camelCase" && a.Naming != "any") {
		return nil, &toolError{msg: "invalid arguments: disable has at most 50 ids and naming is snake_case, camelCase or any"}
	}
	for r, s := range a.Severity {
		if s != "error" && s != "warning" && s != "info" {
			return nil, &toolError{msg: fmt.Sprintf("invalid arguments: severity.%s must be error, warning or info", r)}
		}
	}
	_, page, err := c.readPage(a.pageArgs)
	if err != nil {
		return nil, err
	}
	no := false
	cfg := core.LintConfig{Naming: a.Naming, Rules: map[string]core.RuleCfg{}}
	for _, r := range a.Disable {
		cfg.Rules[r] = core.RuleCfg{Enabled: &no}
	}
	for r, s := range a.Severity {
		rc := cfg.Rules[r]
		rc.Severity = s
		cfg.Rules[r] = rc
	}
	counts := map[string]int{"error": 0, "warning": 0, "info": 0}
	problems := []any{}
	for _, p := range core.LintPage(page, cfg) {
		counts[p.Severity]++
		out := application.Obj{"id": p.ID, "rule": p.Rule, "severity": p.Severity, "message": p.Message, "nodeIds": p.NodeIDs}
		if p.EdgeIDs != nil {
			out["edgeIds"] = p.EdgeIDs
		}
		if p.Fix != "" {
			out["fix"] = p.Fix
		}
		problems = append(problems, out)
	}
	return map[string]any{"counts": counts, "problems": problems}, nil
}

func lintRuleIDs() string {
	ids := make([]string, len(core.LintRules))
	for i, r := range core.LintRules {
		ids[i] = r.ID
	}
	return strings.Join(ids, ", ")
}
