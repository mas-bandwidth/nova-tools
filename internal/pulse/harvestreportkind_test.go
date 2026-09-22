package pulse

// The red test of the report kind on `harvest --bench` (nova-tools #2537, second
// finding): a bench job whose label begins `report-` and whose RESULT.md line 2 is DONE
// produced neither a JOB line nor a REFUSED line. Its deliverable is its RESULT.md, so it
// carries no branch to push, and the branch-prefix filter passed it by with a SKIP line
// that names a filter and an empty branch -- never its kind. A card of any kind is either
// harvested or refused out loud with a reason naming its kind; silence reads as "not
// looked at", and a coordinator cannot tell the two apart.

import (
	"strings"
	"testing"
)

func TestHarvestBenchEvaluatesReportKind(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	label := "report-nova-tools-2537-r1"
	job := "/home/gaffer/rowan-swarm-root/0/jobs/" + label
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing(job, []string{
			"RESULT report-nova-tools-2537-r1 sha=af6a9fccf331 — harvest --bench never evaluates a report-* card: card-00-report-nova-tools-2537-r1 is DONE with a repo and produces no JOB and no REFUSED line, ever",
			"DONE",
			"KIND: report",
			"SCHEMA: v2",
			"ATTEMPT: 1",
			"REPO: mas-bandwidth/nova-tools",
			"BASE: dev",
		}), nil
	}}
	forge := &fakeForge{}
	code, out, errb := runBenchHarvest(t, benchHarvestInput(t, root, shell, forge))
	var named []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, label) {
			named = append(named, l)
		}
	}
	if len(named) != 1 {
		t.Fatalf("the bench drain emitted %d lines naming %s, want exactly one (exit=%d):\nstdout=%s\nstderr=%s",
			len(named), label, code, out, errb)
	}
	line := named[0]
	if !strings.HasPrefix(line, "HARVEST JOB ") && !strings.HasPrefix(line, "HARVEST REFUSED ") {
		t.Fatalf("the one line naming %s is neither a JOB line nor a REFUSED line: %q\nfull output:\n%s", label, line, out)
	}
	if strings.HasPrefix(line, "HARVEST REFUSED ") && !strings.Contains(line, "reason=") {
		t.Fatalf("the REFUSED line carries no reason: %q", line)
	}
}
