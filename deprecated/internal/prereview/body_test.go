package prereview

import (
	"strings"
	"testing"
)

// TestBodyCardPathsAndDoneWhen (nova-tools #2536): a body's PATHS line and
// DONE-WHEN are read, so the paths check decides on hand-written pull requests
// (product code outside the PATHS is red, a test-only file outside is not) and
// a DONE-WHEN whose named test the diff adds answers yes.
func TestBodyCardPathsAndDoneWhen(t *testing.T) {
	t.Parallel()

	body := "Fixes it.\n\n**PATHS:** `internal/x/`, cmd/y/main.go (+ main_test.go). Nothing else.\nDONE-WHEN: `go test ./internal/x/ -run 'TestXA|TestXB'` passes\n"
	got := strings.Join(BodyPaths(body), " ")
	if got != "internal/x/** cmd/y/main.go **/main_test.go" {
		t.Fatalf("BodyPaths = %q", got)
	}
	if dw := BodyDoneWhen(body); !strings.Contains(dw, "TestXA|TestXB") {
		t.Fatalf("BodyDoneWhen = %q", dw)
	}
	if p := BodyPaths("PATHS:\n- a/b.go\n- c/\n\nprose"); strings.Join(p, " ") != "a/b.go c/**" {
		t.Fatalf("bulleted PATHS = %q", p)
	}
	diff := "+++ b/internal/x/x_test.go\n+func TestXA(t *testing.T) {}\n+func TestXB(t *testing.T) {}\n"
	pr := PR{Body: body, Diff: diff, Files: []string{"internal/x/x.go", "internal/x/x_test.go", "cmd/y/main.go", "tests/extra.bats"}}
	c := Mechanical(pr, InferCard(pr))
	if c.Paths.Result != Yes || !strings.Contains(c.Paths.Reason, "test-only") {
		t.Fatalf("paths = %+v, want yes with the test-only file noted", c.Paths)
	}
	if c.Done.Result != Yes {
		t.Fatalf("donewhen = %+v, want yes (the diff adds both named tests)", c.Done)
	}
	pr.Files = append(pr.Files, "cmd/z/main.go")
	if c := Mechanical(pr, InferCard(pr)); c.Paths.Result != No || !strings.Contains(c.Paths.Reason, "cmd/z/main.go") {
		t.Fatalf("paths = %+v, want no: product code outside the body's PATHS", c.Paths)
	}
	pr.Diff = "+++ b/internal/x/x.go\n+func X() {}\n"
	if c := Mechanical(pr, InferCard(pr)); c.Done.Result != Missing {
		t.Fatalf("donewhen = %+v, want missing: a named test the diff does not add may be on the base", c.Done)
	}
}

// TestResultBlankLineIsNotAMissingDone: a blank line under RESULT is
// formatting; the DONE line is the first non-blank line after it.
func TestResultBlankLineIsNotAMissingDone(t *testing.T) {
	t.Parallel()

	if c := doneCheck(PR{Body: "RESULT\n\nDONE\nfiles: a.go"}, Card{}); c.Result != Yes {
		t.Fatalf("done = %+v, want yes", c)
	}
	if c := doneCheck(PR{Body: "RESULT\n\nNOT DONE: stuck"}, Card{}); c.Result != No {
		t.Fatalf("done = %+v, want no", c)
	}
}

// TestBaseGateAndGateCap: the read rubric's base gate (a stacked base or a
// conflict is no) and the cap (an enabled gate that failed caps the score at 7).
func TestBaseGateAndGateCap(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		ref, mergeable string
		want           Result
	}{
		{"dev", "MERGEABLE", Yes},
		{"main", "MERGEABLE", Yes},
		{"rowan/x", "MERGEABLE", No},
		{"dev", "CONFLICTING", No},
		{"dev", "UNKNOWN", Missing},
		{"", "MERGEABLE", Missing},
	} {
		if got := baseCheck(PR{BaseRef: c.ref, Mergeable: c.mergeable}); got.Result != c.want {
			t.Fatalf("base %q %q = %+v, want %s", c.ref, c.mergeable, got, c.want)
		}
	}
	en, _ := ParseEnabled("ci,base,score")
	tune := Tuning{PassAbove: 7, Enabled: en}
	red := Checks{CI: Check{No, "red"}, Base: Check{Yes, "ok"}}
	if got := tune.GateCap(red, 9); got != 7 {
		t.Fatalf("GateCap(ci red, 9) = %d, want 7", got)
	}
	if got := tune.GateCap(Checks{CI: Check{Yes, ""}, Base: Check{Yes, ""}}, 9); got != 9 {
		t.Fatalf("GateCap(green, 9) = %d, want 9", got)
	}
	off := Tuning{PassAbove: 7, Enabled: map[string]bool{"score": true}}
	if got := off.GateCap(red, 9); got != 9 {
		t.Fatalf("a disabled gate capped the score: %d", got)
	}
	if c := ciCheck(PR{Head: strings.Repeat("a", 40), ChecksUnread: true}); c.Result != Missing {
		t.Fatalf("an unread rollup = %+v, want missing", c)
	}
}
