package pulse

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// addPoolCard writes a card file and its cards.tsv row with NO slot job
// directory, plus a pool task in done/ or failed/ and its report copy in
// pool/reports/<id>/RESULT.md -- the layout nova-swarm run leaves behind.
func addPoolCard(t *testing.T, root, label, state, id, contract, result string) {
	t.Helper()
	cardDir := filepath.Join(root, "cardsrc")
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(cardDir, label+".md")
	// The card names its own repository: a harvest checks the RESULT.md's REPO line
	// against the card before it pushes anywhere, because a RESULT is a report and not
	// an instruction (issue #1824). Every card these tests fold is for owner/repo.
	if err := os.WriteFile(cardPath, []byte(contract+"\nREPO owner/repo\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pool := filepath.Join(root, "pool")
	if err := os.MkdirAll(filepath.Join(pool, state), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pool, "reports", id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pool, state, id+".task"), []byte(contract+"\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pool, state, id+".json"), []byte(`{"id":`+strconv.Quote(id)+`,"label":"pulse-p1","files":40,"tokens":100,"deadline":"300s","batch":"b1","rc":-1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pool, "reports", id, "RESULT.md"), []byte(result), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, "cards.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(label + "\t1\tflash\t" + cardPath + "\n")
}

func TestHarvestReadsPoolLayout(t *testing.T) {
	root, specs, arglog := setupPulse(t)

	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/77")

	addPoolCard(t, root, "X", "done", "20260917T010500Z-x-aaaaaa",
		"RESULT X sha=xxx",
		"RESULT X sha=xxx\nDONE\nBRANCH bx\nREPO owner/repo\n")
	// A task in failed/ whose RESULT.md exists with a first line equal to the
	// card's RESULT line is harvested as a result, not a failure.
	addPoolCard(t, root, "Y", "failed", "20260917T010600Z-y-bbbbbb",
		"RESULT Y sha=yyy",
		"RESULT Y sha=yyy\nDONE\nBRANCH by\nREPO owner/repo\n")

	out, _ := runHarvest(t, root)
	if !strings.Contains(out, "HARVEST PR repo=owner/repo pr=77 label=X branch=bx") {
		t.Fatalf("pool card X must be harvested from pool/reports, got:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST PR repo=owner/repo pr=77 label=Y branch=by") {
		t.Fatalf("pool card Y in failed/ with a RESULT must be harvested as a result, got:\n%s", out)
	}
	if !strings.Contains(out, "pushed=2") {
		t.Fatalf("want pushed=2, got:\n%s", out)
	}
	if !strings.Contains(out, "prs=2") {
		t.Fatalf("want prs=2, got:\n%s", out)
	}
}
