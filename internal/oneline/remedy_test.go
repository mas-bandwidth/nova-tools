package oneline

import "testing"

// TestWithRemedyEndsEveryLineWithANextStep pins the two halves: a line with no
// remedy gains "; run: <next>", and a line that names one already is left
// exactly as it was.
func TestWithRemedyEndsEveryLineWithANextStep(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ what, want string }{
		{"the harness binary /x is missing", "the harness binary /x is missing; run: tool verb -h"},
		{"--out exists; pass --overwrite to replace it", "--out exists; pass --overwrite to replace it"},
		{"no store; run: tool init", "no store; run: tool init"},
		{"LINT FAIL remedy=tool lint --max 0", "LINT FAIL remedy=tool lint --max 0"},
		{"--max must be 1 to 64; give 1 to 64", "--max must be 1 to 64; give 1 to 64"},
	} {
		if got := WithRemedy(c.what, "tool verb -h"); got != c.want {
			t.Errorf("WithRemedy(%q) = %q, want %q", c.what, got, c.want)
		}
	}
	if got := WithRemedy("two\nlines", "tool -h"); got != "two\\x0alines; run: tool -h" {
		t.Errorf("WithRemedy escapes: got %q", got)
	}
	if once := WithRemedy(Err(errString("a\tb")), "tool -h"); once != Escape(once) {
		t.Errorf("WithRemedy(Err(...)) = %q is not stable under Escape", once)
	}
	for _, s := range []string{"the store refused the write", "cannot read the file", "the name of the card"} {
		if HasRemedy(s) {
			t.Errorf("HasRemedy(%q) = true; the line names no next step", s)
		}
	}
}
