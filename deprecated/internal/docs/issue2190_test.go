package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2190 pins nova-tools #2190 — "Stand up the space slice and its
// three red tests" — against docs/SPEC-LOGS.md Part 5. The slice itself (Loki,
// Alloy and Grafana on the bench `space`) is deployed on the bench, not in
// this tree, and the class rules refuse its live tests on the CI path (`net`:
// no test names a real host; `waits`: no fixed wall-clock wait; `wall clock`:
// no bound under ten seconds). So the slice's tests are stood up the way this
// repo stands up a red test that cannot run here yet: named in Part 5 as
// tests of record, with the bench shape that makes each runnable where it
// lives. This test holds Part 5 to that text; it was red on 09fbedc90521,
// the base of #2190, where Part 5 carried no build order, no checks of
// record and no bench shape, and the two integration red tests stood with no
// runnable protocol.
func TestIssue2190(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-LOGS.md")
	if err != nil {
		t.Fatalf("docs/SPEC-LOGS.md: %v", err)
	}
	content := string(body)

	// Part 5 is the slice's own section, and the demanded list below it keeps
	// the numbering #2190's COVERS refers to.
	const heading = "## Part 5 — the first slice on `space`, its measure, and the red tests"
	at := strings.Index(content, heading)
	if at < 0 {
		t.Fatalf("missing the Part 5 heading: %s", heading)
	}
	const demanded = "## Tests this spec demands"
	end := strings.Index(content, demanded)
	if end < 0 || end < at {
		t.Fatalf("missing the demanded-tests heading after Part 5: %s", demanded)
	}
	part5 := content[at:end]

	// The receipt #2190 quotes: the slice on space, the measure, the red tests.
	for _, receipt := range []string{
		"**The slice, on one bench (`space`).**",
		"Loki with local disk storage, one Alloy reading the",
		"fleet width, queue depth, cards per hour, minutes per card, and free disk per bench",
		"**Seconds to answer \"why is card X hung\"**",
		"The slice is done when that number is printed, not when Loki is installed.",
		"a-slog-line-from-a-verb-appears-in-loki-within-five-seconds-with-its-labels",
		"the-hung-card-query-returns-the-card-s-last-event",
	} {
		if !strings.Contains(part5, receipt) {
			t.Errorf("Part 5 lost the text of record #2190 quotes: %q", receipt)
		}
	}

	// The build order the issue asks for.
	if !strings.Contains(part5, "Slice, dashboard, measure, red tests") {
		t.Error("Part 5 is missing the build order of record: slice, dashboard, measure, red tests")
	}

	// The four checks #2190 names, stood up in Part 5 as the slice's checks of
	// record — at base they stood only in the demanded list, marked (ABSENT).
	if !strings.Contains(part5, "The slice's checks of record") {
		t.Error("Part 5 is missing the checks-of-record block that stands the slice's tests up")
	}
	for _, check := range []string{
		"TestSliceRunsLokiAlloyGrafanaOnSpace",
		"TestGrafanaHasTheOneDashboard",
		"TestSliceIsAdditive",
		"TestScopeAndMeasureUnderFiveSeconds",
	} {
		if !strings.Contains(part5, check) {
			t.Errorf("Part 5 is missing the slice's check of record: %s", check)
		}
	}

	// The bench shape that makes the red tests runnable where they live: the
	// class rules' own remedies, not a live shape the CI path refuses.
	for _, shape := range []string{
		"On the bench (`space`), never on the CI path",
		"polls for the line up to `NOVA_TEST_WAIT`",
		"the measure of record the bench prints, never a CI assertion",
		"asserts the machine's load, not the code",
	} {
		if !strings.Contains(part5, shape) {
			t.Errorf("Part 5 is missing the bench shape of record: %q", shape)
		}
	}

	// The demanded list keeps the numbering #2190's COVERS refers to: 31 to 35
	// and 37 (36 is the secret-value test item 6 already covers).
	list := content[end:]
	for _, item := range []string{
		"31. `TestSliceRunsLokiAlloyGrafanaOnSpace`",
		"32. `TestGrafanaHasTheOneDashboard`",
		"33. `TestSliceIsAdditive`",
		"34. `TestScopeAndMeasureUnderFiveSeconds`",
		"35. `a-slog-line-from-a-verb-appears-in-loki-within-five-seconds-with-its-labels`",
		"37. `the-hung-card-query-returns-the-card-s-last-event`",
	} {
		if !strings.Contains(list, item) {
			t.Errorf("the demanded list lost the item #2190 covers: %s", item)
		}
	}
}
