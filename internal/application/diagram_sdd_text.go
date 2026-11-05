package application

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/core"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/sddfile"
)

// The text flavour of .sdd ("#!sdd", diagrams as code): export of the active
// page and import of a text as a new single-page file. The language itself is
// in internal/domain/diagram/core.

const textLabel = "Imported from text .sdd"

var reTextTitle = regexp.MustCompile(`(?m)^diagram\s+"((?:[^"\\]|\\.)*)"`)

// SDDTextMime is the type of a text .sdd download.
const SDDTextMime = "text/vnd.sdd; charset=utf-8"

// ExportFileText renders the active page of a file as "#!sdd" text. The
// caller needs the viewer role; a share token works for a non-member.
func (d *Diagrams) ExportFileText(ctx context.Context, actor int, token, id string) (SDDExport, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ExportFileText")
	defer span.End()
	_, f, err := d.fileAccessVia(ctx, actor, token, id, diagram.RoleViewer)
	if err != nil {
		return SDDExport{}, err
	}
	doc, err := DecodeDoc(f.Data)
	if err != nil {
		return SDDExport{}, err
	}
	pages := Pages(doc)
	if len(pages) == 0 {
		return SDDExport{}, diagram.Validation("the file has no pages")
	}
	page := pages[0]
	for _, p := range pages {
		if p["id"] == doc["activePageId"] {
			page = p
			break
		}
	}
	np := core.NormalizePage(page)
	text := core.PrintPage(np, fmt.Sprint(np["name"]))
	return SDDExport{Name: f.Name, Version: f.Version, Data: []byte(text)}, nil
}

// textFile turns a text .sdd into a one-page document. A text with syntax
// errors is refused with the first lines (reason "schema").
func (d *Diagrams) textFile(name string, b []byte) (sddfile.FileResult, error) {
	text, ok := sddfile.DecodeText(b)
	if !ok {
		return sddfile.FileResult{}, &SDDInvalid{Reason: sddfile.CodeUnsupported, Msg: `Not a .sdd file: expected a ZIP archive or text starting with "#!sdd"`}
	}
	built, _ := core.ApplyDSL(nil, text, true)
	var errs []core.Diagnostic
	var warns []string
	for _, dg := range built.Diagnostics {
		if dg.Severity == "error" {
			errs = append(errs, dg)
		} else {
			warns = append(warns, fmt.Sprintf("line %d: %s", dg.Line, dg.Message))
		}
	}
	if len(errs) > 0 {
		var shown []string
		for _, e := range errs[:min(3, len(errs))] {
			shown = append(shown, fmt.Sprintf("line %d: %s", e.Line, e.Message))
		}
		msg := strings.Join(shown, "; ")
		if len(errs) > 3 {
			msg += fmt.Sprintf(" (+%d more)", len(errs)-3)
		}
		return sddfile.FileResult{}, &SDDInvalid{Reason: sddfile.CodeSchema, Entry: "text", Msg: msg}
	}
	title := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	if title == "" || title == "." {
		title = "Imported text"
	}
	if m := reTextTitle.FindStringSubmatch(text); m != nil {
		title = m[1]
	}
	now := d.clock()
	var doc map[string]any
	_ = json.Unmarshal(diagram.EmptyData(title, now), &doc)
	page := doc["pages"].([]any)[0].(map[string]any)
	page["name"] = title
	page["nodes"], page["edges"] = built.Page["nodes"], built.Page["edges"]
	data, err := json.Marshal(doc)
	if err != nil {
		return sddfile.FileResult{}, err
	}
	return sddfile.FileResult{Doc: sddfile.Doc{Name: title, Data: data}, Warnings: warns}, nil
}

// readOne reads a single-file .sdd of any flavour: a file container or text.
func (d *Diagrams) readOne(s sddfile.Sniffed, name string, b []byte) (sddfile.FileResult, string, error) {
	if s.Kind == sddfile.KindText {
		r, err := d.textFile(name, b)
		return r, textLabel, err
	}
	r, err := sddfile.ReadFile(b, d.sddLimits())
	return r, "Imported from .sdd", sddInvalid(err)
}
