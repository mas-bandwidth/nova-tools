package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// card636 writes a practice-17 shaped card and a one-row cards.tsv naming it, with the
// slot column set to "-" so the batch allocates one.
func card636(t *testing.T, dir, label string) string {
	t.Helper()
	card := filepath.Join(dir, label+".md")
	body := "RESULT " + label + " sha=000000000000\nYou are a worker.\n" +
		"STEP 1. mkdir -p scratch && export TMPDIR=$PWD/scratch && git clone -q https://example.com/mas-bandwidth/nova-tools.git . && git checkout -b rowan/" + label + "\n" +
		"STEP 2. write RESULT.md\n"
	if err := os.WriteFile(card, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tsv := filepath.Join(dir, label+"-cards.tsv")
	if err := os.WriteFile(tsv, []byte(label+"\t-\topencode/deepseek-v4-flash\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tsv
}

// TestCard8909BatchRefusesWithNeitherRunnerNorHarness: one refusal naming both doors.
func TestCard8909BatchRefusesWithNeitherRunnerNorHarness(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tsv := card636(t, dir, "card-g")
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		Tokens: "unmetered",
		ID:     "TP1", Deadline: 10 * time.Second, Cards: tsv, Root: filepath.Join(dir, "root"),
		Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--runner or --harness is required") {
		t.Fatalf("the refusal names neither door: %q", errb.String())
	}
}
