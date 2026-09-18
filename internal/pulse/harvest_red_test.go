package pulse

// The red tests of the three pulse edges the schema round-3 dogfood on hulk found
// (2026-09-18), each one against the fake shell, the fake forge and the fake git on PATH:
//
//  1. harvest drained a RED job's card to done/ and released its lane as a success.
//  2. the shipped rows.md template produced a RESULT.md harvest refused four times over,
//     once per field, and cloned into the job directory rather than <job>/repo.
//  3. the four refusals were four runs; they are one line naming every missing field.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// launchedCard writes one live card and its launched marker under --launched, the way fill
// leaves them.
func launchedCard(t *testing.T, launched, name, lane string) {
	t.Helper()
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launched, name), []byte("RESULT "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "lane=" + lane + "\nbench=hulk\nlabel=" + strings.TrimSuffix(name, ".md") + "\n"
	if err := os.WriteFile(filepath.Join(launched, name+".launched"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestHarvestBenchNeverDrainsARedCardToDone is rule (1). On hulk the report line read
// `HARVEST BENCH RED ... skipped=0 drained=3`: every card drained, including the ones whose
// worker abstained, and every one of them landed in done/ with its lane released as though
// the work had arrived. A red RESULT.md is never a done card -- it is not pushed, it gets
// no PR, it goes to failed/ with the verdict on its marker, and it is counted under red=
// and not under done=.
func TestHarvestBenchNeverDrainsARedCardToDone(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	launchedCard(t, launched, "card-9601.md", "schema")
	launchedCard(t, launched, "card-9602.md", "pulse")

	green := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	red := "/home/gaffer/rowan-swarm-root/1/jobs/card-9602"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(green, []string{
			"RESULT card-9601 sha=abc", "DONE",
			"BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
		}) + benchJobListing(red, []string{
			"RESULT card-9602 sha=abc", "ABSTAIN the harness died at the deadline",
			"BRANCH rowan/card-9602", "REPO mas-bandwidth/nova-tools",
		}), nil
	}}
	forge := &fakeForge{}
	in := benchHarvestInput(t, root, shell, forge)
	in.Launched = launched
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	// The red job is counted apart from the done one, and neither count swallows the other.
	if !strings.Contains(out, "done=1 red=1") {
		t.Errorf("the HARVEST BENCH line does not count red apart from done:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST RED bench=hulk label=card-9602 verdict=ABSTAIN") {
		t.Errorf("the red job was not named with its verdict:\n%s", out)
	}
	// Nothing of the red job was published: no push, no PR.
	if len(forge.opened) != 1 || forge.opened[0].branch != "rowan/card-9601" {
		t.Fatalf("PRs opened = %+v, want only the green card's", forge.opened)
	}
	log, _ := os.ReadFile(arglog)
	if strings.Contains(string(log), "refs/heads/rowan/card-9602") {
		t.Errorf("the red job's branch was pushed:\n%s", log)
	}
	// The lane is released -- but to failed/, never to done/.
	if _, err := os.Stat(filepath.Join(root, "queue", "done", "card-9602.md")); err == nil {
		t.Error("the red card landed in done/; a red result is never a done card")
	}
	if _, err := os.Stat(filepath.Join(root, "queue", "failed", "card-9602.md")); err != nil {
		t.Errorf("the red card did not land in failed/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(launched, "card-9602.md.launched")); !os.IsNotExist(err) {
		t.Error("the red card still holds its lane: its launched marker is still there")
	}
	if !strings.Contains(out, "HARVEST DRAIN card=card-9602.md lane=pulse state=failed") {
		t.Errorf("the drain did not send the red card to failed with its lane named:\n%s", out)
	}
	markers, _ := filepath.Glob(filepath.Join(root, "queue", "failed", "card-9602.md.failed-*"))
	if len(markers) != 1 {
		t.Fatalf("the red card carries %d failed markers, want 1", len(markers))
	}
	note, _ := os.ReadFile(markers[0])
	if !strings.Contains(string(note), "why=red-abstain") {
		t.Errorf("the marker does not say why the card failed: %q", note)
	}
	// And the green card is exactly where it was before.
	if _, err := os.Stat(filepath.Join(root, "queue", "done", "card-9601.md")); err != nil {
		t.Errorf("the green card did not land in done/: %v", err)
	}
}

// TestHarvestBenchTakesANoCommitJobAsRed is the other half of rule (1): a job that
// committed nothing did not do the work either, whatever its verdict said. It was counted
// as done and its card drained to done/, so nobody ever cut it again.
func TestHarvestBenchTakesANoCommitJobAsRed(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, map[string]string{"origin/dev..refs/harvest/rowan/card-9601": "0"})
	launched := filepath.Join(root, "queue", "launched")
	launchedCard(t, launched, "card-9601.md", "schema")
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT card-9601 sha=abc", "DONE",
			"BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
		}), nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Launched = launched
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "HARVEST NO-COMMIT") {
		t.Fatalf("the job that committed nothing was not named:\n%s", out)
	}
	if !strings.Contains(out, "done=0") {
		t.Errorf("a job that committed nothing was counted as done:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "queue", "failed", "card-9601.md")); err != nil {
		t.Errorf("the no-commit card did not land in failed/: %v", err)
	}
	markers, _ := filepath.Glob(filepath.Join(root, "queue", "failed", "card-9601.md.failed-*"))
	if len(markers) != 1 {
		t.Fatalf("the no-commit card carries %d failed markers, want 1", len(markers))
	}
	if note, _ := os.ReadFile(markers[0]); !strings.Contains(string(note), "why=no-commit") {
		t.Errorf("the marker does not say the job committed nothing: %q", note)
	}
}

// TestHarvestBenchFoldsEveryMissingFieldIntoOneLine is rule (3). A RESULT.md with neither a
// BRANCH nor a REPO line, harvested with no --clone, was refused three times over three
// runs: each guard returned before the next one looked, so a person fixed one thing, ran
// again, and learnt the next. One line now names all of them, with the one remedy.
func TestHarvestBenchFoldsEveryMissingFieldIntoOneLine(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	job := "/home/gaffer/rowan-swarm-root/0/jobs/card-9601"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{"RESULT card-9601 sha=abc", "DONE"}), nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Clones = nil
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "HARVEST SKIP") {
			if line != "" {
				t.Fatalf("the job was refused on more than one line:\n%s", out)
			}
			line = l
		}
	}
	if line == "" {
		t.Fatalf("the job with no fields at all was not refused:\n%s", out)
	}
	if !strings.Contains(line, "reason=fields") {
		t.Errorf("the refusal does not carry the folded reason: %q", line)
	}
	for _, want := range []string{"BRANCH", "REPO"} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal does not name the missing %s: %q", want, line)
		}
	}
	if !strings.Contains(line, "--clone") {
		t.Errorf("the refusal does not name the missing --clone: %q", line)
	}
	if !strings.Contains(line, "card.go") {
		t.Errorf("the remedy does not point at the one grammar: %q", line)
	}
}

// TestResultVerdictReadsTheContractsThreeWords holds the grammar itself: DONE is the only
// green word, the spec's other two are red, and a RESULT.md with no verdict at all is red
// rather than quietly done.
func TestResultVerdictReadsTheContractsThreeWords(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lines   []string
		verdict string
		red     bool
	}{
		{"done", []string{"RESULT card-1 sha=abc", "DONE"}, VerdictDone, false},
		{"done after the fields", []string{"RESULT card-1", "BRANCH rowan/x", "DONE"}, VerdictDone, false},
		{"abstain", []string{"RESULT card-1", "ABSTAIN no key"}, VerdictAbstain, true},
		{"blocked", []string{"RESULT card-1", "BLOCKED head moved"}, VerdictBlocked, true},
		{"red", []string{"RESULT card-1", "RED: the reader test"}, "RED", true},
		{"no verdict at all", []string{"RESULT card-1", "BRANCH rowan/x"}, VerdictNone, true},
		{"line 1 is never the verdict", []string{"DONE card-1"}, VerdictNone, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, red := ResultVerdict(tc.lines)
			if got != tc.verdict || red != tc.red {
				t.Fatalf("ResultVerdict = (%q, %v), want (%q, %v)", got, red, tc.verdict, tc.red)
			}
		})
	}
}

// TestHarvestBenchRedJobIsDrainedEvenWhenAlreadyHarvested: the `.harvested` marker says the
// PR was considered, never that the card landed. A red job already marked on the bench
// still releases its lane to failed/ -- otherwise the first harvest after the fix would
// leave the lane held forever.
func TestHarvestBenchRedJobIsDrainedEvenWhenAlreadyHarvested(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	launchedCard(t, launched, "card-9602.md", "pulse")
	job := "/home/gaffer/rowan-swarm-root/1/jobs/card-9602"
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return "JOB\t" + job + "\nMTIME\t0\nHARVESTED\nR\tRESULT card-9602 sha=abc\nR\tBLOCKED head moved\nEND\n", nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Launched = launched
	var out, errb bytes.Buffer
	in.Stdout, in.Stderr = &out, &errb
	if code := Harvest(in); code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, "queue", "failed", "card-9602.md")); err != nil {
		t.Errorf("an already-harvested red job did not release its lane to failed/: %v", err)
	}
}
