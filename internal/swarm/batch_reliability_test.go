package swarm

import (
	"strings"
	"testing"
)

// The reliability tests for the three pit-stop faults of 2026-09-15: a batch that lost 34
// cards to one card's admission refusal (#529), two batches that shared a slot and lost both
// cards (#457), and an abstain row that named no reason, which cost the coordinator a RESULT
// read per card (#461). Each test is named after the sentence the fault broke.

// deepSeekCard is a well-shaped DeepSeek card: a lowercase contract line, a role line and
// STEP 1 with the clone, then whatever body the test hands it.
func deepSeekCard(label, body string) string {
	return "RESULT: " + label + "\n" +
		"You are a worker on nova-tools.\n" +
		"STEP 1 cd /tmp/work && git clone https://example.invalid/repo.git\n" +
		body
}

// ISSUE #529: the practice-17 card-shape word check applies to lines 1-3 -- the contract
// line, the role line and STEP 1 -- not to quoted text further down the card. A card that
// quotes an issue mentioning a launcher is admitted; a card that tells the worker to use a
// launcher in its contract lines is still refused.
func TestCardShapeCheckCoversContractLinesOnly(t *testing.T) {
	t.Parallel()

	quoted := deepSeekCard("q",
		"STEP 2 read the issue below and make it true.\n"+
			"STEP 3 the issue says: \"the launcher shim is not the tool; fix the card\"\n"+
			"STEP last write RESULT.md: line 1 is line 1 of this card.\n")
	if why := admitWhyOf(t, t.TempDir(), "q", "opencode/deepseek-v4-flash", quoted); why != "" {
		t.Fatalf("a card whose quoted text mentions a launcher below line 3 is admitted, got %q", why)
	}
	inContract := "RESULT: r\n" +
		"Run the card through the launcher.\n" +
		"STEP 1 cd /tmp/work && git clone https://example.invalid/repo.git\n"
	why := admitWhyOf(t, t.TempDir(), "r", "opencode/deepseek-v4-flash", inContract)
	if !strings.Contains(why, "'launcher' in the contract lines") {
		t.Fatalf("a launcher named in lines 1-3 is still refused, got %q", why)
	}
	if !strings.Contains(why, "docs/WORKER-CARDS.md practice 17") {
		t.Fatalf("the refusal cites practice 17: %q", why)
	}
}
