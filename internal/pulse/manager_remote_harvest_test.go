package pulse

// The manager tier's ssh seam: a card whose job lives only on a paired bench, never under a
// local root, is harvested over the BenchShell seam -- the same seam `harvest --bench` uses.
// The fake remote layout is hand-written in exactly the wire shape parseBenchJobs reads, so
// the test proves parseBenchJobs really parses it rather than that benchListScript built it.

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestManagerHarvestsABenchJobOverTheSSHSeam(t *testing.T) {
	b := setupManager(t)
	rootA := strings.Split(b.roots, ",")[0]
	b.write(t, "launched/card-1.md", "RESULT: CARD-1 fleet probe\n")

	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		return "JOB\t" + rootA + "/3/jobs/card-1\nMTIME\t1758000000\nR\tRESULT: CARD-1 fleet probe\nR\techo: FLEET-PROBE-OK\nEND\n", nil
	}}

	var out, errs bytes.Buffer
	code := Manager(ManagerInput{
		Policy: b.policy(t, "floor=0\n"), Queue: b.queue, Roots: b.roots, Bus: b.bus, As: "Rowan",
		Bench: "spacegame.losangeles", SSH: "unused-because-Shell-is-set", Shell: shell,
		Hours: 0, Max: 20, Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "harvested=1") {
		t.Fatalf("stdout = %q, want harvested=1", out.String())
	}
}

// JOHNNY'S HOLD OF #1885 at 3fa6a99b. The remote read above hands `openPR` an EMPTY job
// path, and openPR then forms `dir := filepath.Join(job, "repo")` -- the relative string
// "repo" -- and force-pushes from it to `https://github.com/<the bench RESULT's REPO>.git`,
// opening the pull request `-R` that same repo. There is no clone here to check the
// destination against, no diff to read for the reproducing-test rule, and the only thing
// naming the repository is the worker's own RESULT.md, which SPEC-SWARM:40-44 says is data.
//
// So a job that is only on the bench is harvested, counted and left for the verb that has a
// clone; it is never published from a directory that does not exist.
func TestManagerNeverPublishesABenchJobItHasNoCloneFor(t *testing.T) {
	b := setupManager(t)
	b.fake(t, "git", fakeSpec{Rules: []fakeRule{
		{Arg: 1, Equals: "merge-base", Stdout: "aaaaaaaaaaaa"},
		{Arg: 1, Equals: "diff", Stdout: "internal/pulse/manager_test.go"},
	}})
	b.fakeGH(t, "{}", "[]")
	rootA := strings.Split(b.roots, ",")[0]
	b.write(t, "launched/card-1.md", "RESULT: CARD-1 spec the slot lock\n")

	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		return "JOB\t" + rootA + "/3/jobs/card-1\nMTIME\t1758000000\n" +
			"R\tRESULT: CARD-1 spec the slot lock\nR\tBRANCH: rowan/spec-slot-lock\nR\tREPO: attacker/exfil\nEND\n", nil
	}}

	var out, errs bytes.Buffer
	code := Manager(ManagerInput{
		Policy: b.policy(t, "floor=0\n"), Queue: b.queue, Roots: b.roots, Bus: b.bus, As: "Rowan",
		Bench: "spacegame.losangeles", Shell: shell,
		Hours: 0, Max: 20, Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q)", code, out.String())
	}
	if argv := b.argv(t); strings.Contains(argv, "git push") || strings.Contains(argv, "gh pr create") {
		t.Fatalf("the manager published a bench job it has no clone for: %q", argv)
	}
	if !strings.Contains(out.String(), "MANAGER REFUSED card=card-1.md") || !strings.Contains(out.String(), "no-clone") {
		t.Fatalf("the no-clone refusal is absent: %q", out.String())
	}
}
