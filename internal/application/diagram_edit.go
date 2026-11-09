package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// Programmatic edits of a file (the MCP tools): read, change the JSON
// document, save through the same path as the REST API (validation,
// optimistic version check, a labelled snapshot, realtime and webhook
// events).

// MCPSuffix marks snapshots made through MCP: "<name> via MCP: <what>".
const MCPSuffix = " via MCP: "

// ErrNothingToUndo is an undo with no MCP change to go back from.
var ErrNothingToUndo = errors.New("the latest change was not made through MCP (or is already undone); use the version history instead")

func (d *Diagrams) actorName(ctx context.Context, actor int) string {
	if n := d.people(ctx, actor)[actor].Name; n != "" {
		return n
	}
	return "MCP"
}

// MCPLabel is the snapshot label of an MCP change: "<user> via MCP: <what>".
func (d *Diagrams) MCPLabel(ctx context.Context, actor int, what string) string {
	return d.actorName(ctx, actor) + MCPSuffix + what
}

// EditFile applies fn to the file's document and saves it. A concurrent
// save is retried (up to 5 times). label says what changed; the snapshot
// is labelled "<user> via MCP: <label>". It returns the new version. fn
// errors are returned as they are, and nothing is saved.
func (d *Diagrams) EditFile(ctx context.Context, actor int, fileID, label string, fn func(doc Obj) error) (int, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.EditFile")
	defer span.End()
	for attempt := 0; ; attempt++ {
		f, err := d.GetFile(ctx, actor, fileID)
		if err != nil {
			return 0, err
		}
		if !f.Role.Allows(diagram.RoleEditor) {
			return 0, diagram.NeedRole(diagram.RoleEditor)
		}
		doc, err := DecodeDoc(f.Data)
		if err != nil {
			return 0, err
		}
		if err := fn(doc); err != nil {
			return 0, err
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return 0, err
		}
		lbl := d.MCPLabel(ctx, actor, label)
		saved, err := d.SaveFileLabeled(ctx, actor, fileID, nil, raw, fmt.Sprintf(`"%d"`, f.Version), &lbl)
		var de *diagram.Error
		if errors.As(err, &de) && errors.Is(err, diagram.ErrPrecondition) && attempt < 4 {
			continue // someone saved in between: read again, apply again
		}
		return saved.Version, err
	}
}

// UndoMCPChange restores the file to the version before the most recent
// MCP change, if that change is the latest meaningful one: later versions
// must be unlabeled autosaves, not somebody's own edits or named versions.
// It returns the version restored from and the new version.
func (d *Diagrams) UndoMCPChange(ctx context.Context, actor int, fileID string) (restoredFrom, version int, err error) {
	ctx, span := tracer.Start(ctx, "Diagrams.UndoMCPChange")
	defer span.End()
	head, err := d.GetFile(ctx, actor, fileID)
	if err != nil {
		return 0, 0, err
	}
	if !head.Role.Allows(diagram.RoleEditor) {
		return 0, 0, diagram.NeedRole(diagram.RoleEditor)
	}
	versions, err := d.Repo.Versions(ctx, fileID) // newest first
	if err != nil {
		return 0, 0, err
	}
	at := -1
	for i, v := range versions {
		if v.Label != nil && strings.Contains(*v.Label, MCPSuffix) {
			at = i
			break
		}
	}
	if at < 0 {
		return 0, 0, ErrNothingToUndo
	}
	for _, v := range versions[:at] {
		if v.Label != nil && *v.Label != "" || v.Named {
			return 0, 0, ErrNothingToUndo
		}
	}
	cur := versions[at]
	if cur.CreatedBy != actor {
		return 0, 0, &diagram.Error{Kind: domain.ErrForbidden, Code: "FORBIDDEN", Msg: "The latest MCP change was made by someone else; only its author can undo it"}
	}
	if strings.Contains(*cur.Label, ": undo of ") {
		return 0, 0, ErrNothingToUndo
	}
	var prev *diagram.Version
	for i := at + 1; i < len(versions); i++ {
		prev = &versions[i]
		break
	}
	if prev == nil {
		return 0, 0, errors.New("no earlier version to go back to")
	}
	old, err := d.Repo.GetVersion(ctx, fileID, prev.Version)
	if err != nil {
		return 0, 0, err
	}
	what := (*cur.Label)[strings.Index(*cur.Label, MCPSuffix)+len(MCPSuffix):]
	lbl := d.MCPLabel(ctx, actor, "undo of "+what)
	saved, err := d.SaveFileLabeled(ctx, actor, fileID, nil, old.Data, fmt.Sprintf(`"%d"`, head.Version), &lbl)
	return prev.Version, saved.Version, err
}
