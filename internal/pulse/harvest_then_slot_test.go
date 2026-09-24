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
	got, n := resolveJobDir(root, "-", "card-a", "RESULT card-a")
	if n != 1 || got != pulled {
		t.Fatalf("resolveJobDir(...) = %s matches=%d, want unique pulled path %s", got, n, pulled)
	}
}

func TestJobDirDashWithNoJobStaysAtZero(t *testing.T) {
	root := t.TempDir()
	got, n := resolveJobDir(root, "-", "card-a", "RESULT card-a")
	want := filepath.Join(root, "0", "jobs", "card-a")
	if n != 0 || got != want {
		t.Fatalf("resolveJobDir with no matching RESULT = %s matches=%d, want named 0 path %s", got, n, want)
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
	copied := time.Date(2026, 9, 20, 17, 0, 0, 0, time.UTC)
	stale := writeJobResult(t, root, "0", "card-a", "RESULT card-a sha=old\nDONE\nBRANCH rowan/stale\n", copied)
	pulled := writeJobResult(t, root, "studio-1", "card-a", "RESULT card-a sha=new\nDONE\nBRANCH rowan/card-a\n", old)
	got, n := resolveJobDir(root, "-", "card-a", "RESULT card-a sha=new")
	if got == stale {
		t.Fatalf("resolveJobDir picked copied-later 0/jobs/card-a over the uniquely matching pull")
	}
	if n != 1 || got != pulled {
		t.Fatalf("resolveJobDir(...) = %s matches=%d, want unique pulled path %s", got, n, pulled)
	}
}

func TestJobDirPicksUniqueContractNotNewerMtime(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	copied := time.Date(2026, 9, 20, 17, 0, 0, 0, time.UTC)
	stale := writeJobResult(t, root, "space-2", "card-a", "RESULT card-a sha=old\nDONE\nBRANCH rowan/stale\n", copied)
	pulled := writeJobResult(t, root, "studio-1", "card-a", "RESULT card-a sha=new\nDONE\nBRANCH rowan/card-a\n", old)
	got, n := resolveJobDir(root, "-", "card-a", "RESULT card-a sha=new")
	if got == stale {
		t.Fatalf("resolveJobDir picked copied-later space-2 over the uniquely matching studio-1")
	}
	if n != 1 || got != pulled {
		t.Fatalf("resolveJobDir(...) = %s matches=%d, want unique pulled path %s", got, n, pulled)
	}
}

func TestJobDirAmbiguousSameContract(t *testing.T) {
	root := t.TempDir()
	when := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	writeJobResult(t, root, "0", "card-a", "RESULT card-a sha=000000000000\nDONE\nBRANCH rowan/stale\n", when)
	writeJobResult(t, root, "studio-1", "card-a", "RESULT card-a sha=000000000000\nDONE\nBRANCH rowan/card-a\n", when)
	got, n := resolveJobDir(root, "-", "card-a", "RESULT card-a sha=000000000000")
	if n < 2 {
		t.Fatalf("resolveJobDir matches=%d dir=%s, want ambiguous (n>1)", n, got)
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
	copied := time.Date(2026, 9, 20, 17, 0, 0, 0, time.UTC)
	writeJobResult(t, root, "0", "card-a",
		"RESULT card-a sha=stale-copy\nDONE\nBRANCH rowan/stale\nREPO owner/repo\n", copied)
	writeJobResult(t, root, "studio-1", "card-a",
		"RESULT card-a sha=000000000000\nDONE\nBRANCH rowan/card-a\nREPO owner/repo\n", old)

	var out, errb bytes.Buffer
	code := Harvest(HarvestInput{ID: id, Root: root, MaxBodyBytes: 4096, Max: 20, Stdout: &out, Stderr: &errb})
	if strings.Contains(out.String(), "branch=rowan/stale") {
		t.Fatalf("harvest folded the stale 0/jobs RESULT (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "branch=rowan/card-a") || !strings.Contains(out.String(), "pushed=1") {
		t.Fatalf("harvest did not fold the current studio-1 RESULT (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
}

func harvestThenFixture(t *testing.T, root, id, contract string) string {
	t.Helper()
	card := filepath.Join(root, "cardsrc", "card-a.md")
	if err := os.MkdirAll(filepath.Dir(card), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(card, []byte(contract+"\nREPO owner/repo\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	admitted := pulseCardsPath(root, id)
	if err := os.MkdirAll(filepath.Dir(admitted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(admitted, []byte("card-a\t-\tflash\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return card
}

// HOLD on #2120 at 96d08409: findLatestJobDir picks max mtime, so a leftover
// RESULT copied or touched later than the current pull wins. The current
// contract uniquely matches studio-1; the stale 0/ copy does not.
func TestHarvestThenDoesNotFoldTouchedLaterStaleResult(t *testing.T) {
	root := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(root, "argv.log")
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/1907")

	id := "20260919T160000Z-pulse-5c8b00"
	contract := "RESULT card-a sha=000000000000"
	harvestThenFixture(t, root, id, contract)
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	copied := time.Date(2026, 9, 20, 17, 0, 0, 0, time.UTC)
	writeJobResult(t, root, "studio-1", "card-a",
		contract+"\nDONE\nBRANCH rowan/card-a\nREPO owner/repo\n", old)
	writeJobResult(t, root, "0", "card-a",
		"RESULT card-a sha=stale-copy\nDONE\nBRANCH rowan/stale\nREPO owner/repo\n", copied)

	var out, errb bytes.Buffer
	code := Harvest(HarvestInput{ID: id, Root: root, MaxBodyBytes: 4096, Max: 20, Stdout: &out, Stderr: &errb})
	if strings.Contains(out.String(), "branch=rowan/stale") {
		t.Fatalf("mtime picked the copied-later stale 0/ RESULT (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "branch=rowan/card-a") || !strings.Contains(out.String(), "pushed=1") {
		t.Fatalf("the uniquely matching current pull was not folded (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
}

func TestHarvestThenRefusesAmbiguousSameContractResults(t *testing.T) {
	root := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(root, "argv.log")
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/1907")

	id := "20260919T160000Z-pulse-5c8b00"
	contract := "RESULT card-a sha=000000000000"
	harvestThenFixture(t, root, id, contract)
	when := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	writeJobResult(t, root, "0", "card-a",
		contract+"\nDONE\nBRANCH rowan/stale\nREPO owner/repo\n", when)
	writeJobResult(t, root, "studio-1", "card-a",
		contract+"\nDONE\nBRANCH rowan/card-a\nREPO owner/repo\n", when)

	var out, errb bytes.Buffer
	code := Harvest(HarvestInput{ID: id, Root: root, MaxBodyBytes: 4096, Max: 20, Stdout: &out, Stderr: &errb})
	if strings.Contains(out.String(), "HARVEST PR") || strings.Contains(out.String(), "pushed=1") {
		t.Fatalf("ambiguous same-contract RESULTS were folded (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "ambiguous") && !strings.Contains(out.String(), "refused=1") {
		t.Fatalf("want an ambiguity refusal, not a silent pick (exit=%d):\n%s\n%s", code, out.String(), errb.String())
	}
}
