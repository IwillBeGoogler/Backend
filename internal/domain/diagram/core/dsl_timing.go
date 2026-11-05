package core

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// timing diagram blocks of the ".sdd" text (core/dsl/parseTiming.ts, types.ts).

type DslTmLife struct {
	ID, Name, Kind string
	States         []string
	Period         float64
}
type DslTmChange struct {
	Life, State string
	T           float64
	Line        int
}
type DslTmMsg struct {
	From, To, Text string
	T, T2          float64
	HasT2          bool
	Line           int
}
type DslTmCon struct {
	Life, Text string
	T1, T2     float64
	Line       int
}
type DslTiming struct {
	ID, Title, Unit string
	End             float64
	HasEnd          bool
	Lifelines       []*DslTmLife
	Changes         []*DslTmChange
	Messages        []*DslTmMsg
	Constraints     []*DslTmCon
	Line            int
}

var (
	tmKinds    = map[string]bool{"robust": true, "concise": true, "binary": true, "clock": true}
	reTmNum    = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	reTmMsgT   = regexp.MustCompile(`^@(-?\d+(?:\.\d+)?)(?:\.\.(-?\d+(?:\.\d+)?))?$`)
	reTmRange  = regexp.MustCompile(`^-?\d+(?:\.\d+)?\.\.-?\d+(?:\.\d+)?$`)
	tmBinLevel = []string{"high", "low"}
)

func tmNum(s string) (float64, bool) {
	if !reTmNum.MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// tmRound mirrors Math.round(t * 1000) / 1000.
func tmRound(t float64) float64 { return math.Floor(t*1000+0.5) / 1000 }

func (t *DslTiming) life(id string) *DslTmLife {
	for _, l := range t.Lifelines {
		if l.ID == id {
			return l
		}
	}
	return nil
}

func tmHas(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// parseTimingLine parses one line of a timing block; true when the block closed.
func parseTimingLine(tm *DslTiming, src string, ln int, diag func(l, c int, m, sev string)) bool {
	s := strings.TrimSpace(src)
	if s == "}" {
		return true
	}
	derr := func(col int, m string) { diag(ln, col, m, "error") }
	toks, lerr := tokenize(s)
	if lerr != nil {
		derr(lerr.col, lerr.msg)
		return false
	}
	if len(toks) == 0 {
		return false
	}
	first := toks[0]
	kw := ""
	if first.k == "word" {
		kw = first.v
	}
	at := func(i int) *tok {
		if i < len(toks) {
			return &toks[i]
		}
		return nil
	}
	if kw == "unit" && len(toks) > 1 && toks[1].k != "sym" {
		tm.Unit = toks[1].v
		return false
	}
	if kw == "end" && len(toks) > 1 {
		v, ok := tmNum(toks[1].v)
		if !ok || v <= 0 {
			derr(toks[1].col, "end is a positive number, e.g. end 400")
		} else {
			tm.End, tm.HasEnd = v, true
		}
		return false
	}
	if tmKinds[kw] {
		idTok := at(1)
		if idTok == nil || idTok.k == "sym" {
			derr(first.col, kw+` needs an id, e.g. `+kw+` web "Web"`)
			return false
		}
		i := 2
		name := idTok.v
		if t := at(i); t != nil && t.k == "str" {
			name = t.v
			i++
		}
		var states []string
		period := 0.0
		for i < len(toks) {
			t := toks[i]
			if t.k == "word" && t.v == "states" {
				i++
				for i < len(toks) && !(toks[i].k == "word" && i+1 < len(toks) && toks[i+1].v == "=") {
					if toks[i].k != "sym" {
						states = append(states, toks[i].v)
					}
					i++
				}
				continue
			}
			if t.k == "word" && t.v == "period" && i+1 < len(toks) && toks[i+1].v == "=" && i+2 < len(toks) {
				v, ok := tmNum(toks[i+2].v)
				if !ok || v <= 0 {
					derr(t.col, "period is a positive number")
				} else {
					period = v
				}
				i += 3
				continue
			}
			derr(t.col, `unexpected "`+t.v+`" in the `+kw+` declaration`)
			i++
		}
		if tm.life(idTok.v) != nil {
			derr(idTok.col, `duplicate lifeline "`+idTok.v+`"`)
			return false
		}
		if period != 0 && kw != "clock" {
			diag(ln, 1, "period belongs to a clock", "warning")
		}
		if len(states) > 0 && (kw == "binary" || kw == "clock") {
			diag(ln, 1, `a `+kw+` lifeline has no state list`, "warning")
		}
		l := &DslTmLife{ID: idTok.v, Name: name, Kind: kw}
		switch kw {
		case "binary":
			l.States = append([]string{}, tmBinLevel...)
		case "clock":
			l.States = []string{}
			l.Period = period
		default:
			l.States = states
			if l.States == nil {
				l.States = []string{}
			}
		}
		tm.Lifelines = append(tm.Lifelines, l)
		return false
	}
	// lifeline @t State
	if len(toks) >= 3 && toks[1].k == "word" && strings.HasPrefix(toks[1].v, "@") {
		t, okT := tmNum(toks[1].v[1:])
		l := tm.life(first.v)
		switch {
		case l == nil:
			derr(first.col, `unknown lifeline "`+first.v+`"`)
		case !okT || t < 0:
			derr(toks[1].col, "expected a time like @100")
		case l.Kind == "clock":
			derr(first.col, "a clock has no state changes")
		default:
			state := toks[2].v
			if l.Kind == "binary" {
				if !tmHas(tmBinLevel, state) {
					diag(ln, toks[2].col, "a binary lifeline is high or low", "warning")
					state = "low"
				}
			} else if !tmHas(l.States, state) {
				l.States = append(l.States, state)
			}
			var same *DslTmChange
			for _, c := range tm.Changes {
				if c.Life == l.ID && c.T == t {
					same = c
					break
				}
			}
			if same != nil {
				same.State = state
			} else {
				tm.Changes = append(tm.Changes, &DslTmChange{Life: l.ID, T: tmRound(t), State: state, Line: ln})
			}
		}
		return false
	}
	// a -> b @t[..t2] "text"
	if len(toks) >= 4 && toks[1].v == "->" && toks[1].k == "word" {
		a, b := tm.life(first.v), tm.life(toks[2].v)
		m := reTmMsgT.FindStringSubmatch(toks[3].v)
		switch {
		case a == nil:
			derr(first.col, `unknown lifeline "`+first.v+`"`)
		case b == nil:
			derr(toks[2].col, `unknown lifeline "`+toks[2].v+`"`)
		case m == nil:
			derr(toks[3].col, "expected a time like @100 or @100..150")
		default:
			t0, _ := strconv.ParseFloat(m[1], 64)
			msg := &DslTmMsg{From: a.ID, To: b.ID, T: tmRound(t0), Line: ln}
			if m[2] != "" {
				t2, _ := strconv.ParseFloat(m[2], 64)
				if tmRound(t2) != msg.T {
					msg.T2, msg.HasT2 = tmRound(t2), true
				}
			}
			if t := at(4); t != nil && t.k == "str" {
				msg.Text = t.v
			}
			tm.Messages = append(tm.Messages, msg)
		}
		return false
	}
	// life t1..t2 "text"
	if len(toks) >= 2 && toks[1].k == "word" && reTmRange.MatchString(toks[1].v) {
		l := tm.life(first.v)
		parts := strings.Split(toks[1].v, "..")
		x, _ := strconv.ParseFloat(parts[0], 64)
		y, _ := strconv.ParseFloat(parts[1], 64)
		if l == nil {
			derr(first.col, `unknown lifeline "`+first.v+`"`)
		} else {
			c := &DslTmCon{Life: l.ID, T1: tmRound(math.Min(x, y)), T2: tmRound(math.Max(x, y)), Line: ln}
			if t := at(2); t != nil && t.k == "str" {
				c.Text = t.v
			}
			tm.Constraints = append(tm.Constraints, c)
		}
		return false
	}
	derr(first.col, `unknown statement "`+first.v+`" in timing (expected unit, end, robust, concise, binary, clock, a state change like wb @100 Busy, a message like a -> b @100 "text" or a constraint like wb 100..250 "{150 ms}")`)
	return false
}
