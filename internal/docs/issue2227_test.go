package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2227 proves the behaviours nova-tools#2227 demands of the spec. A
// pod appends a row to a shared usage.tsv, and its last step is to run the
// clip.
func TestIssue2227(t *testing.T) {
	t.Parallel()

	const want = "The pod's stdout is the harness log, raw, appended to the same usage.tsv the launcher created. The row holds the pod's own start time, end time, exit code, and cost. After each card the pod's last step is the clip"
	const path = "../../docs/SPEC-FLEET-KUBE.md"

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v; this test proves a section of the spec, so the file must be readable", path, err)
	}

	// The spec is prose, so the test is a string search. The wanted string is
	// one sentence, so it should not be split across paragraphs.
	//
	// A join/split simplifies the check by letting the wanted string span
	// multiple lines in the source file, as long as it is one sentence.
	condensed := strings.ReplaceAll(string(body), "\n", " ")
	condensed = strings.Join(strings.Fields(condensed), " ")

	if !strings.Contains(condensed, want) {
		t.Errorf(`%s: does not contain the sentence %q`, path, want)
	}
}
