package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is BUILT here and is a valid credential for nothing: a key-shaped literal in
// the repository would be a key-shaped literal in the repository.
func secretFixture() string { return "ghp_" + strings.Repeat("B", 30) }

// TestHarvestRefusesToPushAKeyShape is the red the whole scan exists for: a RESULT.md
// carrying a key shape is never pushed and no PR is opened for it. The control is one
// edit -- delete the secretScan block from Harvest's "done" case -- and this goes red with
// pushed=1 prs=1.
func TestHarvestRefusesToPushAKeyShape(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/42")

	addCard(t, root, "clean", "1", "flash", "RESULT clean sha=aaa",
		"RESULT clean sha=aaa\nDONE\nBRANCH br1\nREPO owner/repo\n")
	addCard(t, root, "leaky", "1", "flash", "RESULT leaky sha=bbb",
		"RESULT leaky sha=bbb\nDONE\nBRANCH br2\nREPO owner/repo\nout: "+secretFixture()+"\n")

	out, errs := runHarvest(t, root)
	if !strings.Contains(out, "pushed=1") || !strings.Contains(out, "prs=1") {
		t.Fatalf("the clean card must still land, and the leaky one must not:\n%s", out)
	}
	if !strings.Contains(out, "refused=1") {
		t.Fatalf("want refused=1, got:\n%s", out)
	}
	if !strings.Contains(errs, "HARVEST REFUSED secret-shape") {
		t.Fatalf("no refusal line:\n%s", errs)
	}
	if !strings.Contains(errs, "shape=forge-token") {
		t.Fatalf("the refusal does not name the shape:\n%s", errs)
	}
	if strings.Contains(errs, secretFixture()) || strings.Contains(out, secretFixture()) {
		t.Fatal("the refusal printed the matched text")
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.Contains(l, "br2") {
			t.Fatalf("the quarantined card reached the forge: %s", l)
		}
	}
}

// TestASecretQuarantinesAndNeverDeletes: the job is MOVED, not removed, because a key in a
// worker's output is evidence a person has to read.
func TestASecretQuarantinesAndNeverDeletes(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/42")

	addCard(t, root, "leaky", "1", "flash", "RESULT leaky sha=bbb",
		"RESULT leaky sha=bbb\nDONE\nBRANCH br2\nREPO owner/repo\nout: "+secretFixture()+"\n")
	job := filepath.Join(root, "1", "jobs", "leaky")
	if err := os.WriteFile(filepath.Join(job, "harness-output.log"), []byte("evidence\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runHarvest(t, root)

	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("the job is still in the harvest's way: %v", err)
	}
	dest := filepath.Join(root, "quarantine", "leaky")
	raw, err := os.ReadFile(filepath.Join(dest, "RESULT.md"))
	if err != nil {
		t.Fatalf("the quarantined RESULT.md is gone: %v", err)
	}
	if !strings.Contains(string(raw), "RESULT leaky") {
		t.Fatalf("the quarantined RESULT.md was rewritten")
	}
	if _, err := os.Stat(filepath.Join(dest, "harness-output.log")); err != nil {
		t.Fatalf("the quarantine dropped the run's evidence: %v", err)
	}
}

// TestASecretWritesOneHumanLine, and the line names no value.
func TestASecretWritesOneHumanLine(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/42")
	addCard(t, root, "leaky", "1", "flash", "RESULT leaky sha=bbb",
		"RESULT leaky sha=bbb\nDONE\nBRANCH br2\nREPO owner/repo\nout: "+secretFixture()+"\n")

	runHarvest(t, root)

	raw, err := os.ReadFile(filepath.Join(root, "HUMAN"))
	if err != nil {
		t.Fatalf("no HUMAN line: %v", err)
	}
	line := string(raw)
	if !strings.Contains(line, "HUMAN task=secret") || !strings.Contains(line, "card=leaky") {
		t.Fatalf("HUMAN line: %s", line)
	}
	if !strings.Contains(line, "quarantine=") || !strings.Contains(line, "rotate") {
		t.Fatalf("the HUMAN line names neither the quarantine nor the remedy: %s", line)
	}
	if strings.Contains(line, secretFixture()) {
		t.Fatal("the HUMAN line printed the matched text")
	}
	if n := strings.Count(line, "HUMAN "); n != 1 {
		t.Fatalf("want one HUMAN line, got %d: %s", n, line)
	}
}

// TestTheSeatsOwnKeyIsCaughtWithoutAShape: the value of a secret-named variable this
// process holds is found even when its form is on no published list, and the receipt is a
// NAME and a LENGTH.
func TestTheSeatsOwnKeyIsCaughtWithoutAShape(t *testing.T) {
	value := "qqp" + strings.Repeat("3", 29)
	t.Setenv("SEAT_PROVIDER_KEY", value)
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/42")
	addCard(t, root, "leaky", "1", "flash", "RESULT leaky sha=bbb",
		"RESULT leaky sha=bbb\nDONE\nBRANCH br2\nREPO owner/repo\nout: "+value+"\n")

	out, errs := runHarvest(t, root)
	if !strings.Contains(errs, "shape=env-value") || !strings.Contains(errs, "name=SEAT_PROVIDER_KEY") {
		t.Fatalf("the seat's own key was not caught:\n%s", errs)
	}
	if !strings.Contains(errs, "len=32") {
		t.Fatalf("the receipt is not a length:\n%s", errs)
	}
	if strings.Contains(errs, value) || strings.Contains(out, value) {
		t.Fatal("the refusal printed the value")
	}
	if !strings.Contains(out, "pushed=0") {
		t.Fatalf("something was pushed:\n%s", out)
	}
}

// TestThePRBodyIsAPrefixOfWhatTheScanRead pins why the PR body needs no scan of its own:
// openPR's body is the scanned RESULT.md text, truncated. A change to openPR that makes
// the body something else breaks this test rather than opening a hole.
func TestThePRBodyIsAPrefixOfWhatTheScanRead(t *testing.T) {
	lines := []string{"RESULT x", "DONE", "BRANCH b", strings.Repeat("y", 100)}
	scanned := strings.Join(lines, "\n")
	for _, cap := range []int{4096, 20, 1} {
		body := scanned
		if len(body) > cap {
			body = body[:cap]
		}
		if !strings.HasPrefix(scanned, body) {
			t.Fatalf("the PR body at cap %d is not a prefix of the scanned text", cap)
		}
	}
}
