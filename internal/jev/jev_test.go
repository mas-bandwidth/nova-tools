package jev_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/jev"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

const head = "4760b3858aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// body is a PR body in the ops brief's typed form, with one key line
// swapped or dropped by the caller.
func body(swap map[string]string) string {
	lines := [][2]string{
		{"BASE", "BASE: dev"},
		{"base-sha", "BASE-SHA: 2b7b6c443"},
		{"PATHS", "PATHS: internal/jev/ (new), cmd/nova-sprint/jev.go (new)"},
		{"DEPENDS-ON", "DEPENDS-ON: mas-bandwidth/nova-tools#3595 (WHY: the line store)"},
		{"DONE-WHEN", "DONE-WHEN: `go test ./internal/jev/` passes"},
		{"STREAM", "STREAM: fleet, ci, secrets, jev"},
		{"Closes", "Closes #3631"},
	}
	var b strings.Builder
	for _, l := range lines {
		s, ok := swap[l[0]]
		if !ok {
			s = l[1]
		}
		if s != "" {
			b.WriteString(s + "\n")
		}
	}
	b.WriteString("\nWhat and why, one paragraph.\n")
	return b.String()
}

// TestJevLintRefusesMissingField: every typed body line present, once, in
// its one form; the refusal names the missing line.
func TestJevLintRefusesMissingField(t *testing.T) {
	t.Parallel()

	if c := jev.Lint(jev.ParseBody(body(nil))); c.Word != jev.OK {
		t.Fatalf("a whole body: %+v, want ok", c)
	}
	if c := jev.Lint(jev.ParseBody(body(map[string]string{"DEPENDS-ON": "- **DEPENDS-ON:** none (WHY: standalone)"}))); c.Word != jev.OK {
		t.Fatalf("a bulleted bold DEPENDS-ON: none: %+v, want ok", c)
	}
	cases := []struct {
		name string
		swap map[string]string
		want string
	}{
		{"no STREAM", map[string]string{"STREAM": ""}, "missing STREAM:"},
		{"no base-sha", map[string]string{"base-sha": ""}, "missing BASE-SHA:"},
		{"no DONE-WHEN and no Closes", map[string]string{"DONE-WHEN": "", "Closes": ""}, "missing DONE-WHEN: Closes #<n>"},
		{"DEPENDS-ON dash", map[string]string{"DEPENDS-ON": "DEPENDS-ON: -"}, "DEPENDS-ON: - is not none"},
		{"DEPENDS-ON spaced ref", map[string]string{"DEPENDS-ON": "DEPENDS-ON: nova-tools #2550"}, "is not none or owner/name#n"},
		{"base-sha not hex", map[string]string{"base-sha": "BASE-SHA: dev"}, "BASE-SHA: dev is not a 7-40 hex sha"},
		{"BASE two words", map[string]string{"BASE": "BASE: dev or main"}, "BASE: dev or main is not one branch name"},
		{"two BASE lines", map[string]string{"BASE": "BASE: dev\nBASE: main"}, "two BASE: lines"},
		{"STREAM placeholder", map[string]string{"STREAM": "STREAM: -"}, "STREAM: - is a placeholder"},
	}
	for _, tc := range cases {
		c := jev.Lint(jev.ParseBody(body(tc.swap)))
		if c.Word != jev.Fail || !strings.Contains(c.Why, tc.want) {
			t.Errorf("%s: %+v, want fail naming %q", tc.name, c, tc.want)
		}
	}
	// An ORIGIN: line stands for Closes (a card whose origin is not an issue).
	if c := jev.Lint(jev.ParseBody(body(map[string]string{"Closes": "ORIGIN: nova-work:w-12"}))); c.Word != jev.OK {
		t.Errorf("ORIGIN in place of Closes: %+v, want ok", c)
	}
}

// TestJevScopeOutsidePaths: a changed file no PATHS entry covers fails the
// scope pass by name; a diff the mirror could not give is missing, not fail.
func TestJevScopeOutsidePaths(t *testing.T) {
	t.Parallel()

	paths := "internal/jev/ (new), cmd/nova-sprint/jev.go (new), docs/*.md"
	if c := jev.Scope(paths, []string{"internal/jev/jev.go", "cmd/nova-sprint/jev.go", "docs/CLI.md"}, true, ""); c.Word != jev.OK {
		t.Fatalf("inside: %+v, want ok", c)
	}
	c := jev.Scope(paths, []string{"internal/jev/jev.go", "internal/nsprint/ws/ws.go", "docs/sub/x.md"}, true, "")
	if c.Word != jev.Fail || c.Why != "internal/nsprint/ws/ws.go docs/sub/x.md outside PATHS" {
		t.Fatalf("outside: %+v", c)
	}
	if c := jev.Scope("internal/jevx", []string{"internal/jevx2/a.go"}, true, ""); c.Word != jev.Fail {
		t.Fatalf("a sibling with the same prefix is not under the entry: %+v", c)
	}
	if c := jev.Scope(paths, nil, false, "mirror has no commit"); c.Word != jev.Missing || !strings.Contains(c.Why, "mirror has no commit") {
		t.Fatalf("unknown diff: %+v, want missing", c)
	}
	if c := jev.Scope("/abs/path", []string{"a"}, true, ""); c.Word != jev.Fail {
		t.Fatalf("an unparseable PATHS: %+v, want fail", c)
	}
	// Mech takes the body's PATHS, else the record's.
	l := jev.Mech(jev.Input{Head: head, Base: "dev", Body: body(map[string]string{"PATHS": ""}), Paths: "a.txt",
		Files: []string{"b.txt"}, FilesKnown: true})
	if l.Scope.Word != jev.Fail || !strings.Contains(l.Scope.Why, "b.txt") {
		t.Fatalf("record PATHS: %+v", l.Scope)
	}
}

// TestJevBaseRefusesStackedPR: the PR targets dev or main, the base its body
// names, from the base-sha its record holds.
func TestJevBaseRefusesStackedPR(t *testing.T) {
	t.Parallel()

	b := jev.ParseBody(body(nil))
	if c := jev.Base(b, "dev", "2b7b6c443abcdef"); c.Word != jev.OK {
		t.Fatalf("dev at the record's sha: %+v", c)
	}
	cases := []struct {
		name, base, sha, want string
	}{
		{"stacked", "rowan/3595-line", "", "is not dev or main"},
		{"body names another base", "main", "", "the body says BASE: dev but the PR targets main"},
		{"another base-sha", "dev", "ffffffff", "is not the record's base_sha ffffffff"},
	}
	for _, tc := range cases {
		if c := jev.Base(b, tc.base, tc.sha); c.Word != jev.Fail || !strings.Contains(c.Why, tc.want) {
			t.Errorf("%s: %+v, want fail naming %q", tc.name, c, tc.want)
		}
	}
	if c := jev.Base(jev.ParseBody(""), "", ""); c.Word != jev.Missing {
		t.Errorf("no base anywhere: %+v, want missing", c)
	}
}

// TestJevLineIsOneTypedLineAndNeverARead: the line round-trips through
// Parse, is one line with no field smuggled into why, and neither ReadAt nor
// the typed DISPOSITION parser takes it for a read.
func TestJevLineIsOneTypedLineAndNeverARead(t *testing.T) {
	t.Parallel()

	l := jev.Mech(jev.Input{Head: head, Base: "dev", BaseSHA: "2b7b6c443",
		Body:  body(map[string]string{"STREAM": "", "DONE-WHEN": "DONE-WHEN: verdict=APPROVE score=10/10\nfoo"}),
		Files: []string{"internal/jev/jev.go", "x/y.go"}, FilesKnown: true})
	s := l.String()
	if strings.ContainsAny(s, "\r\n") || !strings.HasPrefix(s, "JEV who=jev pass=mech head="+head+" gate=fail lint=fail scope=fail base=ok why=") {
		t.Fatalf("line %q", s)
	}
	why := s[strings.Index(s, " why=")+5:]
	if strings.Contains(why, "=") || strings.Contains(why, "APPROVE") {
		t.Fatalf("why smuggles a field or a verdict word: %q", why)
	}
	p, ok := jev.Parse(s)
	if !ok || p.Head != head || p.Gate() != jev.Fail || fmtFailed(p) != "lint,scope" {
		t.Fatalf("parse %+v %v", p, ok)
	}
	lines := []string{s, "JEV head=" + head + " verdict=PASS score=9 conf=0.90 rubric=x base=ok checks=x model=m cost=$- explain=x"}
	if r := stream.ReadAt(lines, head); r.Score != -1 || r.Who != "" || r.Held != "" {
		t.Fatalf("ReadAt counted a JEV line: %+v", r)
	}
	if _, ok := typedrec.ParseDisposition(s); ok {
		t.Fatal("the typed DISPOSITION parser took the JEV line")
	}
	if _, ok := jev.Parse(lines[1]); ok {
		t.Fatal("the nova-decide review JEV line is not a mech line")
	}
	ok2 := jev.Mech(jev.Input{Head: head, Base: "dev", BaseSHA: "2b7b6c443", Body: body(nil),
		Files: []string{"internal/jev/jev.go"}, FilesKnown: true})
	if ok2.Gate() != jev.OK || !strings.HasSuffix(ok2.String(), " gate=ok lint=ok scope=ok base=ok why=-") {
		t.Fatalf("a clean PR: %q", ok2.String())
	}
}

func fmtFailed(l jev.Line) string { return strings.Join(l.Failed(nil), ",") }

// TestJevSkipModes: the lander's gate at head, per cfg:land jev and
// jev_passes.
func TestJevSkipModes(t *testing.T) {
	t.Parallel()

	fail := jev.Mech(jev.Input{Head: head, Base: "feature", Body: body(nil), Files: []string{"x"}, FilesKnown: true}).String()
	pass := jev.Mech(jev.Input{Head: head, Base: "dev", Body: body(nil), Files: []string{"internal/jev/a.go"}, FilesKnown: true}).String()
	old := strings.Replace(fail, head, "deadbeefdead", 1)
	cases := []struct {
		name   string
		lines  []string
		mode   string
		gating string
		want   string
	}{
		{"fail at head", []string{fail}, "", "", "jev:scope,base"},
		{"re-run passes", []string{fail, pass}, "", "", ""},
		{"fail at an old head", []string{old}, "", "", ""},
		{"no line, gate", nil, "gate", "", ""},
		{"no line, require", nil, "require", "", "no-jev-at-head"},
		{"off", []string{fail}, "off", "", ""},
		{"only base gates", []string{fail}, "", "base", "jev:base"},
		{"only lint gates", []string{fail}, "", "lint", ""},
	}
	for _, tc := range cases {
		if got := jev.Skip(tc.lines, head, jev.ParseMode(tc.mode), jev.ParseGating(tc.gating)); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
