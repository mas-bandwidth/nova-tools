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
