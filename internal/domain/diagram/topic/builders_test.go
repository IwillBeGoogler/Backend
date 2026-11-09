package topic

import "testing"

func TestBuildersRoundTrip(t *testing.T) {
	cases := []struct {
		topic string
		kind  Kind
	}{
		{ProjectEventsTopic("p1"), ProjectEvents},
		{ProjectRosterTopic("p1", "f1", "c1"), ProjectRoster},
		{FileEventsTopic("f1"), FileEvents},
		{FileRosterTopic("f1", "c1"), FileRoster},
		{YjsDownTopic("f1"), YjsDown},
		{YjsSyncTopic("f1", "c1"), YjsSync},
		{AwarenessOutTopic("f1"), AwarenessOut},
		{RTCInboxTopic("f1", "c1"), RTCInbox},
		{YjsUpTopic("f1", "c1"), YjsUp},
		{YjsReqTopic("f1", "c1"), YjsReq},
		{AwarenessUpTopic("f1", "c1"), AwarenessUp},
		{PresenceTopic("f1", "c1"), Presence},
		{RTCUpTopic("f1", "c1"), RTCUp},
		{CtlTopic("c1"), Ctl},
	}
	for _, c := range cases {
		got, ok := Parse(c.topic)
		if !ok || got.Kind != c.kind {
			t.Errorf("%s: %+v %v", c.topic, got, ok)
		}
	}
}

func TestShared(t *testing.T) {
	if got := Shared("dg/f/+/yjs/up/+"); got != "$share/dg/dg/f/+/yjs/up/+" {
		t.Fatal(got)
	}
	if !Match(Shared("dg/f/+/yjs/up/+"), "dg/f/x/yjs/up/y") {
		t.Fatal("shared filter no match")
	}
}

func TestParseProjectBadID(t *testing.T) {
	if _, ok := Parse("dg/p/bad id/events"); ok {
		t.Fatal("bad project id accepted")
	}
}

func TestParseRosterBadIDs(t *testing.T) {
	if _, ok := Parse("dg/p/p1/roster/+/c1"); ok {
		t.Fatal("wildcard in concrete roster topic accepted")
	}
	if _, ok := ParseFilter("dg/p/p1/roster/+/+"); !ok {
		t.Fatal("wildcard filter rejected")
	}
	if _, ok := ParseFilter("dg/p/p1/roster/bad id/c1"); ok {
		t.Fatal("bad file id accepted")
	}
}
