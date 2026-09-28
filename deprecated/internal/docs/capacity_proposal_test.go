package docs

import (
	"os"
	"strings"
	"testing"
)

// TestCapacityProposalPreserved pins the durable home of the capacity,
// throughput and token-join proposal (#151). Glenn asked to preserve it for
// future analysis and team discussion without stopping current implementation
// to build it, so a missing file, a lost fence or a lost question of record is
// a bug: the document is the record, and the issue tracker is not.
func TestCapacityProposalPreserved(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/PROPOSAL-CAPACITY.md")
	if err != nil {
		t.Fatalf("docs/PROPOSAL-CAPACITY.md: %v", err)
	}
	content := string(body)

	// The heading is the issue's own title, so the document is findable from
	// the issue it preserves, and the status line must say what this is not,
	// in the issue's own words.
	if !strings.Contains(content, "# Discover capacity, measure useful throughput, and join per-friend/model token accounting") {
		t.Error("missing heading: # Discover capacity, measure useful throughput, and join per-friend/model token accounting")
	}
	if !strings.Contains(content, "not an implementation assignment or an approved tool specification") {
		t.Error("missing status fence: the proposal must state it is not an implementation assignment or an approved tool specification")
	}

	// The four questions of record, one anchor phrase each.
	for _, question := range []string{
		"What capacity does each friend choose to offer",
		"How much is available now",
		"Are we completing useful, validated work faster",
		"What tokens did each friend and model spend",
	} {
		if !strings.Contains(content, question) {
			t.Errorf("missing question of record: %q", question)
		}
	}

	// The census is the immediate low-cost step, and its row shape is its
	// contract: one timestamped table per friend with these columns.
	for _, column := range []string{
		"pool/model/harness/bench",
		"executing and waiting",
		"immediately available capacity and total chosen limit",
		"useful task types and constraints",
	} {
		if !strings.Contains(content, column) {
			t.Errorf("missing census column: %q", column)
		}
	}

	// The fences: what this proposal authorizes nobody to build.
	for _, fence := range []string{
		"No new watcher or automatic dispatch",
		"Never collect credentials or private prompts",
	} {
		if !strings.Contains(content, fence) {
			t.Errorf("missing fence: %q", fence)
		}
	}

	// The join rules that keep the accounting honest.
	for _, rule := range []string{
		"Missing usage is not zero",
		"Silence is pending",
	} {
		if !strings.Contains(content, rule) {
			t.Errorf("missing join rule: %q", rule)
		}
	}

	// Glenn's sequence, verbatim, and the observation that motivated the
	// proposal: both are evidence, and evidence keeps its date.
	if !strings.Contains(content, "Practice first, learn, then tool") {
		t.Error("missing the sequence of record: Practice first, learn, then tool")
	}
	if !strings.Contains(content, "2026-09-12 17:59:50Z") {
		t.Error("missing the observation of record: 2026-09-12 17:59:50Z")
	}

	// The related work this proposal joins to, so the reader can find the
	// accounting trail from here: cost per completed task and capacity.
	for _, issue := range []string{"#64", "#176"} {
		if !strings.Contains(content, issue) {
			t.Errorf("missing the related-issue pointer: %s", issue)
		}
	}
}
