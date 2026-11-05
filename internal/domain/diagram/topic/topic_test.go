package topic

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Topic
		ok   bool
	}{
		{"dg/p/p1/events", Topic{Kind: ProjectEvents, ProjectID: "p1"}, true},
		{"dg/p/p1/roster/f1/u1.abcdefgh", Topic{Kind: ProjectRoster, ProjectID: "p1", FileID: "f1", ClientID: "u1.abcdefgh"}, true},
		{"dg/f/f1/events", Topic{Kind: FileEvents, FileID: "f1"}, true},
		{"dg/f/f1/yjs/up/g.abcdefgh", Topic{Kind: YjsUp, FileID: "f1", ClientID: "g.abcdefgh"}, true},
		{"dg/f/f1/yjs/req/c1", Topic{Kind: YjsReq, FileID: "f1", ClientID: "c1"}, true},
		{"dg/f/f1/yjs/down", Topic{Kind: YjsDown, FileID: "f1"}, true},
		{"dg/f/f1/yjs/sync/c1", Topic{Kind: YjsSync, FileID: "f1", ClientID: "c1"}, true},
		{"dg/f/f1/awareness/up/c1", Topic{Kind: AwarenessUp, FileID: "f1", ClientID: "c1"}, true},
		{"dg/f/f1/awareness/out", Topic{Kind: AwarenessOut, FileID: "f1"}, true},
		{"dg/f/f1/presence/c1", Topic{Kind: Presence, FileID: "f1", ClientID: "c1"}, true},
		{"dg/f/f1/rtc/up/c1", Topic{Kind: RTCUp, FileID: "f1", ClientID: "c1"}, true},
		{"dg/f/f1/rtc/c1", Topic{Kind: RTCInbox, FileID: "f1", ClientID: "c1"}, true},
		{"dg/c/c1/ctl", Topic{Kind: Ctl, ClientID: "c1"}, true},
		{"dg/f/f1/roster/+", Topic{}, false}, // wildcards only in filters
		{"dg/f/+/events", Topic{}, false},
		{"dg/f/f1/yjs/up/+", Topic{}, false},
		{"dg/f/f1/yjs/up/#", Topic{}, false},
		{"dg/f/f1/yjs/up", Topic{}, false},
		{"dg/f//events", Topic{}, false},
		{"$SYS/brokers", Topic{}, false},
		{"other/f/f1/events", Topic{}, false},
		{"dg/f/f1/events/extra", Topic{}, false},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseFilter(t *testing.T) {
	ok := map[string]Topic{
		"dg/f/f1/roster/+":      {Kind: FileRoster, FileID: "f1", ClientID: "+"},
		"dg/p/p1/roster/+/+":    {Kind: ProjectRoster, ProjectID: "p1", FileID: "+", ClientID: "+"},
		"dg/f/f1/yjs/down":      {Kind: YjsDown, FileID: "f1"},
		"dg/f/f1/awareness/out": {Kind: AwarenessOut, FileID: "f1"},
	}
	for in, want := range ok {
		if got, good := ParseFilter(in); !good || got != want {
			t.Errorf("ParseFilter(%q) = %+v, %v; want %+v", in, got, good, want)
		}
	}
	for _, in := range []string{"dg/+/f1/events", "dg/f/+/roster/+", "dg/f/+/yjs/down", "dg/#", "#", "dg/f/f1/yjs/up/+", "dg/p/+/events", "dg/f/f1/#", "dg/c/+/ctl", "$share/dg/dg/f/f1/yjs/down"} {
		if got, good := ParseFilter(in); good {
			t.Errorf("ParseFilter(%q) accepted: %+v", in, got)
		}
	}
}

func TestMatch(t *testing.T) {
	for _, c := range []struct {
		f, t string
		want bool
	}{
		{"dg/f/+/yjs/up/+", "dg/f/f1/yjs/up/c1", true},
		{"$share/dg/dg/f/+/yjs/up/+", "dg/f/f1/yjs/up/c1", true},
		{"dg/f/+/yjs/up/+", "dg/f/f1/yjs/down", false},
		{"dg/f/+/yjs/up/+", "dg/f/f1/yjs/up/c1/x", false},
		{"dg/#", "dg/f/f1/events", true},
		{"dg/f/f1/roster/+", "dg/f/f1/roster/c1", true},
		{"dg/f/f1/roster/+", "dg/f/f2/roster/c1", false},
	} {
		if got := Match(c.f, c.t); got != c.want {
			t.Errorf("Match(%q, %q) = %v", c.f, c.t, got)
		}
	}
}

func TestClientID(t *testing.T) {
	if got := ClientID(42, "abcdefgh"); got != "u42.abcdefgh" {
		t.Fatal(got)
	}
	if got := ClientID(0, "abcdefgh"); got != "g.abcdefgh" {
		t.Fatal(got)
	}
	for in, want := range map[string]int{"u42.abcdefgh": 42, "g.abcdefgh": 0} {
		if u, ok := ParseClientID(in); !ok || u != want {
			t.Errorf("ParseClientID(%q) = %d, %v", in, u, ok)
		}
	}
	for _, in := range []string{"", "u42", "u42.", "u42.short", "u0.abcdefgh", "u-1.abcdefgh", "u042.abcdefgh", "u+4.abcdefgh", "x1.abcdefgh", "g.bad/char1", "u42.abcdefgh.more", "srv.abcdefgh"} {
		if _, ok := ParseClientID(in); ok {
			t.Errorf("ParseClientID(%q) accepted", in)
		}
	}
}
