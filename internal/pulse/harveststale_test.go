package pulse

// Issue #2032: harvest of a returned branch whose base is stale must never become a
// PR. schema14 folded eight such branches; `git diff <target>..<branch>` showed 1,155
// deletions and would have reverted nine merged PRs. The two-dot diff against the
// CURRENT target must contain only the card's declared PATHS.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHarvestRefusesAReturnedBranchWhoseBaseIsStale is the failing-first case the
// issue names: a card branch cut from an older base, later files landed on the
// target, harvest must refuse and name those files, and nothing is pushed.
func TestHarvestRefusesAReturnedBranchWhoseBaseIsStale(t *testing.T) {
	b := newDestBench(t)
	label := "card-2032"
	branch := "rowan/stale-base"
	addCard(t, b.root, label, "1", "flash", "RESULT "+label+" sha=aaa",
		"RESULT "+label+" sha=aaa\nDONE\nBRANCH "+branch+"\nREPO owner/repo\nBASE dev\n")
	cardPath := filepath.Join(b.root, "cardsrc", label+".md")
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, append(raw, []byte("PATHS: card/fix.go\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	b.job = filepath.Join(b.root, "1", "jobs", label)
	realGit(t, "init", "-b", "dev", b.job)
	realGit(t, "-C", b.job, "remote", "add", "origin", honestURL)
	if err := os.MkdirAll(filepath.Join(b.job, "card"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.job, "card", "fix.go"), []byte("package card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card/fix.go")
	realGit(t, "-C", b.job, "commit", "-m", "old target")
	realGit(t, "-C", b.job, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(b.job, "card", "fix.go"), []byte("package card\n\nfunc Fix() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card/fix.go")
	realGit(t, "-C", b.job, "commit", "-m", "the card")
	realGit(t, "-C", b.job, "checkout", "dev")
	if err := os.MkdirAll(filepath.Join(b.job, "later"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.job, "later", "merged.go"), []byte("package later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "later/merged.go")
	realGit(t, "-C", b.job, "commit", "-m", "later files landed on the target")
	realGit(t, "-C", b.job, "checkout", branch)

	out, errs := runHarvest(t, b.root)

	if got := refs(t, b.honest); len(got) != 0 {
		t.Fatalf("a stale-base branch became a push: %s holds %v\nstdout=%s\nstderr=%s", b.honest, got, out, errs)
	}
	if strings.Contains(out, "HARVEST PR") || strings.Contains(out, "prs=1") {
		t.Fatalf("a stale-base branch became a PR:\n%s\n%s", out, errs)
	}
	if !strings.Contains(out, "pushed=0") || !strings.Contains(out, "prs=0") {
		t.Fatalf("want pushed=0 prs=0, got:\n%s\n%s", out, errs)
	}
	if !strings.Contains(out, "refused=1") && !strings.Contains(errs, "HARVEST REFUSED") {
		t.Fatalf("want refused=1 or HARVEST REFUSED, got:\n%s\n%s", out, errs)
	}
	if !strings.Contains(out, "later/merged.go") && !strings.Contains(errs, "later/merged.go") {
		t.Fatalf("the refusal must name the later file that would be reverted:\nstdout=%s\nstderr=%s", out, errs)
	}
	for _, l := range arglogLines(t, b.arglog) {
		if strings.Contains(l, "pr create") {
			t.Fatalf("gh pr create ran for a stale-base branch: %s", l)
		}
	}
}

// TestHarvestBenchRefusesAReturnedBranchWhoseBaseIsStale is the schema14 path:
// harvest --bench, before any push or CreatePR.
func TestHarvestBenchRefusesAReturnedBranchWhoseBaseIsStale(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 3, Equals: "diff", Stdout: "card/fix.go\nlater/merged.go\n"},
		{Arg: 3, Equals: "rev-list", Stdout: "3"},
		{Arg: 3, Equals: "rev-parse", Stdout: "abc1234"},
		originRule("mas-bandwidth/nova-tools"),
	}})
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-2032"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-2032 sha=abc",
			"DONE",
			"PATHS: card/fix.go",
			"BRANCH rowan/card-2032",
			"REPO mas-bandwidth/nova-tools",
			"BASE dev",
		}), nil
	}}
	forge := &fakeForge{}
	code, out, errb := runBenchHarvest(t, benchHarvestInput(t, root, shell, forge))
	if len(forge.opened) != 0 {
		t.Fatalf("a stale-base returned branch became a PR: %+v\n%s\n%s", forge.opened, out, errb)
	}
	log, _ := os.ReadFile(arglog)
	if strings.Contains(string(log), "push origin") {
		t.Fatalf("a stale-base returned branch was pushed:\n%s", log)
	}
	if !strings.Contains(out, "later/merged.go") && !strings.Contains(errb, "later/merged.go") {
		t.Fatalf("the refusal must name the later file:\nstdout=%s\nstderr=%s", out, errb)
	}
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero for a refused harvest\n%s\n%s", out, errb)
	}
}

// The positive control: a card branch cut from the current target, two-dot inside
// PATHS, still opens. A guard that refuses everything is not a guard.
func TestHarvestOpensAPRWhenTheTwoDotDiffStaysInsidePATHS(t *testing.T) {
	b := newDestBench(t)
	label := "card-2032-ok"
	branch := "rowan/fresh-base"
	addCard(t, b.root, label, "1", "flash", "RESULT "+label+" sha=aaa",
		"RESULT "+label+" sha=aaa\nDONE\nBRANCH "+branch+"\nREPO owner/repo\nBASE dev\n")
	cardPath := filepath.Join(b.root, "cardsrc", label+".md")
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, append(raw, []byte("PATHS: card/fix.go\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	b.job = filepath.Join(b.root, "1", "jobs", label)
	realGit(t, "init", "-b", "dev", b.job)
	realGit(t, "-C", b.job, "remote", "add", "origin", honestURL)
	if err := os.MkdirAll(filepath.Join(b.job, "card"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.job, "card", "fix.go"), []byte("package card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card/fix.go")
	realGit(t, "-C", b.job, "commit", "-m", "current target")
	realGit(t, "-C", b.job, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(b.job, "card", "fix.go"), []byte("package card\n\nfunc Fix() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, "-C", b.job, "add", "card/fix.go")
	realGit(t, "-C", b.job, "commit", "-m", "the card")

	out, errs := runHarvest(t, b.root)

	if got := refs(t, b.honest); len(got) == 0 {
		t.Fatalf("the in-path branch was not pushed:\n%s\n%s", out, errs)
	}
	if !strings.Contains(out, "pushed=1") || !strings.Contains(out, "prs=1") {
		t.Fatalf("want pushed=1 prs=1, got:\n%s\n%s", out, errs)
	}
}
