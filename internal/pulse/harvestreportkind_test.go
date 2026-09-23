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

// The kind is read from the card's KIND field, never inferred from the label (Stella's
// hold on #2647): a report-kind job under a label that does not begin `report-` is still
// refused as a report, and a `report-*` label whose KIND is not report is not.
func TestHarvestBenchReportKindIsTheKindNotTheLabel(t *testing.T) {
	cases := []struct {
		name, label  string
		result       []string
		wantRefusal  bool
		wantPrefixed string
	}{
		{
			name:  "report kind under a non-report label",
			label: "card-07-read-nova-tools-2537",
			result: []string{
				"RESULT card-07-read-nova-tools-2537 sha=af6a9fccf331 — a report under an ordinary label",
				"DONE", "KIND: report", "SCHEMA: v2", "REPO: mas-bandwidth/nova-tools", "BASE: dev",
			},
			wantRefusal:  true,
			wantPrefixed: "HARVEST REFUSED kind=report ",
		},
		{
			name:  "report-shaped label of another kind",
			label: "report-lookalike-fix-1",
			result: []string{
				"RESULT report-lookalike-fix-1 sha=af6a9fccf331 — a fix whose label begins report-",
				"DONE", "KIND: fix", "SCHEMA: v2", "BRANCH other/report-lookalike-fix-1",
				"REPO: mas-bandwidth/nova-tools", "BASE: dev",
			},
			wantRefusal:  false,
			wantPrefixed: "HARVEST SKIP ",
		},
		{
			name:  "report-shaped label with no KIND",
			label: "report-nokind-1",
			result: []string{
				"RESULT report-nokind-1 sha=af6a9fccf331 — no KIND line at all",
				"DONE", "SCHEMA: v2", "REPO: mas-bandwidth/nova-tools", "BASE: dev",
			},
			wantRefusal:  false,
			wantPrefixed: "HARVEST SKIP ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, specs, arglog := setupPulse(t)
			benchGit(t, specs, arglog, nil)
			job := "/home/gaffer/rowan-swarm-root/0/jobs/" + tc.label
			shell := &fakeShell{answer: func(bench, script string) (string, error) {
				if strings.Contains(script, "touch") {
					return "", nil
				}
				return benchJobListing(job, tc.result), nil
			}}
			code, out, errb := runBenchHarvest(t, benchHarvestInput(t, root, shell, &fakeForge{}))
			var named []string
			for _, l := range strings.Split(out, "\n") {
				if strings.Contains(l, tc.label) {
					named = append(named, l)
				}
			}
			if len(named) != 1 {
				t.Fatalf("%d lines name %s, want exactly one (exit=%d):\nstdout=%s\nstderr=%s", len(named), tc.label, code, out, errb)
			}
			line := named[0]
			if got := strings.Contains(line, "kind=report"); got != tc.wantRefusal {
				t.Fatalf("report refusal=%v, want %v: %q", got, tc.wantRefusal, line)
			}
			if !strings.HasPrefix(line, tc.wantPrefixed) {
				t.Fatalf("line %q does not begin %q", line, tc.wantPrefixed)
			}
		})
	}
}
