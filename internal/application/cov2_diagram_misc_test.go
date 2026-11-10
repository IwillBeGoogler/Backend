package application

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func TestCreateTokenValidation(t *testing.T) {
	d := sweepEnv(0)
	pid, _ := newPrj(d, alice, "Tok")
	many := make([]string, 101)
	for i := range many {
		many[i] = "p" + itoa(i)
	}
	cases := []struct {
		name string
		in   NewAccessToken
		ok   bool
	}{
		{"blank name", NewAccessToken{Name: " ", Scopes: diagram.Scopes}, false},
		{"long name", NewAccessToken{Name: strings.Repeat("n", 101), Scopes: diagram.Scopes}, false},
		{"no scopes", NewAccessToken{Name: "n"}, false},
		{"unknown scope", NewAccessToken{Name: "n", Scopes: []string{"nope"}}, false},
		{"negative expiry", NewAccessToken{Name: "n", Scopes: diagram.Scopes, ExpiresInDays: -1}, false},
		{"far expiry", NewAccessToken{Name: "n", Scopes: diagram.Scopes, ExpiresInDays: 99999}, false},
		{"empty project list", NewAccessToken{Name: "n", Scopes: diagram.Scopes, ProjectIDs: []string{}}, false},
		{"too many projects", NewAccessToken{Name: "n", Scopes: diagram.Scopes, ProjectIDs: many}, false},
		{"duplicate projects", NewAccessToken{Name: "n", Scopes: diagram.Scopes, ProjectIDs: []string{pid, pid}}, true},
	}
	for _, c := range cases {
		_, _, err := d.CreateToken(dctx, alice, c.in)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	d.Tokens = nil
	if _, _, err := d.CreateToken(dctx, alice, NewAccessToken{Name: "n", Scopes: diagram.Scopes}); err == nil {
		t.Error("tokens off must fail")
	}
	if err := d.RevokeToken(dctx, alice, "x"); err == nil {
		t.Error("revoke with tokens off must fail")
	}
}

func TestDiagramFeaturesOff(t *testing.T) {
	d := sweepEnv(0)
	pid, fid := newPrj(d, alice, "Off")
	d.Publish, d.Comments, d.Notices = nil, nil, nil
	sink(d.PublishFile(dctx, alice, fid, PublishRequest{Visibility: diagram.VisPublic, Title: "t"}))
	sink(d.UnpublishFile(dctx, alice, fid))
	sink(d.PublicDiagrams(dctx, "", "", dpaging()))
	sink(d.FileComments(dctx, alice, fid))
	sink(d.AddComment(dctx, alice, fid, NewComment{Body: "x"}))
	sink(d.UpdateComment(dctx, alice, "c", CommentPatch{}))
	sink(d.DeleteComment(dctx, alice, "c"))
	sink(d.MarkCommentsRead(dctx, alice, fid))
	sink(d.Notifications(dctx, alice, false, 5))
	sink(d.ReadNotifications(dctx, alice, nil, true))
	_ = pid
}

func TestCommentParentTooLong(t *testing.T) {
	d := sweepEnv(0)
	_, fid := newPrj(d, alice, "C")
	_, err := d.AddComment(dctx, alice, fid, NewComment{Body: "x", ParentID: strings.Repeat("p", 65), Anchor: json.RawMessage(`{"type":"point","x":1,"y":1}`)})
	if err == nil {
		t.Error("overlong parent id accepted")
	}
}

func TestTextImportErrors(t *testing.T) {
	d := sweepEnv(0)
	pid, _ := newPrj(d, alice, "Txt")
	cases := []struct {
		name string
		body string
	}{
		{"not an sdd file", "plain words"},
		{"text with errors", "#!sdd\nthis is ??? not valid\n!!!\n%%%\n@@@\n"},
		{"dotted name", "#!sdd\n"},
	}
	for _, c := range cases {
		sink(d.ImportAny(dctx, alice, "x.sdd", []byte(c.body)))
		sink(d.ImportAny(dctx, alice, ".", []byte(c.body)))
		sink(d.ImportIntoProject(dctx, alice, pid, nil, "", []byte(c.body)))
	}
}

func TestUndoMCPChangeEdges(t *testing.T) {
	d := sweepEnv(0)
	_, fid := newPrj(d, alice, "U")
	if _, _, err := d.UndoMCPChange(dctx, alice, fid); err == nil {
		t.Error("undo with a single version must fail")
	}
	if _, _, err := d.UndoMCPChange(dctx, bob, fid); err == nil {
		t.Error("stranger undo must fail")
	}
	sink(d.EditFile(dctx, alice, fid, "x", func(Obj) error { return errBoom }))
	sink(d.EditFile(dctx, alice, "nope", "x", func(Obj) error { return nil }))
}
