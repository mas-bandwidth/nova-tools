package docs

import (
	"os"
	"strings"
	"testing"
)

// tests_slowtests_slowest_list_test.go holds docs/TESTS.md's nova-ci first run
// to the sentence that explains the `slowest=` list: "the `slowest=` list names
// the few test-level rows that spent it, so the first run tells the reader
// whether one test or the whole package is the cost." The fixture behind that
// run carries two test-level rows — TestSlowThing and TestAlsoSlow — and the
// demanded over-budget line names BOTH. That is the sentence's whole point: a
// list that named only the single slowest test could not tell the reader
// whether one test or the whole package was the cost. The test is named for the
// second row, the one a reader meets only if the promise holds: it goes red if
// TestAlsoSlow ever leaves the documented slowest list.

// testsMDPath is the transcripts the tests execute, relative to this package.
const testsMDPath = "../../docs/TESTS.md"

// TestAlsoSlow pins the sentence at docs/TESTS.md that the `slowest=` list names
// the few test-level rows that spent the package total: the `--budget 60` line
// of the nova-ci first run must name the fixture's second test-level row,
// TestAlsoSlow, alongside TestSlowThing.
func TestAlsoSlow(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(testsMDPath)
	if err != nil {
		t.Fatalf("%s: %v", testsMDPath, err)
	}

	_, rest, found := strings.Cut(string(body), "\n## nova-ci\n")
	if !found {
		t.Fatalf("%s has no `## nova-ci` section; this test is reading the wrong document", testsMDPath)
	}
	section, _, _ := strings.Cut(rest, "\n## ")

	lines := strings.Split(section, "\n")
	slowLine := ""
	for i, line := range lines {
		if !strings.HasPrefix(line, "$ nova-ci slowtests --budget 60 ") {
			continue
		}
		for _, out := range lines[i+1:] {
			if strings.TrimSpace(out) == "" {
				continue
			}
			slowLine = out
			break
		}
		break
	}
	if slowLine == "" {
		t.Fatalf("the nova-ci first run in %s has no `$ nova-ci slowtests --budget 60` command, so the sentence about the `slowest=` list demonstrates nothing", testsMDPath)
	}
	if !strings.HasPrefix(slowLine, "CI-SLOW package=") {
		t.Fatalf("the `--budget 60` command's output is %q, want a CI-SLOW line naming the over-budget package and its slowest tests", slowLine)
	}
	slowest, ok := strings.CutPrefix(slowLine, "CI-SLOW package=github.com/mas-bandwidth/nova-tools/internal/example seconds=65.1s budget=60s slowest=")
	if !ok {
		t.Fatalf("the demanded CI-SLOW line %q changed shape; the sentence promises the `slowest=` list names the few test-level rows, and this test reads it off the documented line", slowLine)
	}

	// "the few test-level rows" is plural, so the list must carry the second
	// row and not stop at the single slowest test. TestAlsoSlow is that row.
	rows := strings.Split(slowest, ",")
	if len(rows) < 2 {
		t.Errorf("the `slowest=` list is %q — one row only; a list that names only the single slowest test cannot tell the reader whether one test or the whole package is the cost", slowest)
	}
	if !strings.Contains(slowest, "TestAlsoSlow:1.5s") {
		t.Errorf("the `slowest=` list %q names no TestAlsoSlow:1.5s; the sentence says the list names the few test-level rows that spent the package total, and the fixture's second row has left it", slowest)
	}
}
