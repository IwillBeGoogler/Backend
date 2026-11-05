package diagram

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseMentions(t *testing.T) {
	body := "Hi " + FormatMention("Bob B", 2) + " and " + FormatMention("Cara", 3) + ", again " + FormatMention("Bob B", 2) +
		". @plain @[x](user:) @[](user:7) email a@b.co @[Word](user:abc) @[Zero](user:0) @[Big](user:99999999999999999999)"
	if got := ParseMentions(body); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Errorf("mentions = %v", got)
	}
	if got := ParseMentions("nothing"); got != nil {
		t.Errorf("none = %v", got)
	}
	if got := FormatMention("A ] B", 1); got != "@[A   B](user:1)" {
		t.Errorf("format = %q", got) // the display name cannot break the token
	}
	if got := FormatMention("a(b)\nc", 1); got != "@[a b  c](user:1)" {
		t.Errorf("format = %q", got)
	}
	long := FormatMention(strings.Repeat("é", 150), 5)
	if ParseMentions(long)[0] != 5 {
		t.Errorf("a long name must still parse: %q", long)
	}
}

func TestParseAnchor(t *testing.T) {
	ok := map[string]string{
		`{"type":"point","x":10,"y":20}`:                                            `{"type":"point","x":10,"y":20}`,
		`{"type":"point","x":1.5,"y":-2,"junk":true}`:                               `{"type":"point","x":1.5,"y":-2}`,
		`{"type":"node","nodeId":"a","dx":4,"dy":-6}`:                               `{"dx":4,"dy":-6,"nodeId":"a","type":"node"}`,
		`{"type":"node","nodeId":"` + strings.Repeat("n", 100) + `","dx":0,"dy":0}`: "",
	}
	for in, want := range ok {
		got, err := ParseAnchor(json.RawMessage(in))
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if want != "" && string(got) != want && !jsonEqual(got, want) {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
	for _, in := range []string{`{"type":"point","x":"a","y":1}`, `{"type":"point","x":1}`, `{"type":"node","nodeId":"","dx":1,"dy":1}`,
		`{"type":"node","nodeId":"` + strings.Repeat("n", 101) + `","dx":1,"dy":1}`, `{"type":"node","nodeId":"a","dx":1}`,
		`{"type":"line"}`, `{}`, `[]`, `5`, `"x"`} {
		if _, err := ParseAnchor(json.RawMessage(in)); err == nil {
			t.Errorf("%s accepted", in)
		}
	}
}

func jsonEqual(a json.RawMessage, b string) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal([]byte(b), &y) == nil && reflect.DeepEqual(x, y)
}

func TestCheckCommentBody(t *testing.T) {
	for body, ok := range map[string]bool{"": false, " ": true, "x": true, strings.Repeat("x", 4000): true, strings.Repeat("x", 4001): false,
		strings.Repeat("é", 4000): true, strings.Repeat("é", 4001): false} {
		if err := CheckCommentBody(body); (err == nil) != ok {
			t.Errorf("%d chars: %v", len([]rune(body)), err)
		}
	}
}

func TestUnread(t *testing.T) {
	t0 := time.Unix(100, 0)
	cs := []Comment{{AuthorID: 1, CreatedAt: t0.Add(time.Second)}, {AuthorID: 2, CreatedAt: t0.Add(time.Second)},
		{AuthorID: 2, CreatedAt: t0}, {AuthorID: 0, CreatedAt: t0.Add(2 * time.Second)}}
	if got := Unread(cs, 1, t0); got != 2 { // not mine, after readAt; a deleted author's counts
		t.Errorf("unread = %d", got)
	}
	if got := Unread(cs, 2, time.Time{}); got != 2 {
		t.Errorf("never read = %d", got)
	}
}

func TestCommenterRole(t *testing.T) {
	if !(RoleViewer.Rank() < RoleCommenter.Rank() && RoleCommenter.Rank() < RoleEditor.Rank() && RoleEditor.Rank() < RoleOwner.Rank()) {
		t.Fatal("ranks must run viewer < commenter < editor < owner")
	}
	if !RoleCommenter.Allows(RoleViewer) || !RoleCommenter.Allows(RoleCommenter) || RoleCommenter.Allows(RoleEditor) || RoleViewer.Allows(RoleCommenter) {
		t.Error("commenter reads and comments, never edits")
	}
	if !RoleCommenter.OverrideRole() || Role("owner").OverrideRole() || Role("").OverrideRole() {
		t.Error("override roles are viewer, commenter, editor")
	}
}
