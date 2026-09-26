package ci

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sleeps_class_test.go is TestSleepsSkipsOnlyFall, the static falling ratchet
// on the SLEEPS skips (card ci-sleeps-ratchet, recut from nova-tools #4312 and
// PR #4321 onto the dev that carries the #4413 ledger).
//
// Glenn's rule (2026-09-25): a unit test never waits on the wall clock; it
// injects the clock and asserts the transition, or it becomes a functional
// test. The tests that did wait were skipped in one change (#4221), each with
// the same first line -- `t.Skip("SLEEPS: needs a mocked clock or a functional
// test (nova-tools #4221)")` -- so the debt is countable, and the count only
// falls.
//
// The SLEEPS ledger (internal/ci/sleeps-skips_allowlist.txt, unitwaits_class_test.go)
// already names every SLEEPS skip in the tree and refuses a row its merge base
// lacks. That refusal reads git -- a merge base with origin/dev, or HEAD's
// first parent -- so it is a verdict about a checkout, not a tree: it is a
// Fatal on a depth-1 checkout, and it is what the functional tier controls.
// This test is the same invariant with no git in it. The ledger carries one
// `# sleeps: <count> <YYYY-MM-DD>` line; the tree's SLEEPS skips are counted
// with the ledger's own reader (treeSleepsSkips, so the two never disagree on
// what a SLEEPS skip is), and a count over the line is red on any tree, whether
// or not the change wrote its ledger row too. A count under the line is red as
// well, with the line to write, because a ratchet that is not tightened when
// the debt falls is a ratchet the next skip fits under.

// sleepsCountPrefix opens the one ledger comment line this test reads.
const sleepsCountPrefix = "# sleeps:"

func TestSleepsSkipsOnlyFall(t *testing.T) {
	t.Parallel()

	count := len(treeSleepsSkips(t))
	ceiling, date := readSleepsCount(t)
	if v := sleepsCountVerdict(count, ceiling, date, time.Now().Format("2006-01-02")); v != "" {
		t.Error(v)
		return
	}
	t.Logf("%d SLEEPS skips, at the ratchet measured %s", count, date)
}

// sleepsCountVerdict is the rule as a pure function of the tree's count and the
// ledger's line, so the DONE-WHEN (red with one added skip, green on dev) is a
// unit test over the real tree and the real ledger. It is "" when the count is
// at the line; today is the date the tightened line carries.
func sleepsCountVerdict(count, ceiling int, date, today string) string {
	switch {
	case count > ceiling:
		return fmt.Sprintf("%d SLEEPS skips under %s, over the %d measured %s in %s; a test that waits on the wall clock is not skipped, it gets an injected clock and asserts the transition, or it becomes a functional test (#4221). The count only falls, and a ledger row written in the same change does not raise it",
			count, strings.Join(unitWaitDirs, "/, ")+"/", ceiling, date, sleepsLedger)
	case count < ceiling:
		return fmt.Sprintf("%d SLEEPS skips under %s, under the %d measured %s in %s; tighten the ratchet in the same change: write `%s %d %s` as the one %s line of %s",
			count, strings.Join(unitWaitDirs, "/, ")+"/", ceiling, date, sleepsLedger, sleepsCountPrefix, count, today, sleepsCountPrefix, sleepsLedger)
	}
	return ""
}

// readSleepsCount reads the ledger's one `# sleeps: <count> <YYYY-MM-DD>` line.
func readSleepsCount(t *testing.T) (int, string) {
	t.Helper()
	n, date, err := parseSleepsCount(readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(sleepsLedger))))
	if err != nil {
		t.Fatalf("%s: %v", sleepsLedger, err)
	}
	return n, date
}

// parseSleepsCount finds the one `# sleeps: <count> <YYYY-MM-DD>` line in the
// ledger's text. No line, two lines, a count that is not a whole number or a
// date that is not YYYY-MM-DD is an error, never a zero.
func parseSleepsCount(text string) (int, string, error) {
	var found []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, sleepsCountPrefix) {
			found = append(found, strings.TrimSpace(strings.TrimPrefix(line, sleepsCountPrefix)))
		}
	}
	if len(found) != 1 {
		return 0, "", fmt.Errorf("carries %d %q lines, want exactly one: `%s <count> <YYYY-MM-DD>`", len(found), sleepsCountPrefix, sleepsCountPrefix)
	}
	f := strings.Fields(found[0])
	if len(f) != 2 {
		return 0, "", fmt.Errorf("%q is not `%s <count> <YYYY-MM-DD>`", sleepsCountPrefix+" "+found[0], sleepsCountPrefix)
	}
	n, err := strconv.Atoi(f[0])
	if err != nil || n < 0 {
		return 0, "", fmt.Errorf("%q is not a count on the %s line", f[0], sleepsCountPrefix)
	}
	if _, err := time.Parse("2006-01-02", f[1]); err != nil {
		return 0, "", fmt.Errorf("%q is not a YYYY-MM-DD date on the %s line", f[1], sleepsCountPrefix)
	}
	return n, f[1], nil
}

// TestSleepsRatchetIsRedOnOneAddedSkipAndGreenOnDev is the DONE-WHEN as a unit
// test over the real tree and the real ledger: the count as it stands is green;
// one more is red, naming the ledger and the rule; one fewer is red with the
// tightened line to write.
func TestSleepsRatchetIsRedOnOneAddedSkipAndGreenOnDev(t *testing.T) {
	t.Parallel()

	count := len(treeSleepsSkips(t))
	ceiling, date := readSleepsCount(t)
	if v := sleepsCountVerdict(count, ceiling, date, "2026-09-26"); v != "" {
		t.Fatalf("dev is not green under the ratchet: %s", v)
	}
	over := sleepsCountVerdict(count+1, ceiling, date, "2026-09-26")
	if !strings.Contains(over, fmt.Sprintf("%d SLEEPS skips", count+1)) || !strings.Contains(over, "over the") || !strings.Contains(over, "The count only falls") || !strings.Contains(over, sleepsLedger) {
		t.Fatalf("one added skip must be red, naming the count, the ledger and the rule: %q", over)
	}
	under := sleepsCountVerdict(count-1, ceiling, date, "2026-09-26")
	want := fmt.Sprintf("`%s %d 2026-09-26`", sleepsCountPrefix, count-1)
	if !strings.Contains(under, "under the") || !strings.Contains(under, want) {
		t.Fatalf("one removed skip must be red with the line to write %s: %q", want, under)
	}
}

// TestSleepsCountLineIsExactlyOne pins the line's reader: the one shape it
// accepts, and each way a ledger can carry no usable count.
func TestSleepsCountLineIsExactlyOne(t *testing.T) {
	t.Parallel()

	n, date, err := parseSleepsCount("# a header\n  # sleeps:  85   2026-09-26  \ncmd/a\tTestX\twhere\n")
	if err != nil || n != 85 || date != "2026-09-26" {
		t.Errorf("the good line: %d %q %v; want 85 2026-09-26", n, date, err)
	}
	for name, text := range map[string]string{
		"no line":     "# a header\ncmd/a\tTestX\twhere\n",
		"two lines":   "# sleeps: 85 2026-09-26\n# sleeps: 84 2026-09-27\n",
		"no date":     "# sleeps: 85\n",
		"bad count":   "# sleeps: eighty-five 2026-09-26\n",
		"negative":    "# sleeps: -1 2026-09-26\n",
		"bad date":    "# sleeps: 85 26/09/2026\n",
		"three words": "# sleeps: 85 2026-09-26 measured\n",
	} {
		if _, _, err := parseSleepsCount(text); err == nil {
			t.Errorf("%s: read a count from %q; want an error", name, text)
		}
	}
	if _, _, err := parseSleepsCount("## sleeps: 85 2026-09-26\n#sleeps: 85 2026-09-26\n"); err == nil {
		t.Error("a line spelt `## sleeps:` or `#sleeps:` was read as the count line; there is one spelling")
	}
}
