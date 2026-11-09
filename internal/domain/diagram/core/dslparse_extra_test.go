package core

import (
	"strings"
	"testing"
)

func diagText(r ParseResult) string {
	var b strings.Builder
	for _, d := range r.Diagnostics {
		b.WriteString(d.Message + "\n")
	}
	return b.String()
}

func TestParseDiagnostics(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"late header", "node a\n#!sdd 1", "header must be the first line"},
		{"stray close", "}", `unexpected "}"`},
		{"unterminated string", `node a "x`, "unterminated string"},
		{"diagram no title", "diagram", "needs a title"},
		{"node no id", "node", "needs an id"},
		{"node block", "node a {\n}", "only groups can contain nodes"},
		{"dup id", "node a\nnode a", `duplicate id "a"`},
		{"attr stray", "node a ,= x", `unexpected "="`},
		{"attr no value", "node a icon=", `missing value for "icon"`},
		{"attr list value", "node a pos=1,2,3", ""},
		{"table nested", "group g {\ntable t {\n}\n}", "blocks must be at the top level"},
		{"table no name", "table", "needs a name"},
		{"table no brace", "table t", `expected "{" after the table name`},
		{"dup table", "table t {\n}\ntable t {\n}", `duplicate table "t"`},
		{"bad column", "table t {\n  x\n}", "expected a column"},
		{"dup column", "table t {\n  a int\n  a int\n}", `duplicate column "a"`},
		{"unknown column attr", "table t {\n  a int [bogus]\n}", `unknown column attribute "bogus"`},
		{"bad column ref", "table t {\n  a int [ref: nope]\n}", "expected ref: > table.column"},
		{"null column", "table t {\n  a int [not null, null, increment, default: 1, note: hi, primary key]\n}", ""},
		{"enum rows", "enum e {\n  one [note: x]\n  two\n}", ""},
		{"indexes", "table t {\n  a int\n  indexes {\n    (a, b) [unique, name: \"i\"]\n    a\n    [\n  }\n  note: hello\n}", "expected: column"},
		{"ref bad", "ref: nope", "expected: ref:"},
		{"ref unknown table", "ref: a.b > c.d", `unknown table "a"`},
		{"ref unknown column", "table a {\n  x int\n}\nref: a.nope > a.x", `unknown column "a.nope"`},
		{"col ref unknown", "table a {\n  x int [ref: > b.y]\n}", `unknown table "b"`},
		{"ref relation", "a.b > c.d", "relations are written"},
		{"edge unknown node", "a -> b", `unknown node "a"`},
		{"unknown statement", "frobnicate", `unknown statement "frobnicate"`},
		{"unclosed group", "group g {", `missing "}" to close group "g"`},
		{"unclosed table", "table t {", `missing "}" to close table "t"`},
		{"unclosed seq", "seq s {", `missing "}" to close seq`},
		{"unclosed timing", "timing t {", `missing "}" to close timing`},
		{"unclosed uml", "classes {", "missing"},
		{"hash comment", "# nothing\nnode a", ""},
		{"enum single quote", "enum e {\n  '\n}", ""},
		{"comment only", "// nothing\nnode a // trailing", ""},
		{"escape", "node a \"x\\ny\\\"z\"", ""},
		{"seq slug fallback", "seq \"!!!\" {\n}", ""},
		{"ref half", "table a {\n  x int\n}\nref: a.x > b", "expected: ref:"},
		{"enum quote", "enum e {\n  \"\n}", ""},
		{"uml nested", "group g {\nclasses {\n}\n}", "blocks must be at the top level"},
		{"seq no brace", "seq", `expected: seq "Title" {`},
		{"seq untitled", "seq {\n}\nseq {\n}", `duplicate sequence`},
		{"seq titled", "seq \"A b\" {\n}\nseq \"A b\" {\n}", `duplicate sequence`},
		{"timing no brace", "timing", "expected: timing id"},
		{"timing untitled", "timing {\n}\ntiming {\n}", "duplicate timing"},
		{"timing titled", "timing t \"T\" {\n}\ntiming t {\n}", "duplicate timing"},
		{"seq end without open", "seq s {\n  end\n}", `"end" without an open`},
		{"seq unclosed fragment", "seq s {\n  alt x\n  a -> b: hi\n}", `missing "end" for alt`},
		{"seq bad note", "seq s {\n  note over a, b, c: hi\n}", "one or two participants"},
		{"seq note sides", "seq s {\n  note left of a: hi\n  note right of a: hi\n  note over a: hi\n}", ""},
		{"seq wrong section", "seq s {\n  alt x\n  and y\n  end\n}", `"and" does not belong in alt`},
		{"seq else ok", "seq s {\n  loop x\n  else y\n  end\n}", ""},
		{"seq garbage", "seq s {\n  " + strings.Repeat("zzz ", 20) + "\n}", "cannot read sequence statement"},
		{"seq participants", "seq s {\n  participant a as \"A\"\n  participant a as B model=m1\n  actor c\n  a ->> c: x\n  a -->> c: y\n  a -x c: z\n  a --> c: r\n  autonumber\n}", ""},
	}
	for _, c := range cases {
		got := diagText(ParseDSL(c.text))
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: diagnostics %q lack %q", c.name, got, c.want)
		}
		if c.want == "" && strings.Contains(got, "\n") {
			t.Logf("%s: unexpected diagnostics %q", c.name, got)
		}
	}
}

func blk(block string, lines ...string) string {
	return block + " {\n" + strings.Join(lines, "\n") + "\n}"
}

func TestParseUmlDiagnostics(t *testing.T) {
	cases := []struct{ name, text, want string }{
		// classes
		{"member bad", blk("classes", "class A {", "  + (", "}"), "expected a member"},
		{"member missing paren", blk("classes", "class A {", "  + f(a: A", "}"), `missing ")"`},
		{"member param no type", blk("classes", "class A {", "  + f(a, b: B, ): R", "}"), ""},
		{"member after op", blk("classes", "class A {", "  + f() junk", "}"), "unexpected text after the operation"},
		{"member junk", blk("classes", "class A {", "  + name junk", "}"), "after the member name"},
		{"member nested parens", blk("classes", "class A {", "  static abstract + f(a: List<(b)>, c: Map<A, B>): R", "  - x: int = 3", "}"), ""},
		{"enum members", blk("classes", "enum E {", "  A", "  B", "}"), ""},
		{"package no id", blk("classes", "package"), "package needs an id"},
		{"package no brace", blk("classes", "package p"), `expected "{" after the package`},
		{"package nested", blk("classes", "package p \"P\" {", "package q {", "class A", "}", "}"), ""},
		{"generics open", blk("classes", "class A<T"), `missing ">"`},
		{"generics ok", blk("classes", "class A<T, U=>X> extends B, C implements D {", "}"), ""},
		{"class header lex", blk("classes", `class A "x`), "unterminated string"},
		{"class header junk", blk("classes", "class A , x"), "in the class header"},
		{"class header attrs", blk("classes", `class A "stereo" model=m1 archRef=n1 archFile=f archPage=p {`, "}"), ""},
		{"dup class", blk("classes", "class A", "class A"), `duplicate class "A"`},
		{"rel attr unknown", blk("classes", "class A", "class B", `A --|> B "x" srcMult=1 bogus=2`), `unknown relation attribute "bogus"`},
		{"classes unknown", blk("classes", "zzz"), `in classes`},
		// states
		{"region outside", blk("states", "region"), `"region" belongs inside`},
		{"region ok", blk("states", "state s {", "region", "state t", "}"), ""},
		{"state no id", blk("states", "state"), "needs an id"},
		{"state label", blk("states", "initial i", `state a "A" vertical`, "a -> a \"e\""), ""},
		{"non-plain composite", blk("states", "choice c {", "}"), "only a plain state can hold other states"},
		{"state entry", blk("states", "state s {", "entryPoint e", "}"), ""},
		{"states unknown", blk("states", "zzz"), "in states"},
		// usecases
		{"ucase point bad", blk("usecases", "usecase u {", "zzz", "}"), `expected: point`},
		{"ucase point ok", blk("usecases", "usecase u {", "point \"p\"", "}"), ""},
		{"uc no id", blk("usecases", "actor"), "needs an id"},
		{"actor body", blk("usecases", "actor a {", "}"), "an actor has no body"},
		{"boundary nested", blk("usecases", "boundary b {", "boundary c {", "}", "}"), "boundaries cannot be nested"},
		{"uc rel", blk("usecases", "actor a \"A\"", "usecase u", "a -- u \"x\" srcMult=1", "a include u", "a extend u", "a generalizes u"), ""},
		{"uc unknown", blk("usecases", "zzz"), "in usecases"},
		// activity
		{"lane no id", blk("activity", "lane"), "lane needs an id"},
		{"lane no brace", blk("activity", "lane l"), `expected "{" after the lane`},
		{"lane nested", blk("activity", "lane l {", "lane m {", "}", "}"), "lanes cannot be nested"},
		{"lane attrs", blk("activity", `lane l "L" bogus orient=x {`, "}"), "unknown lane attribute"},
		{"lane orient h", blk("activity", `lane l "L" orient=h {`, "initial i", "}"), ""},
		{"act no id", blk("activity", "action"), "needs an id"},
		{"act body", blk("activity", "action a {", "}"), "an activity element has no body"},
		{"act kinds", blk("activity", "initial i", `action a "A"`, "fork f vertical", "object o state=s", "final x", "a -> f \"[g]\"", "a -- f"), ""},
		{"act unknown", blk("activity", "zzz"), "in activity"},
		// packages
		{"pkg no id", blk("packages", "package"), "package needs an id"},
		{"pkg nested", blk("packages", "package a {", "package b \"B\" {", "}", "}", "a imports b \"x\"", "a ..> b"), ""},
		{"pkg unknown", blk("packages", "zzz"), "in packages"},
		// objects / communication
		{"obj no id", blk("objects", "object"), "object needs an id"},
		{"obj attrs", blk("objects", `object o "O" class=C model=m bogus {`, "  a = 1", "  \"b c\" = 2", "}"), "unknown object attribute"},
		{"obj slot bad", blk("objects", "object o {", "  = x", "}"), "expected a slot"},
		{"obj link", blk("objects", "object a", "object b", `a -- b "x" srcRole=r bogus=1`), "unknown link attribute"},
		{"obj unknown", blk("objects", "zzz"), "in objects"},
		{"comm msg", blk("communication", "object a", "object b", `1.1 a -> b "t" async bogus`), "unknown message attribute"},
		{"comm unknown", blk("communication", "zzz"), "in communication"},
		// deployment
		{"dep no id", blk("deployment", "node"), "needs an id"},
		{"dep attrs", blk("deployment", `node n "N" stereotype=s bogus=1 {`, `artifact a`, "}"), "unknown deployment attribute"},
		{"dep leaf body", blk("deployment", "artifact a {", "}"), "can hold other elements"},
		{"dep links", blk("deployment", "node a", "node b", `a -- b "http"`, "a ..> b", "a manifests b"), ""},
		{"dep unknown", blk("deployment", "zzz"), "in deployment"},
		// components
		{"comp no id", blk("components", "component"), "needs an id"},
		{"comp provides", blk("components", "component c {", "provides IA, IB", "requires IC", "provides", "}"), "needs interface names"},
		{"comp nested", blk("components", "component c \"C\" stereotype=s archRef=n1 {", "component d", "}"), ""},
		// bad nested contexts and EOF in blocks
		{"unclosed class", blk("classes", "class A {"), "missing"},
		{"unclosed class real", "classes {\nclass A {", `class "A"`},
		{"member junk long", blk("classes", "class A {", "  + name "+strings.Repeat("x", 40), "}"), "after the member name"},
		{"param weird", blk("classes", "class A {", "  + f(:x, y): R", "}"), ""},
		{"rel attr junk", blk("classes", "class A", "class B", `A --|> B (`, `A --|> B , x=1`), "unexpected"},
		{"generics then bad string", blk("classes", `class A<"x> "y`), "unterminated string"},
		{"provides orphan", "node c\ncomponents {\ncomponent c {\nprovides I\n}\n}", ""},
		{"unclosed package", "classes {\npackage p {", "package"},
		{"unclosed usecase", "usecases {\nusecase u {", "usecase"},
		{"unclosed object", "objects {\nobject o {", "object"},
		{"unclosed lane", "activity {\nlane l {", ""},
	}
	for _, c := range cases {
		got := diagText(ParseDSL(c.text))
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: diagnostics %q lack %q", c.name, got, c.want)
		}
	}
}

func TestParseTimingDiagnostics(t *testing.T) {
	tm := func(lines ...string) string { return blk("timing t \"T\"", lines...) }
	cases := []struct{ name, text, want string }{
		{"lex", tm(`robust a "x`), "unterminated string"},
		{"unit end", tm("unit ms", "end 100", "end 0", "end x"), "end is a positive number"},
		{"no id", tm("robust"), "needs an id"},
		{"period nonclock", tm("robust a period=5"), "period belongs to a clock"},
		{"period bad", tm("clock c period=0"), "period is a positive number"},
		{"junk decl", tm("robust a (", "binary b states x"), "in the robust declaration"},
		{"dup life", tm("robust a", "robust a"), "duplicate lifeline"},
		{"binary states", tm("binary b states x"), "has no state list"},
		{"changes", tm(`robust a states Idle, Busy`, `clock c period=10`, `binary b`, `a @10 Busy`, `a @10 Idle`, `a @20 New`, `b @5 high`, `b @6 weird`, `c @5 x`, `a @-1 x`, `zz @1 x`), `unknown lifeline "zz"`},
		{"change bad time", tm("robust a", "a @x s"), "expected a time like @100"},
		{"change clock", tm("clock c", "c @1 s"), "a clock has no state changes"},
		{"msg", tm("robust a", "robust b", `a -> b @10 "hi"`, `a -> b @10..20`, `a -> b @10..10`), ""},
		{"msg unknown a", tm("robust b", `x -> b @10 "hi"`), `unknown lifeline "x"`},
		{"msg unknown b", tm("robust a", `a -> x @10 "hi"`), `unknown lifeline "x"`},
		{"msg bad time", tm("robust a", `a -> a 10 "hi"`), "expected a time like @100 or @100..150"},
		{"constraint", tm("robust a", `a 250..100 "{150}"`, `a 1..2`), ""},
		{"constraint unknown", tm(`x 1..2`), `unknown lifeline "x"`},
		{"unknown stmt", tm("zzz"), `in timing`},
	}
	for _, c := range cases {
		got := diagText(ParseDSL(c.text))
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: diagnostics %q lack %q", c.name, got, c.want)
		}
	}
}
