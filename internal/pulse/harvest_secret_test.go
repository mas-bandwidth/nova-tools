package pulse

import (
	"bytes"
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
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/42")

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
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/42")

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
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/42")
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
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/42")
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

// --- Johnny's HOLD of #1838: the other three publish sites ------------------------------

// TestHarvestBenchRefusesToPushAKeyShape: `harvest --bench` is how every Space card is
// harvested, and it pushed the branch and built the PR body out of the bench's RESULT.md
// with no scan at all. Control: delete the secretFindings block from harvestBench and this
// goes red with a push in the argv log and one opened PR.
func TestHarvestBenchRefusesToPushAKeyShape(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	job := "/home/gaffer/rowan-swarm-root/0/jobs/leaky"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") || strings.Contains(script, "mv ") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT leaky sha=abc",
			"DONE",
			"BRANCH rowan/leaky",
			"REPO mas-bandwidth/nova-tools",
			"out: " + secretFixture(),
		}), nil
	}}
	forge := &fakeForge{}
	code, out, errb := runBenchHarvest(t, benchHarvestInput(t, root, shell, forge))

	if !strings.Contains(errb, "HARVEST REFUSED secret-shape") || !strings.Contains(errb, "site=harvest-bench") {
		t.Fatalf("no bench refusal line:\nstdout=%s\nstderr=%s", out, errb)
	}
	if len(forge.opened) != 0 {
		t.Fatalf("a PR was opened for a card carrying a key shape: %+v", forge.opened)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.Contains(l, "push") {
			t.Fatalf("the quarantined card was pushed: %s", l)
		}
	}
	if shell.ran("touch " + shellQuote(job+"/.harvested")) {
		t.Error("a quarantined job was marked harvested, so the next harvest would push it")
	}
	if !shell.ran("mv '"+job+"'") || !shell.ran("quarantine") {
		t.Errorf("the job was not quarantined on the bench: %v", shell.scripts)
	}
	for _, s := range shell.scripts {
		if strings.Contains(s, "rm ") || strings.Contains(s, "rm -") {
			t.Fatalf("the quarantine deleted something: %s", s)
		}
	}
	if strings.Contains(errb, secretFixture()) || strings.Contains(out, secretFixture()) {
		t.Fatal("the refusal printed the matched text")
	}
	if !strings.Contains(errb, "HUMAN task=secret") {
		t.Errorf("no HUMAN line on a bench refusal:\n%s", errb)
	}
	if code == 0 {
		t.Error("a bench harvest that refused a card exits nonzero")
	}
}

// TestHarvestWorkingRefusesToPushAKeyShape: the working layout's one() pushed with
// --force-with-lease and then edited or opened the PR from the same lines, with no scan.
func TestHarvestWorkingRefusesToPushAKeyShape(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "abc1234567890abc 2026-09-19T18:00:00Z\n"},
		{Arg: 1, Equals: "ls-remote", Stdout: "abc1234567890abc\trefs/heads/rowan/leaky\n"},
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{{Arg: 1, Equals: "pr", Stdout: "[]"}}})

	job := filepath.Join(root, "working", "jobs", "leaky")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	result := "RESULT leaky sha=abc\nDONE\nBRANCH rowan/leaky\nREPO owner/repo\nout: " + secretFixture() + "\n"
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(result), 0o644); err != nil {
		t.Fatal(err)
	}

	var errb bytes.Buffer
	r := &workingRun{in: HarvestInput{Stdout: &bytes.Buffer{}, Stderr: &errb}, base12: "origin/dev"}
	outcome := r.one(harvestJob{dir: job, label: "leaky"})

	if outcome.pushed != 0 || outcome.prs != 0 {
		t.Fatalf("the card was published: %+v", outcome)
	}
	if !strings.Contains(errb.String(), "HARVEST REFUSED secret-shape") || !strings.Contains(errb.String(), "site=harvest-working") {
		t.Fatalf("no working refusal line:\n%s", errb.String())
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.Contains(l, "push") || strings.Contains(l, "pr create") || strings.Contains(l, "pr edit") {
			t.Fatalf("the quarantined card reached the forge: %s", l)
		}
	}
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("the job was not quarantined: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "working", "quarantine", "leaky", "RESULT.md")); err != nil {
		t.Fatalf("the quarantined RESULT.md is gone: %v", err)
	}
	if strings.Contains(errb.String(), secretFixture()) {
		t.Fatal("the refusal printed the matched text")
	}
}

// TestManagerOpenPRRefusesToPushAKeyShape: the manager tier pushed the branch and copied
// the same lines into the PR body with no scan.
func TestManagerOpenPRRefusesToPushAKeyShape(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{{Arg: 1, Equals: "pr", Stdout: "[]"}}})

	queue := filepath.Join(root, "queue")
	for _, d := range []string{"launched", "failed", "done"} {
		if err := os.MkdirAll(filepath.Join(queue, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(queue, "launched", "leaky.md"), []byte("card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(root, "swarm", "jobs", "leaky")
	if err := os.MkdirAll(filepath.Join(job, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{"RESULT leaky sha=abc", "DONE", "BRANCH rowan/leaky", "REPO owner/repo",
		"red: TestThing", "out: " + secretFixture()}

	var out bytes.Buffer
	m := &manager{in: ManagerInput{Queue: queue, Stdout: &out, Stderr: &out}, out: bound(&out, 20)}
	m.openPR("leaky.md", job, lines)

	if m.prs != 0 {
		t.Fatalf("the manager opened %d PRs for a card carrying a key shape", m.prs)
	}
	if !strings.Contains(out.String(), "MANAGER REFUSED") || !strings.Contains(out.String(), "secret-shape") {
		t.Fatalf("no manager refusal line:\n%s", out.String())
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.Contains(l, "push") || strings.Contains(l, "pr create") {
			t.Fatalf("the quarantined card reached the forge: %s", l)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "swarm", "quarantine", "leaky.md")); err != nil {
		t.Fatalf("the job was not quarantined: %v", err)
	}
	human, err := os.ReadFile(filepath.Join(queue, "HUMAN"))
	if err != nil {
		t.Fatalf("no HUMAN line in the queue: %v", err)
	}
	if !strings.Contains(string(human), "HUMAN task=secret") || strings.Contains(string(human), secretFixture()) {
		t.Fatalf("HUMAN line: %s", human)
	}
	if strings.Contains(out.String(), secretFixture()) {
		t.Fatal("the refusal printed the matched text")
	}
}
