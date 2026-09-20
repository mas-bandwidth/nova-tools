package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #1907. The receipt: launch --bench pulls RESULT.md to <root>/<bench>-<n>/jobs/<label>/
// (swarm scratchName). Launch writes the admitted table with slot `-`. Harvest --then is
// `nova-pulse harvest --id <id> --root <root>` and jobDir("-") is <root>/0/jobs/<label>.
// Those are two directories. The card is scored elsewhere=1 and never folds, even though
// the files are already here.
func TestHarvestThenFoldsPulledBenchScratchWhenSlotIsDash(t *testing.T) {
	root := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(root, "argv.log")
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/1907")

	id := "20260919T160000Z-pulse-5c8b00"
	card := filepath.Join(root, "cardsrc", "card-a.md")
	if err := os.MkdirAll(filepath.Dir(card), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(card, []byte("RESULT card-a sha=000000000000\nREPO owner/repo\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	admitted := pulseCardsPath(root, id)
	if err := os.MkdirAll(filepath.Dir(admitted), 0o755); err != nil {
		t.Fatal(err)
	}
	// Exactly what launch's writeCardsTSV leaves: slot `-`, never the allocated bench slot.
	if err := os.WriteFile(admitted, []byte("card-a\t-\tflash\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(root, "studio-1", "jobs", "card-a")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"),
		[]byte("RESULT card-a sha=000000000000\nDONE\nBRANCH rowan/card-a\nREPO owner/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "0", "jobs", "card-a")); err == nil {
		t.Fatal("this fixture must have no <root>/0/jobs/card-a: that is the point")
	}

	var out, errb bytes.Buffer
	code := Harvest(HarvestInput{ID: id, Root: root, MaxBodyBytes: 4096, Max: 20, Stdout: &out, Stderr: &errb})

	if strings.Contains(errb.String(), "ran somewhere else") {
		t.Fatalf("harvest --then treated the pulled <bench>-<n> job as elsewhere (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "elsewhere=1") {
		t.Fatalf("the pulled RESULT.md sat at studio-1/jobs/card-a and harvest looked at 0/:\n%s\n%s", out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "done=1") || !strings.Contains(out.String(), "pushed=1") {
		t.Fatalf("the --then harvest did not fold the pulled bench scratch (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
}

func TestJobDirMapsDashSlotToPulledBenchScratch(t *testing.T) {
	root := t.TempDir()
	pulled := filepath.Join(root, "studio-1", "jobs", "card-a")
	if err := os.MkdirAll(pulled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pulled, "RESULT.md"), []byte("RESULT card-a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := jobDir(root, "-", "card-a")
	if got != pulled {
		t.Fatalf("jobDir(root, \"-\", \"card-a\") = %s, want the pulled path %s", got, pulled)
	}
}

func TestJobDirMapsBenchColonSlotToScratchName(t *testing.T) {
	root := t.TempDir()
	pulled := filepath.Join(root, "space-2", "jobs", "card-b")
	if err := os.MkdirAll(pulled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pulled, "RESULT.md"), []byte("RESULT card-b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := jobDir(root, "space:2", "card-b")
	if got != pulled {
		t.Fatalf("jobDir(root, \"space:2\", \"card-b\") = %s, want scratchName path %s", got, pulled)
	}
}

func TestJobDirDashWithNoJobStaysAtZero(t *testing.T) {
	root := t.TempDir()
	got := jobDir(root, "-", "card-a")
	want := filepath.Join(root, "0", "jobs", "card-a")
	if got != want {
		t.Fatalf("jobDir with no pulled RESULT.md = %s, want the named 0 path %s", got, want)
	}
}

func writeJobResult(t *testing.T, root, slot, label, body string, when time.Time) string {
	t.Helper()
	dir := filepath.Join(root, slot, "jobs", label)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "RESULT.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Stella's HOLD on #2120: slot `-` accepted any existing 0/jobs/<label>/RESULT.md
// before looking at the pulled <bench>-<n> path, so a leftover local result hid
// the current bench pull for the same label.
func TestJobDirDoesNotPreferStaleZeroOverPulledResult(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cur := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	stale := writeJobResult(t, root, "0", "card-a", "RESULT card-a sha=old\nDONE\nBRANCH rowan/stale\n", old)
	pulled := writeJobResult(t, root, "studio-1", "card-a", "RESULT card-a sha=new\nDONE\nBRANCH rowan/card-a\n", cur)
	got := jobDir(root, "-", "card-a")
	if got == stale {
		t.Fatalf("jobDir picked stale 0/jobs/card-a over current studio-1/jobs/card-a")
	}
	if got != pulled {
		t.Fatalf("jobDir(root, \"-\", \"card-a\") = %s, want the current pulled path %s", got, pulled)
	}
}

func TestJobDirPicksNewerOfTwoPulledSameLabel(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cur := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	stale := writeJobResult(t, root, "space-2", "card-a", "RESULT card-a sha=old\nDONE\nBRANCH rowan/stale\n", old)
	pulled := writeJobResult(t, root, "studio-1", "card-a", "RESULT card-a sha=new\nDONE\nBRANCH rowan/card-a\n", cur)
	got := jobDir(root, "-", "card-a")
	if got == stale {
		t.Fatalf("jobDir picked older space-2/jobs/card-a (ReadDir order) over current studio-1")
	}
	if got != pulled {
		t.Fatalf("jobDir(root, \"-\", \"card-a\") = %s, want the newer pulled path %s", got, pulled)
	}
}

func TestJobDirDoesNotFoldStalePullWhenLiveJobIsNewer(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cur := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	writeJobResult(t, root, "studio-1", "card-a", "RESULT card-a sha=old\nDONE\nBRANCH rowan/stale\n", old)
	live := filepath.Join(root, "0", "jobs", "card-a")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(live, cur, cur); err != nil {
		t.Fatal(err)
	}
	got := jobDir(root, "-", "card-a")
	if got != live {
		t.Fatalf("jobDir(root, \"-\", \"card-a\") = %s, want the live job %s, not the stale pull", got, live)
	}
}

func TestHarvestThenDoesNotFoldStaleZeroOverPulledResult(t *testing.T) {
	root := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(root, "argv.log")
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/1907")

	id := "20260919T160000Z-pulse-5c8b00"
	card := filepath.Join(root, "cardsrc", "card-a.md")
	if err := os.MkdirAll(filepath.Dir(card), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(card, []byte("RESULT card-a sha=000000000000\nREPO owner/repo\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	admitted := pulseCardsPath(root, id)
	if err := os.MkdirAll(filepath.Dir(admitted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(admitted, []byte("card-a\t-\tflash\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cur := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	writeJobResult(t, root, "0", "card-a",
		"RESULT card-a sha=000000000000\nDONE\nBRANCH rowan/stale\nREPO owner/repo\n", old)
	writeJobResult(t, root, "studio-1", "card-a",
		"RESULT card-a sha=000000000000\nDONE\nBRANCH rowan/card-a\nREPO owner/repo\n", cur)

	var out, errb bytes.Buffer
	code := Harvest(HarvestInput{ID: id, Root: root, MaxBodyBytes: 4096, Max: 20, Stdout: &out, Stderr: &errb})
	if strings.Contains(out.String(), "branch=rowan/stale") {
		t.Fatalf("harvest folded the stale 0/jobs RESULT (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "branch=rowan/card-a") || !strings.Contains(out.String(), "pushed=1") {
		t.Fatalf("harvest did not fold the current studio-1 RESULT (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
}
