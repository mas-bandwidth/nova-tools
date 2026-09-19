package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ISSUE #1923, the table half. The label column is joined into a path by every
// consumer: batch makes <root>/<scratch>/jobs/<label>, selfNative hands it to
// `native` as --label, and native joins it again into the job directory, the temp
// directory and the wall's write set. A row whose label is `../../../OUTSIDE` is a
// card naming a directory outside the swarm root. The bound: the label column holds
// a NAME, and a table that breaks it queues nothing -- a batch is all of its cards
// or none, so a walking label is a refusal at the parse with its line named.
func TestReadCardsRefusesALabelThatIsAPath(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "card.md")
	if err := os.WriteFile(card, []byte("do the thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"../../../OUTSIDE", "..", "a/b", "-rf"} {
		t.Run(label, func(t *testing.T) {
			tsv := filepath.Join(dir, "cards.tsv")
			body := "good-card\t1\tfake/fake-model\t" + card + "\n" +
				label + "\t2\tfake/fake-model\t" + card + "\n"
			if err := os.WriteFile(tsv, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := readCards(tsv)
			if err == nil {
				t.Fatalf("readCards admitted a label that is a path; it queued %d cards", len(got))
			}
			if !strings.Contains(err.Error(), "line 2") {
				t.Errorf("the refusal does not name the line: %v", err)
			}
			if got != nil {
				t.Errorf("a refused table still queued %d cards", len(got))
			}
		})
	}
	// The honest table still loads, so the refusal is about the shape of the name.
	tsv := filepath.Join(dir, "ok.tsv")
	if err := os.WriteFile(tsv, []byte("good-card.1_a\t1\tfake/fake-model\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cards, err := readCards(tsv)
	if err != nil || len(cards) != 1 || cards[0].label != "good-card.1_a" {
		t.Fatalf("an ordinary label no longer loads: cards=%v err=%v", cards, err)
	}
}
