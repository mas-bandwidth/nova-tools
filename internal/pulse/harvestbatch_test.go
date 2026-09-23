package pulse

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeMemory is the batch pass's memory as a map: what it was told to remember, and the
// row it reported.
type fakeMemory struct {
	refused    map[string]bool
	remembered []string
	row        map[string]any
}

func (m *fakeMemory) Refused(context.Context, string) (map[string]bool, error) { return m.refused, nil }
func (m *fakeMemory) Remember(_ context.Context, _, label, head string) error {
	m.remembered = append(m.remembered, label+" "+head)
	return nil
}
func (m *fakeMemory) Report(_ context.Context, _ string, fields map[string]any) error {
	m.row = fields
	return nil
}

const stagedHead = "0123456789abcdef0123456789abcdef01234567"

// batchShell answers the three scripts a batch pass runs: the listing, the stage and the
// marks. It records which it saw.
func batchShell(listing string) *fakeShell {
	return &fakeShell{answer: func(bench, script string) (string, error) {
		switch {
		case strings.Contains(script, "touch"):
			return "", nil
		case strings.Contains(script, "take "):
			return "STAGE\tnova-tools\t/home/gaffer/nova-bench/harvest-stage/nova-tools.git\n" +
				"STAGED\tcard-9601\t" + stagedHead + "\n", nil
		}
		return listing, nil
	}}
}

// batchGit is benchGit plus the one answer the batch pass reads that harvestBench never
// asked for: `git push --porcelain`, one line per ref.
func batchGit(t *testing.T, specs, arglog string) {
	t.Helper()
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 3, Equals: "push", Stdout: "To https://forge.invalid/mas-bandwidth/nova-tools.git\n*\trefs/harvest/rowan/card-9601:refs/heads/rowan/card-9601\t[new branch]\nDone\n"},
		{Arg: 3, Equals: "rev-list", Stdout: "3"},
		{Arg: 3, Equals: "rev-parse", Stdout: "abc1234"},
		originRule("mas-bandwidth/nova-tools"),
	}})
}

func batchListing() string {
	return benchJobListing("/home/gaffer/rowan-swarm-root/0/jobs/card-9601", []string{
		"RESULT card-9601 sha=abc", "DONE (green tests)", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
	}) + benchJobListing("/home/gaffer/rowan-swarm-root/1/jobs/card-9602", []string{
		"RESULT card-9602 sha=abc", "ABSTAIN not-per-leg", "BRANCH rowan/card-9602", "REPO mas-bandwidth/nova-tools",
	}) + strings.Replace(benchJobListing("/home/gaffer/rowan-swarm-root/2/jobs/card-9603", []string{
		"RESULT card-9603 sha=abc", "DONE", "BRANCH rowan/card-9603", "REPO mas-bandwidth/nova-tools",
	}), "END\n", "HARVESTED\nEND\n", 1)
}

// #2756: the batch pass pays for the wire once per phase. One ssh stages every DONE
// candidate on the bench, one fetch brings the staged branches over, one push carries every
// refspec, one ssh marks. A job that is not DONE is never staged, whatever BRANCH it names;
// a job already harvested is never staged either.
func TestHarvestBatchStagesFetchesPushesAndMarksOnce(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	batchGit(t, specs, arglog)
	shell := batchShell(batchListing())
	forge := &fakeForge{}
	mem := &fakeMemory{}
	in := benchHarvestInput(t, root, shell, forge)
	in.Batch, in.Memory = true, mem
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", code, out, errb)
	}
	for _, want := range []string{
		"HARVEST JOB bench=hulk label=card-9601 branch=rowan/card-9601 sha=abc1234 base=dev pr=mas-bandwidth/nova-tools#77",
		"HARVEST SKIPPED bench=hulk reason=harvested count=1",
		"HARVEST SKIPPED bench=hulk reason=state count=1",
		"HARVEST hulk jobs=3 done=3 fetched=1 pushed=1 prs=1 skipped=2 took=",
		"HARVEST BENCH OK bench=hulk jobs=3 done=3 pushed=1 prs=1 no-commit=0 skipped=2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	var stages, marks int
	for _, s := range shell.scripts {
		switch {
		case strings.Contains(s, "take "):
			stages++
			if strings.Contains(s, "card-9602") || strings.Contains(s, "card-9603") {
				t.Errorf("a job that is not DONE, or already harvested, was staged:\n%s", s)
			}
			if !strings.Contains(s, "take 'nova-tools' '/home/gaffer/rowan-swarm-root/0/jobs/card-9601' 'rowan/card-9601' 'card-9601'") {
				t.Errorf("the stage script does not take the DONE job's branch:\n%s", s)
			}
		case strings.Contains(s, "touch"):
			marks++
			if !strings.Contains(s, "/home/gaffer/rowan-swarm-root/0/jobs/card-9601/.harvested") {
				t.Errorf("the harvested job was not marked:\n%s", s)
			}
		}
	}
	if stages != 1 || marks != 1 {
		t.Errorf("stage scripts = %d, mark scripts = %d; want one of each", stages, marks)
	}
	log, _ := os.ReadFile(arglog)
	if !strings.Contains(string(log), "fetch --no-tags ssh://hulk/home/gaffer/nova-bench/harvest-stage/nova-tools.git +refs/harvest/card-9601:refs/harvest/rowan/card-9601") {
		t.Errorf("the staged branch was not fetched from the stage by explicit refspec:\n%s", log)
	}
	if strings.Contains(string(log), "/jobs/card-9601/repo") {
		t.Errorf("a job clone was fetched one at a time over ssh; the stage is the one transfer:\n%s", log)
	}
	if !strings.Contains(string(log), "push --porcelain origin refs/harvest/rowan/card-9601:refs/heads/rowan/card-9601") {
		t.Errorf("the branch was not pushed by explicit refspec in the one push:\n%s", log)
	}
	if len(forge.opened) != 1 {
		t.Fatalf("PRs opened = %d, want 1", len(forge.opened))
	}
	if mem.row == nil || mem.row["prs"] != 1 || mem.row["skipped"] != 2 {
		t.Errorf("the row reported to the store is wrong: %v", mem.row)
	}
}

// A (job, head) refused for a stale base on an earlier pass is remembered in the store and
// never fetched again at that head; a rebase on the bench moves the head and the job is
// tried afresh.
func TestHarvestBatchNeverRefetchesARefusedHead(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	batchGit(t, specs, arglog)
	shell := batchShell(batchListing())
	forge := &fakeForge{}
	mem := &fakeMemory{refused: map[string]bool{"card-9601 " + stagedHead: true}}
	in := benchHarvestInput(t, root, shell, forge)
	in.Batch, in.Memory = true, mem
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", code, out, errb)
	}
	if !strings.Contains(out, "HARVEST SKIPPED bench=hulk reason=refused-stale-base count=1") {
		t.Errorf("the refused head was not skipped by name:\n%s", out)
	}
	if !strings.Contains(out, "fetched=0 pushed=0 prs=0 skipped=3") {
		t.Errorf("the refused head was fetched or pushed:\n%s", out)
	}
	log, _ := os.ReadFile(arglog)
	if strings.Contains(string(log), "fetch --no-tags ssh://hulk") {
		t.Errorf("the refused head crossed the wire again:\n%s", log)
	}
	if len(forge.opened) != 0 {
		t.Errorf("PRs opened = %d, want 0", len(forge.opened))
	}
}

// A job harvested before (un-marked, or marked on a bench that lost the marker) already
// has its pull request: the forge says so on create, and the pass finds it instead of
// failing or opening a second one. Any other refusal stands, and a lookup that answers
// nothing is its own refusal, naming both calls.
func TestCreateFirstFindsThePROnlyWhenTheForgeSaysItExists(t *testing.T) {
	exists := fmt.Errorf("HTTP 422: A pull request already exists for o:rowan/old.")
	if !prExists(exists) || prExists(fmt.Errorf("gh api: refused")) || prExists(nil) {
		t.Error("prExists reads the wrong refusals")
	}
	if pr, err := findExistingPR(exists, 41, nil); err != nil || pr != 41 {
		t.Errorf("found: pr=%d err=%v, want 41", pr, err)
	}
	if _, err := findExistingPR(exists, 0, nil); err == nil || !strings.Contains(err.Error(), "no open pull request") {
		t.Errorf("an empty lookup was not refused: %v", err)
	}
	if _, err := findExistingPR(exists, 0, fmt.Errorf("rate limited")); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("a failed lookup lost its cause: %v", err)
	}
}

func TestResultStateReadsTheStateLine(t *testing.T) {
	cases := map[string][]string{
		"DONE":    {"RESULT x sha=abc", "DONE"},
		"DONE ":   {"RESULT x sha=abc", "", "DONE (green tests)"},
		"DONE:":   {"RESULT x sha=abc", "DONE: all green"},
		"ABSTAIN": {"RESULT x sha=abc", "ABSTAIN not-per-leg"},
		"RED":     {"RESULT x sha=abc", "RED 2 tests"},
		"tmpl":    {"RESULT x sha=abc", "DONE <- or: ABSTAIN <why> | BLOCKED <why>"},
		"empty":   {"RESULT x sha=abc"},
	}
	want := map[string]string{"DONE": "DONE", "DONE ": "DONE", "DONE:": "DONE", "ABSTAIN": "ABSTAIN", "RED": "RED", "tmpl": "DONE", "empty": ""}
	for name, lines := range cases {
		if got := resultState(lines); got != want[name] {
			t.Errorf("%s: resultState = %q, want %q", name, got, want[name])
		}
	}
}

func TestParsePorcelainPushNamesEachRefsFate(t *testing.T) {
	out := "To https://forge.invalid/o/r.git\n" +
		"*\trefs/harvest/rowan/a:refs/heads/rowan/a\t[new branch]\n" +
		" \trefs/harvest/rowan/b:refs/heads/rowan/b\tabc..def\n" +
		"!\trefs/harvest/rowan/c:refs/heads/rowan/c\t[rejected] (non-fast-forward)\n" +
		"=\trefs/harvest/rowan/d:refs/heads/rowan/d\t[up to date]\n" +
		"Done\n"
	fate := parsePorcelainPush(out)
	for _, ok := range []string{"refs/heads/rowan/a", "refs/heads/rowan/b", "refs/heads/rowan/d"} {
		if !fate[ok].ok {
			t.Errorf("%s: not ok: %+v", ok, fate[ok])
		}
	}
	if f := fate["refs/heads/rowan/c"]; f.ok || !strings.Contains(f.summary, "non-fast-forward") {
		t.Errorf("the rejected ref is not rejected with its reason: %+v", f)
	}
	if _, there := fate["Done"]; there {
		t.Error("the trailing Done line was read as a ref")
	}
}

func TestParseStagedReadsTheStageAnswer(t *testing.T) {
	stages, staged, failed := parseStaged("STAGE\tnova-tools\t/h/nova-bench/harvest-stage/nova-tools.git\n" +
		"STAGED\tcard-1\t" + stagedHead + "\n" +
		"STAGE-FAIL\tcard-2\tfatal: couldn't find remote ref rowan/x\n" +
		"STAGED\tcard-3\tnot-a-sha\n" +
		"noise on login\n")
	if stages["nova-tools"] != "/h/nova-bench/harvest-stage/nova-tools.git" {
		t.Errorf("stages = %v", stages)
	}
	if staged["card-1"] != stagedHead || len(staged) != 1 {
		t.Errorf("staged = %v", staged)
	}
	if !strings.Contains(failed["card-2"], "couldn't find remote ref") || !strings.Contains(failed["card-3"], "no sha") {
		t.Errorf("failed = %v", failed)
	}
}
