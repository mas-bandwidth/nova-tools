package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeJob lays down one job directory under a root, the way a runner does: <root>/<slot>/
// jobs/<label>/ with the files it names.
func writeJob(t *testing.T, root, slot, label string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, slot, "jobs", label)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func harvestIn(t *testing.T, in HarvestInput) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout, in.Stderr = &out, &errb
	if in.Max == 0 {
		in.Max = 20
	}
	code := Harvest(in)
	return code, out.String(), errb.String()
}

// TestHarvestFoldsARootItDidNotCut is issue #628's red test: harvest read <root>/cards.tsv
// and refused any root without one -- "HARVEST REFUSED: cannot read <root>/cards.tsv" --
// so the root the hand loop actually fills (nova-swarm batch straight from bin/pulse-loop.sh)
// could only be harvested by the shim. The non-test line it needs is harvest.go's
// harvestCards, which falls through launch.tsv to cards.tsv to the job directories.
func TestHarvestFoldsARootItDidNotCut(t *testing.T) {
	root := t.TempDir()
	writeJob(t, root, "1", "card-700", map[string]string{
		"RESULT.md": "RESULT card-700 sha=abc\nABSTAIN reason=deadline\n",
		"usage.tsv": "job\tusd\ncard-700\t0.0249\n",
	})
	writeJob(t, root, "2", "card-744", map[string]string{
		"RESULT.md": "RESULT card-744 sha=def\nABSTAIN reason=no-result\n",
		"usage.tsv": "job\tusd\ncard-744\t0.0249\n",
	})

	code, out, _ := harvestIn(t, HarvestInput{Root: root})
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (two abstains); out=%q", code, out)
	}
	if !strings.Contains(out, "source=jobs") || !strings.Contains(out, "abstain=2") {
		t.Fatalf("harvest did not fold the root's own job dirs: %q", out)
	}
	// #12: the reason token the packet scored, not the runner's NATIVE line.
	if !strings.Contains(out, "HARVEST RETRY label=card-700 reason=deadline") {
		t.Fatalf("the abstain reason token is not on the card's line: %q", out)
	}
	// #12: the spend is the cards' own usage.tsv rows, summed.
	if !strings.Contains(out, "usd=0.0498") {
		t.Fatalf("the batch's spend was lost: %q", out)
	}
}

// TestHarvestMarksALaunchedCardWithNoJobDirAsAnOrphan is the 23:36Z class: the batch was
// admitted, no job directory was ever made, and a coordinator read the silent root as idle.
// Red without the orphan branch in Harvest: the card scored `abstain` with reason `-` and
// was requeued as though a harness had refused it.
func TestHarvestMarksALaunchedCardWithNoJobDirAsAnOrphan(t *testing.T) {
	root := t.TempDir()
	long := time.Now().Add(-10 * time.Minute).UTC().Format("2006-01-02T15:04:05Z")
	rows := fmt.Sprintf("TP1\tcard-900\t-\tspace\tm\tabc123\t%s\n", long)
	if err := os.WriteFile(filepath.Join(root, "launch.tsv"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := harvestIn(t, HarvestInput{ID: "TP1", Root: root})
	if code != 1 {
		t.Fatalf("exit=%d, want 1", code)
	}
	if !strings.Contains(out, "HARVEST ORPHAN label=card-900 bench=space") {
		t.Fatalf("the orphan is not named: %q", out)
	}
	if !strings.Contains(out, "orphan=1") || !strings.Contains(out, "source=launch.tsv") {
		t.Fatalf("the HARVEST line does not count the orphan: %q", out)
	}
}

// TestHarvestPrintsThePublishCommandForABranchCard: pushing and opening a PR stays with
// nova-swarm publish. Without --publish the command is printed, and nothing is pushed.
// Red without publishCommand and the Publish branch in Harvest.
func TestHarvestPrintsThePublishCommandForABranchCard(t *testing.T) {
	root := t.TempDir()
	writeJob(t, root, "1", "card-801", map[string]string{
		"RESULT.md": "RESULT card-801 sha=abc\nDONE\nBRANCH rowan/fix-801\nREPO mas-bandwidth/nova-tools\n",
	})
	code, out, _ := harvestIn(t, HarvestInput{Root: root})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%q", code, out)
	}
	if !strings.Contains(out, "HARVEST BRANCH label=card-801 branch=rowan/fix-801 job=") {
		t.Fatalf("the BRANCH card is not named: %q", out)
	}
	if !strings.Contains(out, "HARVEST PUBLISH nova-swarm publish --job ") {
		t.Fatalf("the publish command is not printed per BRANCH card: %q", out)
	}
	if !strings.Contains(out, "--branch rowan/fix-801") || !strings.Contains(out, "--base main") {
		t.Fatalf("the publish command is not runnable as printed: %q", out)
	}
}
