package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// --parallel is the form nova-sprint's own runner had (its Makefile passes
// TLC_PARALLEL through it): independent cases at once, 1 to half the CPUs, a
// quarter of them by default.

func TestRunRunsIndependentCasesInParallelAndRecordsThemInPlanOrder(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	var runs []tlc.Run
	dir := filepath.Join(t.TempDir(), "out")
	r := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13}, &runs), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha", "--timeout", "100s")
	if r.code != 0 || r.stderr != "" || len(runs) != 2 {
		t.Fatalf("the default width (a quarter of the 8 CPUs) ran: %+v, %d runs", r, len(runs))
	}
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	if err != nil || len(recs) != 2 || recs[0].Config != "MCA.cfg" || recs[1].Config != "MCABroken.cfg" {
		t.Fatalf("the records are not in plan order: %v %v", recs, err)
	}
}

func TestRunRefusesAParallelWidthOutsideTheCPUs(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	for _, width := range []string{"5", "-1"} {
		r := call(t, nil, "run", "--root", root, "--jar", jar, "--dir", t.TempDir(), "--group", "alpha", "--parallel", width)
		if r.code != 2 || !strings.Contains(r.stderr, "parallel must be between 1 and 4 (half the 8 CPUs)") || !strings.Contains(r.stderr, "nothing was run") {
			t.Errorf("--parallel %s: %+v", width, r)
		}
	}
	r := call(t, nil, "run", "--root", root, "--dir", t.TempDir(), "--bench", "vision", "--parallel", "2")
	if r.code != 2 || !strings.Contains(r.stderr, "--parallel is not taken with --bench") {
		t.Errorf("--parallel with --bench: %+v", r)
	}
}
