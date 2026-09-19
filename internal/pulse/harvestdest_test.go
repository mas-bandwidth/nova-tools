package pulse

// THE FOUR PATHS, THE TWO REFUSALS (Johnny's HOLD of PR #1809 at 8bfa4020).
//
// The card before this one guarded the four sites with a veto that only fired when the
// job's origin RESOLVED: an empty or unreadable origin verified nothing and let the
// worker's own REPO line through, on every path. These are the fixtures for that hole --
// a RESULT.md naming a foreign repository with NOTHING but itself to say so -- one per
// path, plus the resolver's own two unit refusals.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// (1) THE LOCAL FOLD, on a bare swarm root. `discoverRootCards` has no cards.tsv to read,
// so it builds each row with the RESULT.md itself as the card path -- correct for the
// contract (rule 11), and, before this change, the destination check comparing the
// RESULT.md against the RESULT.md. Johnny's word for it: tautological.
func TestHarvestRefusesABareRootWhoseOnlyDestinationIsItsOwnResult(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	// NO origin rule on purpose: this fixture is the hole itself -- git answers nothing,
	// so the RESULT.md is the only thing naming a repository, and it must be refused.
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})
	fakeGH(t, specs, arglog, "https://forge.invalid/attacker/exfil/pull/1")

	job := filepath.Join(root, "1", "jobs", "card-880")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"),
		[]byte("RESULT card-880 sha=aaa\nDONE\nBRANCH rowan/br-880\nREPO attacker/exfil\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errs := runHarvest(t, root)
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.HasPrefix(l, "gh pr create") {
			t.Fatalf("the local fold reached a forge on a destination only the RESULT.md named: %s", l)
		}
	}
	if !strings.Contains(errs, "HARVEST REFUSED repo-unknown card=card-880") {
		t.Fatalf("the repo-unknown refusal is absent:\nstdout=%s\nstderr=%s", out, errs)
	}
	if !strings.Contains(out, "pushed=0") || !strings.Contains(out, "prs=0") {
		t.Fatalf("want pushed=0 prs=0, got:\n%s", out)
	}
}

// (2) `harvest --working`: the path that force-pushes with a lease, so an unknown
// destination matters more here, not less.
func TestHarvestWorkingRefusesAnUnknownOriginAndForcePushesNothing(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "aaaa000000000000000000000000000000000000 2026-09-17T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "bbbb000000000000000000000000000000000000\trefs/heads/rowan/w"},
		// no `remote get-url` rule: git answers nothing, the origin is unknown
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: "[]"},
		{Arg: 2, Equals: "create", Stdout: "https://forge.invalid/attacker/exfil/pull/23"},
	}})
	wkJob(t, working, "g-w", "w", wkResult("w", "rowan/w", "attacker/exfil"))

	_, errs, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", Max: 20})
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.Contains(l, "pr create") {
			t.Fatalf("--working reached a forge with no origin to check against: %s", l)
		}
	}
	wkHasLine(t, errs, "HARVEST REFUSED repo-unknown card=w")
}

// (3) `harvest --bench`: the path every Space card is harvested through.
func TestHarvestBenchRefusesAnUnknownOriginAndOpensNoPR(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 3, Equals: "rev-list", Stdout: "3"},
		{Arg: 3, Equals: "rev-parse", Stdout: "abc1234"},
		// no `remote get-url` rule: git answers nothing, the origin is unknown
	}})
	mine := "/home/gaffer/rowan-swarm-root/0/jobs/card-1"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(mine, []string{
			"RESULT card-1 sha=abc", "DONE",
			"BRANCH rowan/card-1", "REPO attacker/exfil",
		}), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.BranchPrefix = DefaultBranchPrefix
	_, out, _ := runBenchHarvest(t, in)
	if len(forge.opened) != 0 {
		t.Fatalf("--bench opened a PR with no origin to check against: %+v", forge.opened)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") {
			t.Fatalf("--bench pushed with no origin to check against: %s", l)
		}
	}
	if !strings.Contains(out, "HARVEST REFUSED repo-unknown card=card-1") {
		t.Fatalf("the repo-unknown refusal line is absent:\n%s", out)
	}
}

// (4) `manager.openPR`: the manager force-pushes with a leading plus and opens the PR
// `-R <repo>`, so it takes the same rule.
func TestManagerRefusesAnUnknownOriginAndPushesNothing(t *testing.T) {
	b := setupManager(t)
	b.fake(t, "git", fakeSpec{Rules: []fakeRule{
		{Arg: 1, Equals: "merge-base", Stdout: "aaaaaaaaaaaa"},
		{Arg: 1, Equals: "diff", Stdout: "internal/pulse/manager_test.go"},
		// no `remote get-url` rule: git answers nothing, the origin is unknown
	}})
	b.fakeGH(t, "{}", "[]")
	rootA := strings.Split(b.roots, ",")[0]
	result := "RESULT: CARD-1 fix the slot lock\nBRANCH: rowan/fix-slot-lock\nREPO: attacker/exfil\n"
	b.job(t, rootA, "1", "card-1", "RESULT: CARD-1 fix the slot lock\n", result)
	p := b.policy(t, "floor=0\n")
	out, _, _ := b.run(t, p, 0)
	if strings.Contains(b.argv(t), "git push") || strings.Contains(b.argv(t), "gh pr create") {
		t.Fatalf("the manager reached a forge with no origin to check against: %q", b.argv(t))
	}
	if !strings.Contains(out, "HARVEST REFUSED repo-unknown card=card-1.md") {
		t.Fatalf("the repo-unknown refusal is absent: %q", out)
	}
}

// THE RESOLVER'S OWN TWO REFUSALS, without a harvest around them.
func TestResolveDestinationRefusals(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "card-1.md")
	if err := os.WriteFile(card, []byte("RESULT card-1\nREPO owner/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(dir, "RESULT.md")
	if err := os.WriteFile(result, []byte("RESULT card-1\nREPO attacker/exfil\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The launch record answers when git cannot, and the URL is formed from IT.
	got, err := resolveDestination("card-1", dir, card, "owner/repo")
	if err != nil {
		t.Fatalf("the launch record must answer: %v", err)
	}
	if got.repo != "owner/repo" || got.url != githubCloneBase+"owner/repo.git" || got.from != "launch-record" {
		t.Fatalf("destination = %+v", got)
	}

	// A claim that disagrees is refused by name.
	if _, err := resolveDestination("card-1", dir, card, "attacker/exfil"); err == nil ||
		!strings.Contains(err.Error(), "HARVEST REFUSED repo-mismatch card=card-1 origin=owner/repo") {
		t.Fatalf("the mismatch refusal: %v", err)
	}

	// A RESULT.md is not a launch record at any position, so it answers nothing.
	if _, err := resolveDestination("card-1", dir, result, "attacker/exfil"); err == nil ||
		!strings.Contains(err.Error(), "HARVEST REFUSED repo-unknown card=card-1") {
		t.Fatalf("a RESULT.md must never be the record: %v", err)
	}

	// Nothing at all: refused, never guessed.
	if _, err := resolveDestination("card-1", dir, "", "attacker/exfil"); err == nil ||
		!strings.Contains(err.Error(), "HARVEST REFUSED repo-unknown card=card-1") {
		t.Fatalf("an empty origin must refuse: %v", err)
	}
}

// TestNoPushURLFromAResultRemains: `pushURL` formed the push destination FROM the worker's
// claim, which is the defect itself and not a detail of it. It is gone, and this test says
// so from the package's own source so it cannot quietly come back.
func TestNoPushURLFromAResultRemains(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("pushURL(")) {
			t.Errorf("%s still calls pushURL(): the push destination is resolveDestination's answer, never a URL a caller formed", e.Name())
		}
	}
}
