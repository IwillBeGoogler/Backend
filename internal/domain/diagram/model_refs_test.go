package diagram

import (
	"encoding/json"
	"testing"
)

func TestCheckModelRefs(t *testing.T) {
	node := func(typ, data string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"data": json.RawMessage(data)}
	}
	cases := []struct {
		typ, data string
		bad       bool
	}{
		{"class", `{"label":"A","modelId":"m_1"}`, false},
		{"class", `{"label":"A"}`, false},
		{"class", `{"label":"A","modelId":5}`, true},
		{"object", `{"label":"o","modelId":"m_1","slots":[]}`, false},
		{"object", `{"label":"o","modelId":{},"slots":[]}`, true},
		{"sequence", `{"participants":[{"id":"a","name":"A","kind":"participant","modelId":"m_1"},{"id":"b","name":"B","kind":"actor"}]}`, false},
		{"sequence", `{"participants":[{"id":"a","name":"A","kind":"participant","modelId":7}]}`, true},
	}
	for _, c := range cases {
		got := checkModelRefs(c.typ, node(c.typ, c.data))
		if (got != "") != c.bad {
			t.Errorf("%s %s: got %q, bad=%v", c.typ, c.data, got, c.bad)
		}
	}
}
