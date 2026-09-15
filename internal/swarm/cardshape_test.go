package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCardsTSV writes one TSV line naming a single card and returns the TSV path.
func writeCardsTSV(t *testing.T, dir, label, model, card string) string {
	t.Helper()
	path := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(path, []byte(label+"\t1\t"+model+"\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// admitWhyOf runs readCards against one card and returns the card's admission refusal --
// the text after "ADMIT REFUSED <label> " -- or "" when the card was admitted.
func admitWhyOf(t *testing.T, dir, label, model, cardBody string) string {
	t.Helper()
	card := writeCard(t, dir, label+".card", cardBody)
	tsv := writeCardsTSV(t, dir, label, model, card)
	cards, err := readCards(tsv)
	if err != nil {
		t.Fatalf("the TSV is readable, got %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("admitted %d cards, want 1", len(cards))
	}
	return cards[0].admitWhy
}

func TestAdmitRefusesCapitalisedContractForDeepSeek(t *testing.T) {
	why := admitWhyOf(t, t.TempDir(), "c1", "opencode/deepseek-v4-flash",
		"THIS IS A CAPITALISED CONTRACT BLOCK\nSTEP 1 clone the repo\n")
	if why == "" {
		t.Fatal("a capitalised contract for a DeepSeek model is refused, got no refusal")
	}
	line := admitRefusalLine("c1", why)
	if !strings.HasPrefix(line, "ADMIT REFUSED c1 card-shape: capitalised contract block") {
		t.Fatalf("refusal names the shape: %q", line)
	}
	if !strings.Contains(line, "docs/WORKER-CARDS.md practice 17") {
		t.Fatalf("refusal cites practice 17: %q", line)
	}
}

func TestAdmitAcceptsNumberedStepsForDeepSeek(t *testing.T) {
	why := admitWhyOf(t, t.TempDir(), "c2", "deepseek/v4-flash",
		"RESULT: do the work\nSTEP 1 clone the repo\nSTEP 2 edit the file\n")
	if why != "" {
		t.Fatalf("a numbered-steps card for a DeepSeek model is admitted, got %q", why)
	}
}

func TestAdmitDoesNotCheckMercury(t *testing.T) {
	// A Mercury card in every shape a DeepSeek card would be refused for is admitted.
	body := "THIS IS A CAPITALISED CONTRACT BLOCK\nlauncher text here\n"
	if why := admitWhyOf(t, t.TempDir(), "c3", "inception/mercury-2.5", body); why != "" {
		t.Fatalf("a Mercury card is not shape-checked, got %q", why)
	}
}
