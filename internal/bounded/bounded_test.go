package bounded

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Parallel()

	var out bytes.Buffer
	l := Capped(&out, Default, "VERIFY", "wikilink", "--fail-max <n> (0 = all)")
	for i := 0; i < 500; i++ {
		l.Line(fmt.Sprintf("VERIFY FAIL wikilink entry-%d", i))
	}
	l.More()

	got := lines(&out)
	require.Len(t, got, Default+1, "got %d lines, want %d item lines and one MORE line", len(got), Default+1)
	assert.Equal(t, "VERIFY FAIL wikilink entry-0", got[0], "first line is %q; the cap must keep the ORDER the verb produced, not a sample", got[0])
	assert.Equal(t, "VERIFY FAIL wikilink entry-19", got[Default-1], "last item line is %q, want entry-19", got[Default-1])
	want := "VERIFY MORE kind=wikilink shown=20 total=500 --fail-max <n> (0 = all)"
	assert.Equal(t, want, got[Default], "MORE line is\n  %q\nwant\n  %q", got[Default], want)
	assert.Equal(t, [2]int{20, 500}, [2]int{l.Shown(), l.Total()}, "counted shown/total; the count must be the truth about the STATE, not about the output")
}

// A cap that cannot be turned off is a tool deciding what its user may see.
func TestMaxZeroPrintsEverythingAndNoMoreLine(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	l := Capped(&out, 0, "LINKS", "broken", "--fail-max <n>")
	for i := 0; i < 300; i++ {
		l.Line(fmt.Sprintf("LINKS FAIL %d", i))
	}
	l.More()
	got := lines(&out)
	require.Len(t, got, 300, "got %d lines, want 300 and no MORE line", len(got))
}

// Below the ceiling nothing is added: a MORE line saying total equals shown carries no
// information, and this package is about not printing those.
func TestNoMoreLineWhenNothingWasElided(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	l := Capped(&out, 20, "SCAN", "dated", "--max <n>")
	for i := 0; i < 20; i++ {
		l.Line("SCAN DATED x")
	}
	l.More()
	got := lines(&out)
	require.Len(t, got, 20, "got %d lines, want exactly the 20 item lines", len(got))
}

// An empty listing prints nothing at all, so a caller can build the list unconditionally
// and let its own summary line speak for a clean run.
func TestEmptyListPrintsNothing(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	l := Capped(&out, 20, "NOCODE", "file", "--fail-max <n>")
	l.More()
	assert.Empty(t, out.String(), "an empty listing wrote %q", out.String())
}

// The cap must not be able to break its own line count. A finding's text is corpus text,
// and corpus text holds newlines.
func TestAnItemLineCannotAddASecondLine(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	l := Capped(&out, 20, "VERIFY", "wikilink", "--fail-max <n>")
	l.Line("VERIFY FAIL a\nVERIFY OK gating=0")
	l.More()
	got := lines(&out)
	require.Len(t, got, 1, "one Line produced %d lines: %q", len(got), got)
	assert.NotContains(t, got[0], "\n", "the embedded newline is not escaped: %q", got[0])
	assert.Contains(t, got[0], `\x0a`, "the embedded newline is not escaped: %q", got[0])
}

// A caller converting an existing fmt.Fprintf site copies a format string ending in \n.
// Tolerating that is robustness, not politeness: the alternative is a visible \x0a at the
// end of every line in the repo the day someone forgets.
func TestOneTrailingNewlineIsToleratedNotEscaped(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	l := Capped(&out, 20, "LINKS", "broken", "--fail-max <n>")
	l.Line("LINKS FAIL a.md:1\n")
	got := out.String()
	assert.Equal(t, "LINKS FAIL a.md:1\n", got, "got %q, want the line with exactly one newline", got)
}

// The remedy and the kind reach the MORE line through the escape like everything else.
func TestMoreLineEscapesItsFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	l := Capped(&out, 1, "VERIFY", "a kind\nwith a break", "see from-rowan/OPEN\nand also")
	l.Line("one")
	l.Line("two")
	l.More()
	got := lines(&out)
	require.Len(t, got, 2, "got %d lines, want 2: %q", len(got), got)
	assert.Contains(t, got[1], `kind=a\x20kind\x0awith`, "kind is not a single escaped token: %q", got[1])
	assert.Equal(t, 2, strings.Count(out.String(), "\n"), "the MORE line broke the line count: %q", out.String())
}

// The reason Group exists: a flat cap over a concatenated list means the 10,000 findings
// of the first kind eat the single finding of the second, which is the one line the
// reader did not already know.
func TestGroupCapsEachKindSoOneCannotBuryAnother(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	g := Grouped(&out, 20, "VERIFY", "--fail-max <n> (0 = all)")
	for i := 0; i < 500; i++ {
		g.Line("wikilink", fmt.Sprintf("VERIFY FAIL wikilink %d", i))
	}
	g.Line("frontmatter", "VERIFY FAIL frontmatter the one that matters")
	g.More()

	got := lines(&out)
	// 20 wikilink lines + the frontmatter line + one MORE line for wikilink only.
	require.Len(t, got, 22, "got %d lines, want 22: %q", len(got), got)
	assert.Equal(t, "VERIFY FAIL frontmatter the one that matters", got[20], "the buried kind is missing; line 21 is %q", got[20])
	assert.Equal(t, "VERIFY MORE kind=wikilink shown=20 total=500 --fail-max <n> (0 = all)", got[21], "MORE line is %q", got[21])
	assert.Equal(t, [2]int{21, 501}, [2]int{g.Shown(), g.Total()}, "group counted shown/total, want 21/501")
}

// Determinism: the same findings in the same order print the same bytes, run after run.
// A map iteration leaking into the MORE lines would pass a length assertion and fail a
// diff, which is how this class of bug survives.
func TestGroupOutputIsDeterministic(t *testing.T) {
	t.Parallel()

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
		got := render()
		require.Equal(t, first, got, "run %d differs:\n%s\nvs\n%s", i, got, first)
	}
}

// A listing whose writer fails has not shown anything, and says so: a caller
// whose delivery record follows Shown() must not mark a line delivered that
// never reached the stream.
func TestAFailedWriteIsNotShown(t *testing.T) {
	t.Parallel()

	l := Capped(brokenWriter{}, 0, "WAKE", "report", "--max-lines 0 prints them all")
	l.Line("WAKE REPORT path=a")
	l.Line("WAKE REPORT path=b")
	assert.Equal(t, 0, l.Shown(), "Shown() = %d over a writer that failed every write, want 0", l.Shown())
	assert.Equal(t, 2, l.Total(), "Total() = %d, want 2: the count is the truth about the state even when the output is not", l.Total())
	assert.Error(t, l.Err(), "the write error was discarded")
}

type brokenWriter struct{}

func (brokenWriter) Write(p []byte) (int, error) { return 0, errWrite }

var errWrite = errors.New("the reader has gone")

// TestTallyCountsLikeTheListing: a Tally keeps the same items per kind that a
// Grouped listing prints, and counts the rest.
func TestTallyCountsLikeTheListing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		max          int
		kinds        string
		listed       string
		shownA, allA int
	}{
		{"a ceiling per kind", 2, "aabaab", "+++--+", 2, 4},
		{"zero lists all", 0, "aaab", "++++", 3, 3},
		{"under the ceiling", 5, "ab", "++", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tl := NewTally(tc.max)
			got := ""
			for _, k := range tc.kinds {
				if tl.Add(string(k)) {
					got += "+"
				} else {
					got += "-"
				}
			}
			assert.Equal(t, tc.listed, got, "listed %s shown %d total %d kinds %v; want %s %d %d", got, tl.Shown("a"), tl.Total("a"), tl.Kinds(), tc.listed, tc.shownA, tc.allA)
			assert.Equal(t, tc.shownA, tl.Shown("a"), "listed %s shown %d total %d kinds %v; want %s %d %d", got, tl.Shown("a"), tl.Total("a"), tl.Kinds(), tc.listed, tc.shownA, tc.allA)
			assert.Equal(t, tc.allA, tl.Total("a"), "listed %s shown %d total %d kinds %v; want %s %d %d", got, tl.Shown("a"), tl.Total("a"), tl.Kinds(), tc.listed, tc.shownA, tc.allA)
			if assert.NotEmpty(t, tl.Kinds(), "listed %s shown %d total %d kinds %v; want %s %d %d", got, tl.Shown("a"), tl.Total("a"), tl.Kinds(), tc.listed, tc.shownA, tc.allA) {
				assert.Equal(t, "a", tl.Kinds()[0], "listed %s shown %d total %d kinds %v; want %s %d %d", got, tl.Shown("a"), tl.Total("a"), tl.Kinds(), tc.listed, tc.shownA, tc.allA)
			}
		})
	}
}
