package bounded

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// lines splits a stream into its lines, dropping the empty tail after the last newline.
func lines(b *bytes.Buffer) []string {
	s := b.String()
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// The whole point, at the state that caused the incident: five hundred findings cost a
// reader twenty-one lines, and the twenty-first says how many there were.
func TestCapPrintsMaxLinesThenOneMoreLine(t *testing.T) {
	var out bytes.Buffer
	l := Capped(&out, Default, "VERIFY", "wikilink", "--fail-max <n> (0 = all)")
	for i := 0; i < 500; i++ {
		l.Line(fmt.Sprintf("VERIFY FAIL wikilink entry-%d", i))
	}
	l.More()

	got := lines(&out)
	if len(got) != Default+1 {
		t.Fatalf("got %d lines, want %d item lines and one MORE line", len(got), Default+1)
	}
	if got[0] != "VERIFY FAIL wikilink entry-0" {
		t.Errorf("first line is %q; the cap must keep the ORDER the verb produced, not a sample", got[0])
	}
	if got[Default-1] != "VERIFY FAIL wikilink entry-19" {
		t.Errorf("last item line is %q, want entry-19", got[Default-1])
	}
	want := "VERIFY MORE kind=wikilink shown=20 total=500 --fail-max <n> (0 = all)"
	if got[Default] != want {
		t.Errorf("MORE line is\n  %q\nwant\n  %q", got[Default], want)
	}
	if l.Total() != 500 || l.Shown() != 20 || l.Elided() != 480 {
		t.Errorf("counted shown=%d total=%d elided=%d; want 20/500/480 -- the count must be the truth about the STATE, not about the output",
			l.Shown(), l.Total(), l.Elided())
	}
}

// A cap that cannot be turned off is a tool deciding what its user may see.
func TestMaxZeroPrintsEverythingAndNoMoreLine(t *testing.T) {
	var out bytes.Buffer
	l := Capped(&out, 0, "LINKS", "broken", "--fail-max <n>")
	for i := 0; i < 300; i++ {
		l.Line(fmt.Sprintf("LINKS FAIL %d", i))
	}
	l.More()
	if got := lines(&out); len(got) != 300 {
		t.Fatalf("got %d lines, want 300 and no MORE line", len(got))
	}
	if l.Elided() != 0 {
		t.Errorf("elided=%d, want 0", l.Elided())
	}
}

// Below the ceiling nothing is added: a MORE line saying total equals shown carries no
// information, and this package is about not printing those.
func TestNoMoreLineWhenNothingWasElided(t *testing.T) {
	var out bytes.Buffer
	l := Capped(&out, 20, "SCAN", "dated", "--max <n>")
	for i := 0; i < 20; i++ {
		l.Line("SCAN DATED x")
	}
	l.More()
	if got := lines(&out); len(got) != 20 {
		t.Fatalf("got %d lines, want exactly the 20 item lines", len(got))
	}
}

// An empty listing prints nothing at all, so a caller can build the list unconditionally
// and let its own summary line speak for a clean run.
func TestEmptyListPrintsNothing(t *testing.T) {
	var out bytes.Buffer
	l := Capped(&out, 20, "NOCODE", "file", "--fail-max <n>")
	l.More()
	if out.Len() != 0 {
		t.Errorf("an empty listing wrote %q", out.String())
	}
}

// The cap must not be able to break its own line count. A finding's text is corpus text,
// and corpus text holds newlines.
func TestAnItemLineCannotAddASecondLine(t *testing.T) {
	var out bytes.Buffer
	l := Capped(&out, 20, "VERIFY", "wikilink", "--fail-max <n>")
	l.Line("VERIFY FAIL a\nVERIFY OK gating=0")
	l.More()
	got := lines(&out)
	if len(got) != 1 {
		t.Fatalf("one Line produced %d lines: %q", len(got), got)
	}
	if strings.Contains(got[0], "\n") || !strings.Contains(got[0], `\x0a`) {
		t.Errorf("the embedded newline is not escaped: %q", got[0])
	}
}

// A caller converting an existing fmt.Fprintf site copies a format string ending in \n.
// Tolerating that is robustness, not politeness: the alternative is a visible \x0a at the
// end of every line in the repo the day someone forgets.
func TestOneTrailingNewlineIsToleratedNotEscaped(t *testing.T) {
	var out bytes.Buffer
	l := Capped(&out, 20, "LINKS", "broken", "--fail-max <n>")
	l.Line("LINKS FAIL a.md:1\n")
	if got := out.String(); got != "LINKS FAIL a.md:1\n" {
		t.Errorf("got %q, want the line with exactly one newline", got)
	}
}

// The remedy and the kind reach the MORE line through the escape like everything else.
func TestMoreLineEscapesItsFields(t *testing.T) {
	var out bytes.Buffer
	l := Capped(&out, 1, "VERIFY", "a kind\nwith a break", "see from-rowan/OPEN\nand also")
	l.Line("one")
	l.Line("two")
	l.More()
	got := lines(&out)
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(got), got)
	}
	if !strings.Contains(got[1], `kind=a\x20kind\x0awith`) {
		t.Errorf("kind is not a single escaped token: %q", got[1])
	}
	if strings.Count(out.String(), "\n") != 2 {
		t.Errorf("the MORE line broke the line count: %q", out.String())
	}
}

// The reason Group exists: a flat cap over a concatenated list means the 10,000 findings
// of the first kind eat the single finding of the second, which is the one line the
// reader did not already know.
func TestGroupCapsEachKindSoOneCannotBuryAnother(t *testing.T) {
	var out bytes.Buffer
	g := Grouped(&out, 20, "VERIFY", "--fail-max <n> (0 = all)")
	for i := 0; i < 500; i++ {
		g.Line("wikilink", fmt.Sprintf("VERIFY FAIL wikilink %d", i))
	}
	g.Line("frontmatter", "VERIFY FAIL frontmatter the one that matters")
	g.More()

	got := lines(&out)
	// 20 wikilink lines + the frontmatter line + one MORE line for wikilink only.
	if len(got) != 22 {
		t.Fatalf("got %d lines, want 22: %q", len(got), got)
	}
	if got[20] != "VERIFY FAIL frontmatter the one that matters" {
		t.Errorf("the buried kind is missing; line 21 is %q", got[20])
	}
	if got[21] != "VERIFY MORE kind=wikilink shown=20 total=500 --fail-max <n> (0 = all)" {
		t.Errorf("MORE line is %q", got[21])
	}
	if g.Total() != 501 || g.Shown() != 21 {
		t.Errorf("group counted shown=%d total=%d, want 21/501", g.Shown(), g.Total())
	}
	if kinds := g.Kinds(); len(kinds) != 2 || kinds[0] != "wikilink" || kinds[1] != "frontmatter" {
		t.Errorf("kinds are %q; want first-seen order", kinds)
	}
}

// Determinism: the same findings in the same order print the same bytes, run after run.
// A map iteration leaking into the MORE lines would pass a length assertion and fail a
// diff, which is how this class of bug survives.
func TestGroupOutputIsDeterministic(t *testing.T) {
	render := func() string {
		var out bytes.Buffer
		g := Grouped(&out, 2, "VERIFY", "--fail-max <n>")
		for _, kind := range []string{"coverage", "wikilink", "frontmatter"} {
			for i := 0; i < 5; i++ {
				g.Line(kind, fmt.Sprintf("VERIFY FAIL %s %d", kind, i))
			}
		}
		g.More()
		return out.String()
	}
	first := render()
	for i := 0; i < 20; i++ {
		if got := render(); got != first {
			t.Fatalf("run %d differs:\n%s\nvs\n%s", i, got, first)
		}
	}
}
