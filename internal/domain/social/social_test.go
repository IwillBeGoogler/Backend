package social

import "testing"

func TestDiagram(t *testing.T) {
	yes := []NotificationType{NotifyDiagramMention, NotifyDiagramReply, NotifyDiagramComment, NotifyDiagramClap}
	for _, n := range yes {
		if !n.Diagram() {
			t.Errorf("%s", n)
		}
	}
	no := []NotificationType{NotifyComment, NotifyReply, NotifyLike, NotifyCommentLike, NotifyFollow, NotifyNewPost, NotifyMention, ""}
	for _, n := range no {
		if n.Diagram() {
			t.Errorf("%s", n)
		}
	}
}

func TestHideKindValid(t *testing.T) {
	for _, k := range []HideKind{HidePost, HideAuthor, HideTag} {
		if !k.Valid() {
			t.Errorf("%s", k)
		}
	}
	if HideKind("x").Valid() {
		t.Fatal("bad accepted")
	}
}

func TestPaginated(t *testing.T) {
	items, meta := NotificationPage{Items: []Notification{{ID: 1}}, Total: 1, Unread: 4, Page: 1, Limit: 5, NextCursor: "c"}.Paginated()
	if len(items.([]Notification)) != 1 {
		t.Fatal("items")
	}
	if m, ok := meta.(NotificationMeta); !ok || m.Unread != 4 {
		t.Fatalf("meta %+v", meta)
	}
	items, meta = HidePage{Items: []Hide{{Target: "x"}}, Total: 1, Page: 1, Limit: 5}.Paginated()
	if len(items.([]Hide)) != 1 || meta == nil {
		t.Fatal("hide page")
	}
}
