package swarm

import (
	"errors"
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

// admit runs readCards against one card and returns its error, or nil when it was admitted.
func admit(t *testing.T, dir, label, model, cardBody string) error {
	t.Helper()
	card := writeCard(t, dir, label+".card", cardBody)
	tsv := writeCardsTSV(t, dir, label, model, card)
	cards, err := readCards(tsv)
	if err == nil && len(cards) != 1 {
		t.Fatalf("admitted %d cards, want 1", len(cards))
	}
	return err
}

var asAdmit = func(err error) *admitError {
	var ae *admitError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

func TestAdmitRefusesCapitalisedContractForDeepSeek(t *testing.T) {
	err := admit(t, t.TempDir(), "c1", "opencode/deepseek-v4-flash",
		"THIS IS A CAPITALISED CONTRACT BLOCK\nSTEP 1 clone the repo\n")
	ae := asAdmit(err)
	if ae == nil {
		t.Fatalf("a capitalised contract for a DeepSeek model is refused, got %v", err)
	}
	if !strings.HasPrefix(ae.Error(), "ADMIT REFUSED c1 card-shape: capitalised contract block") {
		t.Fatalf("refusal names the shape: %q", ae.Error())
	}
	if !strings.Contains(ae.Error(), "docs/WORKER-CARDS.md practice 17") {
		t.Fatalf("refusal cites practice 17: %q", ae.Error())
	}
}

func TestAdmitAcceptsNumberedStepsForDeepSeek(t *testing.T) {
	err := admit(t, t.TempDir(), "c2", "deepseek/v4-flash",
		"RESULT: do the work\nSTEP 1 clone the repo\nSTEP 2 edit the file\n")
	if err != nil {
		t.Fatalf("a numbered-steps card for a DeepSeek model is admitted, got %v", err)
	}
}

func TestAdmitDoesNotCheckMercury(t *testing.T) {
	// A Mercury card in every shape a DeepSeek card would be refused for is admitted.
	body := "THIS IS A CAPITALISED CONTRACT BLOCK\nlauncher text here\n"
	err := admit(t, t.TempDir(), "c3", "inception/mercury-2.5", body)
	if err != nil {
		t.Fatalf("a Mercury card is not shape-checked, got %v", err)
	}
}
