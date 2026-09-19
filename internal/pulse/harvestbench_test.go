package pulse

// The red tests of `nova-pulse harvest --bench`: the six edges the schema dogfood loop
// found in ~/rowan-working/bin/harvest-bench.sh and in today's local-only harvest
// (2026-09-18). Every one of them drives the verb through its two seams -- a fake bench
// shell instead of ssh and a fake forge instead of gh -- plus the fake git already on PATH,
// so no test here opens a connection.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeShell is the BenchShell seam: it records every script it was handed and answers from
// the function the test gave it.
type fakeShell struct {
	scripts []string
	answer  func(bench, script string) (string, error)
}

func (f *fakeShell) Run(bench, script string) (string, error) {
	f.scripts = append(f.scripts, script)
	if f.answer == nil {
		return "", nil
	}
	return f.answer(bench, script)
}

// ran says whether any script this shell was handed holds the substring.
func (f *fakeShell) ran(sub string) bool {
	for _, s := range f.scripts {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// openedPR is one CreatePR call, kept whole so a test reads the base and the title the
// verb chose.
type openedPR struct {
	repo, base, branch, title, body string
}

// fakeForge is the Forge seam: no PR exists until the verb opens one, and every open is
// recorded.
type fakeForge struct {
	opened []openedPR
	next   int
	find   func(repo, branch string) (int, error)
}

func (f *fakeForge) FindPR(repo, branch string) (int, error) {
	if f.find != nil {
		return f.find(repo, branch)
	}
	return 0, nil
}

func (f *fakeForge) CreatePR(repo, base, branch, title, body string) (int, error) {
	f.opened = append(f.opened, openedPR{repo: repo, base: base, branch: branch, title: title, body: body})
	f.next++
	return 76 + f.next, nil
}

// benchJobListing is what the listing script prints for one job on the bench: the job
// directory, then its RESULT.md body, one `R` line each, then END. A job with no RESULT.md
// prints its directory and END and nothing between, which is a job still running.
func benchJobListing(dir string, result []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "JOB\t%s\n", dir)
	fmt.Fprintf(&b, "MTIME\t%d\n", time.Now().Unix())
	for _, l := range result {
		fmt.Fprintf(&b, "R\t%s\n", l)
	}
	b.WriteString("END\n")
	return b.String()
}

// benchGit is the fake git this package's tests already put on PATH, taught the three
// answers the bench harvest reads: the commit count, the short sha, and silence for
// everything else (a fetch and a push that work).
func benchGit(t *testing.T, specs, arglog string, counts map[string]string) {
	t.Helper()
	rules := []fakeRule{}
	for rng, n := range counts {
		rules = append(rules, fakeRule{Arg: 5, Equals: rng, Stdout: n})
	}
	rules = append(rules,
		fakeRule{Arg: 3, Equals: "rev-list", Stdout: "3"},
		fakeRule{Arg: 3, Equals: "rev-parse", Stdout: "abc1234"},
		// The clone's own origin, which is what these fixtures push and open PRs
		// against now: the destination is resolved from git's record, never from the
		// bench RESULT.md's REPO line (resolveDestination; Johnny's HOLD of #1809).
		originRule("mas-bandwidth/nova-tools"),
	)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: rules})
}

func benchHarvestInput(t *testing.T, root string, shell *fakeShell, forge *fakeForge) HarvestInput {
	t.Helper()
	return HarvestInput{
		Bench:  "hulk",
		Root:   "/home/gaffer/rowan-swarm-root",
		Clones: []string{filepath.Join(root, "clone")},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Shell:  shell,
		Forge:  forge,
	}
}

func runBenchHarvest(t *testing.T, in HarvestInput) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout, in.Stderr = &out, &errb
	code := Harvest(in)
	return code, out.String(), errb.String()
}

// Edge 11: `harvest` had no --bench and read RESULT.md locally, so a card that finished
// green on a bench with a committed branch was reported as a retry. With --bench it lists
// the bench's jobs over the shell seam, reads each RESULT.md, pushes the job's branch from
// the local clone and opens the PR through the forge seam -- and wants no --sources, no
// --templates and no --id, which a --rows cut never produces.
func TestHarvestBenchReadsResultsOverTheShellSeamAndOpensThePR(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-9601 sha=abc",
			"DONE",
			"BRANCH rowan/card-9601",
			"REPO mas-bandwidth/nova-tools",
		}), nil
	}}
	forge := &fakeForge{}
	code, out, errb := runBenchHarvest(t, benchHarvestInput(t, root, shell, forge))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", code, out, errb)
	}
	if !strings.Contains(out, "HARVEST JOB bench=hulk label=card-9601 branch=rowan/card-9601") {
		t.Errorf("no HARVEST JOB line for the finished job:\n%s", out)
	}
	if !strings.Contains(out, "pr=mas-bandwidth/nova-tools#77") {
		t.Errorf("the PR the forge opened is not on the line:\n%s", out)
	}
	if strings.Contains(out, "retry=1") {
		t.Errorf("a job that finished green was reported as a retry:\n%s", out)
	}
	if len(forge.opened) != 1 {
		t.Fatalf("PRs opened = %d, want 1", len(forge.opened))
	}
	log, _ := os.ReadFile(arglog)
	if !strings.Contains(string(log), "push origin refs/harvest/rowan/card-9601:refs/heads/rowan/card-9601") {
		t.Errorf("the branch was not pushed from the local clone by explicit refspec:\n%s", log)
	}
	if !strings.Contains(string(log), "ssh://hulk"+job+"/repo") {
		t.Errorf("the job's branch was not fetched from the bench:\n%s", log)
	}
	if !shell.ran(".harvested") {
		t.Error("the harvested job was not marked on the bench, so the next harvest opens the PR again")
	}
}

// Edge 11, the other half: a card in cards.tsv whose job directory is not under the local
// root did not run here at all. It was counted as an abstain and retried -- a success
// reported as a retry. It is its own state now, and the note names the remedy.
func TestHarvestDoesNotCountAMissingJobDirAsARetry(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/1")
	addCard(t, root, "card-9601", "0", "flash", "RESULT card-9601 sha=abc", "")
	if err := os.RemoveAll(filepath.Join(root, "0", "jobs", "card-9601")); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Harvest(HarvestInput{ID: "p1", Root: root, Stdout: &out, Stderr: &errb})
	if strings.Contains(out.String(), "retry=1") || strings.Contains(out.String(), "abstain=1") {
		t.Errorf("a card with no job directory here was counted as an abstain and a retry:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "elsewhere=1") {
		t.Errorf("the HARVEST line does not count the card that ran elsewhere:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "--bench") {
		t.Errorf("the note does not name the remedy (--bench):\n%s", errb.String())
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0: a card that ran elsewhere is not a red harvest", code)
	}
}

// Edge 12: harvest-bench.sh took every rowan/* job under six hours, whoever cut it. The
// verb filters by --session and by --branch-prefix, and names the reason it passed a job by.
func TestHarvestBenchFiltersBySessionAndBranchPrefix(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	mine := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	other := "/home/gaffer/rowan-swarm-root/1/jobs/card-9602"
	stella := "/home/gaffer/rowan-swarm-root/2/jobs/card-9603"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(mine, []string{
			"RESULT card-9601 sha=abc", "DONE", "SESSION s-42",
			"BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
		}) + benchJobListing(other, []string{
			"RESULT card-9602 sha=abc", "DONE", "SESSION s-41",
			"BRANCH rowan/card-9602", "REPO mas-bandwidth/nova-tools",
		}) + benchJobListing(stella, []string{
			"RESULT card-9603 sha=abc", "DONE", "SESSION s-42",
			"BRANCH stella/card-9603", "REPO mas-bandwidth/nova-tools",
		}), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.Session = "s-42"
	in.BranchPrefix = "rowan/"
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s\n%s", code, out, errb)
	}
	if len(forge.opened) != 1 || forge.opened[0].branch != "rowan/card-9601" {
		t.Fatalf("PRs opened = %+v, want only rowan/card-9601", forge.opened)
	}
	if !strings.Contains(out, "reason=session") {
		t.Errorf("the job of another session was not named with its reason:\n%s", out)
	}
	if !strings.Contains(out, "reason=branch-prefix") {
		t.Errorf("the job on another line's branch was not named with its reason:\n%s", out)
	}
}

// Edge 13: the script's no-commit guard was hardcoded to origin/dev, so a job cut onto main
// was measured against the wrong base. The base is the card's, and a job that committed
// nothing is NOT pushed and is marked no-commit.
func TestHarvestBenchMarksANoCommitJobAgainstTheCardsBase(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, map[string]string{"origin/main..refs/harvest/rowan/card-9601": "0"})
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-9601 sha=abc", "DONE", "BASE main",
			"BRANCH rowan/card-9601", "REPO mas-bandwidth/schema",
		}), nil
	}}
	forge := &fakeForge{}
	code, out, errb := runBenchHarvest(t, benchHarvestInput(t, root, shell, forge))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s\n%s", code, out, errb)
	}
	log, _ := os.ReadFile(arglog)
	if !strings.Contains(string(log), "rev-list --count origin/main..refs/harvest/rowan/card-9601") {
		t.Errorf("the commit count was not taken against the card's base:\n%s", log)
	}
	if strings.Contains(string(log), "git -C") && strings.Contains(string(log), "push origin") {
		t.Errorf("a job that committed nothing was pushed:\n%s", log)
	}
	if !strings.Contains(out, "HARVEST NO-COMMIT") || !strings.Contains(out, "base=main") {
		t.Errorf("the no-commit job was not marked against its base:\n%s", out)
	}
	if len(forge.opened) != 0 {
		t.Errorf("a PR was opened for a job that committed nothing: %+v", forge.opened)
	}
}

// Edge 14: the script hardcoded schema's PR base to main. The PR base is the card's base,
// whatever repo it is in.
func TestHarvestBenchOpensThePRAgainstTheCardsBase(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-9601 sha=abc", "DONE", "BASE rowan/cut-fill-edges",
			"BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
		}), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.Base = "dev"
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s\n%s", code, out, errb)
	}
	if len(forge.opened) != 1 {
		t.Fatalf("PRs opened = %d, want 1", len(forge.opened))
	}
	if got := forge.opened[0].base; got != "rowan/cut-fill-edges" {
		t.Errorf("PR base = %q, want the card's base rowan/cut-fill-edges", got)
	}
}

// Edge 15: the script cut the PR title at 110 characters mid-word and then appended
// ` (<label>, <bench>)`, so the title lost a word and ran past the cap. The title is cut at
// a word boundary and the suffix is kept.
func TestHarvestPRTitleCutsAtAWordBoundaryAndKeepsTheSuffix(t *testing.T) {
	long := "RESULT: card-9601 the fixed table reader refuses a row whose declared width disagrees with the body it carries, red first"
	head := strings.TrimPrefix(long, "RESULT: ")
	got := prTitle(long, "card-9601", "hulk")
	suffix := " (card-9601, hulk)"
	if !strings.HasSuffix(got, suffix) {
		t.Fatalf("title %q does not keep the suffix %q", got, suffix)
	}
	if len(got) > prTitleMax {
		t.Errorf("title is %d bytes, over the %d cap: %q", len(got), prTitleMax, got)
	}
	cut := strings.TrimSuffix(strings.TrimSuffix(got, suffix), "...")
	if !strings.HasPrefix(head, cut) {
		t.Fatalf("the cut title is not a prefix of the RESULT line: %q", cut)
	}
	if rest := head[len(cut):]; rest != "" && !strings.HasPrefix(rest, " ") {
		t.Errorf("the title was cut mid-word (%q follows the cut): %q", rest, got)
	}
}

// Edge 15, through the verb: the title the forge is handed is the cut one.
func TestHarvestBenchHandsTheForgeTheCutTitle(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	line1 := "RESULT: card-9601 the fixed table reader refuses a row whose declared width disagrees with the body it carries, red first"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{line1, "DONE", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools"}), nil
	}}
	forge := &fakeForge{}
	if code, out, errb := runBenchHarvest(t, benchHarvestInput(t, root, shell, forge)); code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	if len(forge.opened) != 1 {
		t.Fatalf("PRs opened = %d, want 1", len(forge.opened))
	}
	if got := forge.opened[0].title; got != prTitle(line1, "card-9601", "hulk") {
		t.Errorf("PR title = %q, want the cut title %q", got, prTitle(line1, "card-9601", "hulk"))
	}
}

// Edge 10: nothing drained --launched except manager, so a lane taken by a finished card
// stayed occupied forever. harvest releases the lane of a card whose job is done -- a
// RESULT.md on the bench, or a job directory that is gone -- and moves the card out of
// --launched with a marker.
func TestHarvestReleasesTheLaneOfAFinishedCard(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, lane string }{
		{"card-9601.md", "schema"}, {"card-9602.md", "pulse"}, {"card-9603.md", "bus"},
	} {
		if err := os.WriteFile(filepath.Join(launched, c.name), []byte("RESULT "+c.name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := "lane=" + c.lane + "\nbench=hulk\nlabel=" + strings.TrimSuffix(c.name, ".md") + "\n"
		if err := os.WriteFile(filepath.Join(launched, c.name+".launched"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	done := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	running := "/home/gaffer/rowan-swarm-root/1/jobs/card-9602"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(done, []string{
			"RESULT card-9601 sha=abc", "DONE", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
		}) + benchJobListing(running, nil), nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Launched = launched
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	// The finished card left --launched, and its marker went with it: the lane is free.
	if _, err := os.Stat(filepath.Join(launched, "card-9601.md")); !os.IsNotExist(err) {
		t.Error("the finished card is still under --launched, holding its lane")
	}
	if _, err := os.Stat(filepath.Join(launched, "card-9601.md.launched")); !os.IsNotExist(err) {
		t.Error("the finished card's launched marker is still there")
	}
	if !strings.Contains(out, "HARVEST DRAIN card=card-9601.md lane=schema state=done") {
		t.Errorf("the drain did not name the card and its lane:\n%s", out)
	}
	// The running card is still live and still holds its lane.
	if _, err := os.Stat(filepath.Join(launched, "card-9602.md")); err != nil {
		t.Error("a card whose job is still running was drained")
	}
	// The card whose job directory is gone is failed, not left forever.
	if !strings.Contains(out, "HARVEST DRAIN card=card-9603.md lane=bus state=failed") {
		t.Errorf("the card whose job directory is gone was not failed:\n%s", out)
	}
	movedDone := filepath.Join(root, "queue", "done", "card-9601.md")
	if _, err := os.Stat(movedDone); err != nil {
		t.Errorf("the finished card was not moved to the done directory: %v", err)
	}
	markers, _ := filepath.Glob(filepath.Join(root, "queue", "done", "card-9601.md.done-*"))
	if len(markers) != 1 {
		t.Errorf("the drained card carries %d done markers, want 1", len(markers))
	}
	if _, err := os.Stat(filepath.Join(root, "queue", "failed", "card-9603.md")); err != nil {
		t.Errorf("the card whose job is gone was not moved to the failed directory: %v", err)
	}
}

// TestHarvestBenchRefusesARunnerHostBeforeTheFirstSSH: Glenn's lock of 2026-09-18 -- runner
// hosts are CI-only. A bench harvest opens an ssh to the machine it names, so the NAME is
// held against the machines registry at the verb's own edge, before any connection. The
// refusal names the machine, the reason and the remedy, and the shell is never opened: the
// four helpers below that guard (prTitle, benchPRBody, benchRepoURL, markHarvested) are
// reached through nothing else, which is why they are narrowings in the bench-name list.
func TestHarvestBenchRefusesARunnerHostBeforeTheFirstSSH(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		t.Errorf("an ssh was opened to %q; the registry guard runs BEFORE the first connection", bench)
		return "", nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.Bench = "batman"
	in.Machines = machinesFile(t, t.TempDir(), []string{"hulk"}, []string{"batman"})
	code, out, errb := runBenchHarvest(t, in)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a runner host\nstdout=%s\nstderr=%s", code, out, errb)
	}
	if !strings.Contains(errb, "HARVEST REFUSED bench=batman reason=runner-host") {
		t.Errorf("the refusal is not the registry's line under this verb's token: %q", errb)
	}
	if len(forge.opened) != 0 {
		t.Errorf("PRs opened = %d, want 0: nothing runs past the refusal", len(forge.opened))
	}
}

// TestHarvestBenchHarvestsABenchInTheRegistry: the same registry, the bench beside the
// runner host, and the verb runs exactly as it did -- the guard refuses what it must and
// nothing else.
func TestHarvestBenchHarvestsABenchInTheRegistry(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-9601 sha=abc",
			"DONE",
			"BRANCH rowan/card-9601",
			"REPO mas-bandwidth/nova-tools",
		}), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.Machines = machinesFile(t, t.TempDir(), []string{"hulk"}, []string{"batman"})
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", code, out, errb)
	}
	if len(forge.opened) != 1 {
		t.Fatalf("PRs opened = %d, want 1: the guard let the bench through", len(forge.opened))
	}
}

// JOHNNY'S HOLD, the repo destination on `harvest --bench`: a RESULT.md REPO line that is
// not the job's own clone origin must push nothing and open no PR (issue #1824's receipt,
// on the --bench path). The branch is in-prefix, so the branch rule is not what stops it.
func TestHarvestBenchRefusesARepoMismatchAndOpensNoPR(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 3, Equals: "rev-list", Stdout: "3"},
		{Arg: 3, Equals: "rev-parse", Stdout: "abc1234"},
		{Arg: 3, Equals: "remote", Stdout: "https://example.com/real/repo.git"},
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
		t.Fatalf("--bench opened a PR on a repo the worker's RESULT claimed: %+v", forge.opened)
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") && strings.Contains(l, "attacker/exfil") {
			t.Fatalf("--bench pushed a repo the worker's RESULT claimed: %s", l)
		}
	}
	if !strings.Contains(out, "HARVEST REFUSED repo-mismatch card=card-1 origin=real/repo") {
		t.Fatalf("the repo-mismatch refusal line is absent:\n%s", out)
	}
}

// TestHarvestBenchRefusesAMachineTheRegistryDoesNotCarry: an unknown name is a refusal too,
// not a guess -- the registry answers what it knows and never more.
func TestHarvestBenchRefusesAMachineTheRegistryDoesNotCarry(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		t.Errorf("an ssh was opened to %q; an unknown name never reaches a machine", bench)
		return "", nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "nowhere"
	in.Machines = machinesFile(t, t.TempDir(), []string{"hulk"}, nil)
	code, out, errb := runBenchHarvest(t, in)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a name the registry does not carry\nstdout=%s\nstderr=%s", code, out, errb)
	}
	if !strings.Contains(errb, "reason=unknown") {
		t.Errorf("the refusal does not say the name is unknown: %q", errb)
	}
}
