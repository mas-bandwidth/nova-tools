package main

// The round trip the schema dogfood on hulk could not make (2026-09-18): a card cut from the
// SHIPPED rows.md template, the RESULT.md that card asks the worker for, and a
// `harvest --bench` that reads it and opens the PR. The template told the worker to "write
// RESULT.md with line 1 equal to this card's line 1" and nothing more, so the harvester
// refused what it produced four times over -- no BRANCH, no REPO, no clone, and a fetch of
// <job>/repo where the template had cloned into <job> itself.
//
// Nothing here is a second copy of the grammar: the card's front matter is read with
// pulse.ReadCardFront and the RESULT.md is written with CardFront.ResultMD, which are the
// same two functions cut renders through and harvest reads through. If either end drifts,
// this test is the thing that goes red.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// shippedTemplates is the template directory `nova-pulse help` names in its own example --
// the one a hand on a bench actually passes to --templates.
const shippedTemplates = "testdata/templates"

// roundTripShell answers the bench listing with one job whose RESULT.md is the text given,
// and swallows the `.harvested` touch.
type roundTripShell struct{ job, result string }

func (s roundTripShell) Run(bench, script string) (string, error) {
	if strings.Contains(script, "touch") {
		return "", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "JOB\t%s\nMTIME\t0\n", s.job)
	for _, l := range strings.Split(strings.TrimRight(s.result, "\n"), "\n") {
		fmt.Fprintf(&b, "R\t%s\n", l)
	}
	b.WriteString("END\n")
	return b.String(), nil
}

// roundTripForge records the one PR the harvest opens.
type roundTripForge struct {
	repo, base, branch, title string
	opened                    int
}

func (f *roundTripForge) FindPR(string, string) (int, error) { return 0, nil }

func (f *roundTripForge) CreatePR(repo, base, branch, title, body string) (int, error) {
	f.repo, f.base, f.branch, f.title = repo, base, branch, title
	f.opened++
	return 4242, nil
}

// stepLine is one numbered step of a rendered card, trimmed.
func stepLine(card, prefix string) string {
	for _, l := range strings.Split(card, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// TestShippedRowsTemplateCutsACardHarvestCanRead is the whole contract in one run.
func TestShippedRowsTemplateCutsACardHarvestCanRead(t *testing.T) {
	specs := fakePATH(t)
	arglog := filepath.Join(t.TempDir(), "argv.log")
	// The clone cut runs its git in: `remote get-url origin` is where the card's REPO
	// line comes from, and everything else answers exit 0 (branch absent, path present).
	gitAnswers(t, specs, arglog,
		fakeRule{Arg: 3, Equals: "remote", Stdout: "git@example.com:mas-bandwidth/nova-tools.git"},
		fakeRule{Arg: 3, Equals: "rev-list", Stdout: "3"},
		fakeRule{Arg: 3, Equals: "rev-parse", Stdout: "abc1234"},
	)

	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	rows := filepath.Join(dir, "rows.tsv")
	body := "9601\tdev\tinternal/pulse/card.go\tdocs/SPEC-PULSE.md\trowan/card-9601\tpulse\n"
	if err := os.WriteFile(rows, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runValidatedCut(t, "--rows", rows,
		"--templates", shippedTemplates, "--out", out, "--repo", dir)
	if code != 0 {
		t.Fatalf("cut from the shipped template exit = %d, want 0\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(out, "card-9601.md"))
	if err != nil {
		t.Fatal(err)
	}
	card := string(raw)

	// The shipped template carries the grammar's own words, so a worker reading the card
	// is told exactly what the harvester will look for.
	if !strings.Contains(card, pulse.ResultInstruction()) {
		t.Errorf("the shipped template's last step is not the one instruction in card.go:\n%s", card)
	}
	// And it clones into <job>/repo, which is the one place the fleet reads a card's
	// commits from -- the harvest's fetch, the swarm's wall reader and the manager's push.
	step1 := stepLine(card, "STEP 1.")
	if !strings.HasSuffix(step1, "checkout -b rowan/card-9601 dev") {
		t.Errorf("STEP 1 does not check out the card's own branch off its base: %q", step1)
	}
	if !strings.Contains(step1, " "+pulse.JobRepoDir+" && git -C "+pulse.JobRepoDir) {
		t.Errorf("STEP 1 does not clone into ./%s, which is where the whole fleet reads a card's commits: %q",
			pulse.JobRepoDir, step1)
	}

	front := pulse.ReadCardFront(card)
	if front.Branch != "rowan/card-9601" || front.Repo != "mas-bandwidth/nova-tools" || front.Base != "dev" {
		t.Fatalf("the cut card declares %+v, want branch rowan/card-9601, repo mas-bandwidth/nova-tools, base dev", front)
	}

	// Be the worker: write the RESULT.md this card asks for, and nothing else.
	contract := strings.SplitN(card, "\n", 2)[0]
	result := front.ResultMD(contract, pulse.VerdictDone)

	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	forge := &roundTripForge{}
	var hout, herr bytes.Buffer
	if got := pulse.Harvest(pulse.HarvestInput{
		Bench:  "hulk",
		Root:   "/home/gaffer/rowan-swarm-root",
		Clones: []string{dir},
		Shell:  roundTripShell{job: job, result: result},
		Forge:  forge,
		Stdout: &hout,
		Stderr: &herr,
	}); got != 0 {
		t.Fatalf("harvest of the card's own RESULT.md exit = %d\n%s\n%s", got, hout.String(), herr.String())
	}
	if strings.Contains(hout.String(), "HARVEST SKIP") {
		t.Fatalf("the harvester refused the RESULT.md the shipped template asks for:\n%s", hout.String())
	}
	if forge.opened != 1 {
		t.Fatalf("PRs opened = %d, want 1\n%s", forge.opened, hout.String())
	}
	if forge.repo != "mas-bandwidth/nova-tools" || forge.branch != "rowan/card-9601" || forge.base != "dev" {
		t.Errorf("the PR is %s %s onto %s, want the card's own repo, branch and base",
			forge.repo, forge.branch, forge.base)
	}
	if !strings.Contains(forge.title, "9601") {
		t.Errorf("the PR title does not carry the card's label: %q", forge.title)
	}
	// The branch was fetched from <job>/repo, which is what the template now writes.
	log, _ := os.ReadFile(arglog)
	if !strings.Contains(string(log), "ssh://hulk"+job+"/"+pulse.JobRepoDir) {
		t.Errorf("the branch was not fetched from the job's clone:\n%s", log)
	}
}

// TestShippedRowsTemplateRedResultIsNeverDone: the same card, the same template, the worker
// abstaining. The round trip must not end in a PR.
func TestShippedRowsTemplateRedResultIsNeverDone(t *testing.T) {
	front := pulse.CardFront{Branch: "rowan/card-9601", Repo: "mas-bandwidth/nova-tools", Base: "dev"}
	result := front.ResultMD("RESULT card-9601 sha=abc", pulse.VerdictAbstain+" the harness died")
	forge := &roundTripForge{}
	var hout, herr bytes.Buffer
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	if got := pulse.Harvest(pulse.HarvestInput{
		Bench:  "hulk",
		Root:   "/home/gaffer/rowan-swarm-root",
		Clones: []string{t.TempDir()},
		Shell:  roundTripShell{job: job, result: result},
		Forge:  forge,
		Stdout: &hout,
		Stderr: &herr,
	}); got != 0 {
		t.Fatalf("exit = %d\n%s\n%s", got, hout.String(), herr.String())
	}
	if forge.opened != 0 {
		t.Errorf("a PR was opened for an abstaining worker: %+v", forge)
	}
	if !strings.Contains(hout.String(), "red=1") {
		t.Errorf("the abstain was not counted as red:\n%s", hout.String())
	}
}
