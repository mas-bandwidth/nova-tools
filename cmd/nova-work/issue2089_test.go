package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nova-tools#2089, as its title states it: "nova-work link-mode dogfood: measure
// tokens and wall clock per question, and log every drift with its repair cost
// (the inputs to the absorb decision)". Glenn, 2026-09-20: operate in link mode
// for a while and dogfood, before deciding on absorb mode -- and the dogfood is
// only useful for that decision if it produces numbers from day one. So the
// machinery writes two ledgers, neither estimated, and this test drives them the
// way the dogfood would: one cost row per question kind (tokens and wall clock,
// answered from nova-work in link mode against today's way, gh issue list/view
// output read into a model's context), one row per drift (what differed, which
// side was right, how it arose, how it was found, the tokens and minutes the
// repair cost, and whether a decision was made on the stale copy before it was
// caught), and `dogfood report` printing both tables with the signals named in
// advance so nobody argues after the fact.
//
// The test speaks only the CLI surface and reads the ledger files as JSON: the
// ledgers ARE the record ("both written by machinery"), so what is on disk is
// asserted, not just what was printed.
func TestIssue2089(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "dogfood-ledger")

	// Ledger 1, cost per question. Two kinds answered both ways -- so the
	// report can put link against gh on the same kind -- and one kind answered
	// link-only, the row whose gh side is a dash rather than a guessed zero:
	// a side that was never measured is not a side that measured zero.
	questions := []struct {
		kind, mode, tokens, wall, now string
	}{
		{"uid", "link", "840", "312ms", "2026-09-20T09:00:00Z"},
		{"uid", "gh", "12000", "4m10s", "2026-09-20T09:05:00Z"},
		{"percent", "link", "400", "100ms", "2026-09-21T09:00:00Z"},
		{"percent", "gh", "8000", "1m0s", "2026-09-21T09:05:00Z"},
		{"subtree", "link", "650", "250ms", "2026-09-22T09:00:00Z"},
	}
	for _, q := range questions {
		code, stdout, stderr := invoke("dogfood", "question", "--ledger", ledger,
			"--kind", q.kind, "--mode", q.mode, "--tokens", q.tokens, "--wall", q.wall, "--now", q.now)
		if code != 0 {
			t.Fatalf("dogfood question %s %s exit=%d, want 0\nstdout: %s\nstderr: %s", q.kind, q.mode, code, stdout, stderr)
		}
		want := "DOGFOOD QUESTION OK kind=" + q.kind + " mode=" + q.mode +
			" tokens=" + q.tokens + " wall=" + q.wall + " at=" + q.now
		if !strings.Contains(stdout, want) {
			t.Fatalf("dogfood question %s %s printed\n%s\nwant a line carrying\n%s", q.kind, q.mode, stdout, want)
		}
	}

	// Ledger 2, the drift log. One drift reconcile found and repaired
	// unattended, and one a person found -- late, after a decision had already
	// been made on the stale copy: both halves of the signal the report owes
	// the absorb decision.
	drifts := []struct {
		args []string
		want string
	}{
		{
			args: []string{"--what", "issue 2081 title", "--right", "gh", "--arose", "issue-transfer",
				"--found", "reconcile", "--repair-tokens", "900", "--repair-minutes", "4", "--now", "2026-09-22T10:00:00Z"},
			want: "DOGFOOD DRIFT OK what=issue\\x202081\\x20title right=gh arose=issue-transfer found=reconcile" +
				" repair-tokens=900 repair-minutes=4 decision-on-stale=false at=2026-09-22T10:00:00Z",
		},
		{
			args: []string{"--what", "card-2548-state", "--right", "link", "--arose", "missed-capture",
				"--found", "person", "--repair-tokens", "1500", "--repair-minutes", "25", "--decision-on-stale", "--now", "2026-09-22T11:00:00Z"},
			want: "DOGFOOD DRIFT OK what=card-2548-state right=link arose=missed-capture found=person" +
				" repair-tokens=1500 repair-minutes=25 decision-on-stale=true at=2026-09-22T11:00:00Z",
		},
	}
	for _, d := range drifts {
		args := append([]string{"dogfood", "drift", "--ledger", ledger}, d.args...)
		code, stdout, stderr := invoke(args...)
		if code != 0 {
			t.Fatalf("dogfood drift exit=%d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		if !strings.Contains(stdout, d.want) {
			t.Fatalf("dogfood drift printed\n%s\nwant a line carrying\n%s", stdout, d.want)
		}
	}

	// The ledgers are the record. Five question rows and two drift rows are on
	// disk as JSON lines, holding what was measured -- a row that existed only
	// in this run's output would be a number nobody could audit later.
	type questionRow struct {
		Kind   string `json:"kind"`
		Mode   string `json:"mode"`
		Tokens int64  `json:"tokens"`
		WallMS int64  `json:"wall_ms"`
		At     string `json:"at"`
	}
	raw, err := os.ReadFile(filepath.Join(ledger, "questions.jsonl"))
	if err != nil {
		t.Fatalf("read the question ledger: %v", err)
	}
	var qrows []questionRow
	for i, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r questionRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("questions.jsonl line %d is not a row: %v", i+1, err)
		}
		qrows = append(qrows, r)
	}
	if len(qrows) != 5 {
		t.Fatalf("questions.jsonl holds %d rows, want 5:\n%s", len(qrows), raw)
	}
	if qrows[0] != (questionRow{Kind: "uid", Mode: "link", Tokens: 840, WallMS: 312, At: "2026-09-20T09:00:00Z"}) {
		t.Fatalf("the first question row is not what was measured: %+v", qrows[0])
	}
	type driftRow struct {
		What            string `json:"what"`
		Right           string `json:"right"`
		Arose           string `json:"arose"`
		Found           string `json:"found"`
		RepairTokens    int64  `json:"repair_tokens"`
		RepairMinutes   int64  `json:"repair_minutes"`
		DecisionOnStale bool   `json:"decision_on_stale"`
		At              string `json:"at"`
	}
	draw, err := os.ReadFile(filepath.Join(ledger, "drift.jsonl"))
	if err != nil {
		t.Fatalf("read the drift ledger: %v", err)
	}
	var drows []driftRow
	for i, line := range strings.Split(strings.TrimSuffix(string(draw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r driftRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("drift.jsonl line %d is not a row: %v", i+1, err)
		}
		drows = append(drows, r)
	}
	if len(drows) != 2 {
		t.Fatalf("drift.jsonl holds %d rows, want 2:\n%s", len(drows), draw)
	}
	if drows[0].RepairTokens != 900 || drows[0].RepairMinutes != 4 || drows[0].DecisionOnStale {
		t.Fatalf("the first drift row does not carry its repair cost: %+v", drows[0])
	}
	if !drows[1].DecisionOnStale {
		t.Fatalf("the second drift row does not carry the stale-copy decision: %+v", drows[1])
	}

	// The report: one table a month, or on demand. The cost table puts link
	// against gh per kind (tokens and wall clock, the means of the runs, and
	// the saving), the drift table carries every row with its repair cost, and
	// the signals are the ones the issue named in advance: drift that needs a
	// person, or any decision made on a stale copy, is sync pain; a per-question
	// saving that is large and obvious across most kinds is "radical". The
	// round trips that stay on GitHub regardless are printed once, because
	// absorb cannot remove them and they cap the possible saving.
	code, stdout, stderr := invoke("dogfood", "report", "--ledger", ledger)
	if code != 0 {
		t.Fatalf("dogfood report exit=%d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{
		"DOGFOOD REPORT questions=5 drifts=2",
		"DOGFOOD COST kind=uid link-runs=1 gh-runs=1 link-tokens=840 gh-tokens=12000 link-wall=312ms gh-wall=4m10s saving-tokens=93 saving-wall=99",
		"DOGFOOD COST kind=subtree link-runs=1 gh-runs=0 link-tokens=650 gh-tokens=- link-wall=250ms gh-wall=- saving-tokens=- saving-wall=-",
		"DOGFOOD COST kind=percent link-runs=1 gh-runs=1 link-tokens=400 gh-tokens=8000 link-wall=100ms gh-wall=1m0s saving-tokens=95 saving-wall=99",
		"DOGFOOD DRIFT row=1 what=issue\\x202081\\x20title right=gh arose=issue-transfer found=reconcile repair-tokens=900 repair-minutes=4 decision-on-stale=false",
		"DOGFOOD DRIFT row=2 what=card-2548-state right=link arose=missed-capture found=person repair-tokens=1500 repair-minutes=25 decision-on-stale=true",
		"DOGFOOD SIGNAL name=drift-repairs-unattended state=sync-pain drifts=2 needing-a-person=1",
		"DOGFOOD SIGNAL name=decision-on-a-stale-copy state=sync-pain made=1",
		"DOGFOOD SIGNAL name=per-question-saving state=radical kinds-both-ways=2 large-and-obvious=2",
		"DOGFOOD CAP stays-on-github=prs,reviews,ci caps=the-possible-saving",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the report does not carry\n%s\nreport:\n%s", want, stdout)
		}
	}

	// One table a month: --from and --to bound the rows the table counts. The
	// September-21 window drops the two September-20 uid rows and keeps both
	// drifts; a window that closed before the dogfood began prints the empty
	// table, whose signals are the quiet states -- no drift, nothing decided on
	// a stale copy, and no kind measured both ways yet.
	code, stdout, stderr = invoke("dogfood", "report", "--ledger", ledger, "--from", "2026-09-21T00:00:00Z")
	if code != 0 {
		t.Fatalf("dogfood report --from exit=%d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "DOGFOOD REPORT questions=3 drifts=2") {
		t.Errorf("the monthly window did not drop the rows before it:\n%s", stdout)
	}
	code, stdout, stderr = invoke("dogfood", "report", "--ledger", ledger, "--to", "2026-09-19T00:00:00Z")
	if code != 0 {
		t.Fatalf("dogfood report --to exit=%d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{
		"DOGFOOD REPORT questions=0 drifts=0",
		"DOGFOOD SIGNAL name=drift-repairs-unattended state=fine drifts=0 needing-a-person=0",
		"DOGFOOD SIGNAL name=decision-on-a-stale-copy state=fine made=0",
		"DOGFOOD SIGNAL name=per-question-saving state=not-yet kinds-both-ways=0 large-and-obvious=0",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the empty table does not carry\n%s\nreport:\n%s", want, stdout)
		}
	}

	// The drift table is a listing, so it is a cap and a count: --max 1 shows
	// one row and stands the other behind one MORE line.
	code, stdout, stderr = invoke("dogfood", "report", "--ledger", ledger, "--max", "1")
	if code != 0 {
		t.Fatalf("dogfood report --max 1 exit=%d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "DOGFOOD MORE kind=drift shown=1 total=2 widen --max or pass --max 0") {
		t.Errorf("the drift table's cap is not a cap and a count:\n%s", stdout)
	}

	// What cannot run refuses at 2, naming what the input WANTS and never
	// guessing: no sub-verb, an unknown sub-verb, a missing ledger, a kind or a
	// mode or a cause or a finder outside the closed lists, a measurement with
	// no time in it, a drift without its repair cost, a report over a ledger
	// that is not there, and a --now that is not an instant.
	refusals := []struct {
		name string
		args []string
		want string
	}{
		{"no sub-verb", []string{"dogfood"}, "the three are question, drift and report"},
		{"unknown sub-verb", []string{"dogfood", "bogus"}, "unknown sub-verb"},
		{"question without a ledger", []string{"dogfood", "question", "--kind", "uid", "--mode", "link", "--tokens", "840", "--wall", "312ms"}, "--ledger is required; it wants"},
		{"unknown kind", []string{"dogfood", "question", "--ledger", ledger, "--kind", "bogus", "--mode", "link", "--tokens", "840", "--wall", "312ms"}, "uid, number, subtree, since, blocks or percent"},
		{"unknown mode", []string{"dogfood", "question", "--ledger", ledger, "--kind", "uid", "--mode", "web", "--tokens", "840", "--wall", "312ms"}, "one of link or gh"},
		{"a wall of zero", []string{"dogfood", "question", "--ledger", ledger, "--kind", "uid", "--mode", "link", "--tokens", "840", "--wall", "0s"}, "--wall is required"},
		{"a drift without its repair cost", []string{"dogfood", "drift", "--ledger", ledger, "--what", "x", "--right", "gh", "--arose", "edit", "--found", "reconcile"}, "--repair-tokens is required"},
		{"a drift with an unknown cause", []string{"dogfood", "drift", "--ledger", ledger, "--what", "x", "--right", "gh", "--arose", "gremlin", "--found", "reconcile", "--repair-tokens", "10", "--repair-minutes", "1"}, "edit, issue-transfer, repo-rename, missed-capture, failed-back-pointer or human-edit"},
		{"report over a ledger that is not there", []string{"dogfood", "report", "--ledger", filepath.Join(t.TempDir(), "absent-ledger")}, "refusing to guess"},
		{"a report window that ends before it starts", []string{"dogfood", "report", "--ledger", ledger, "--from", "2026-09-22T00:00:00Z", "--to", "2026-09-20T00:00:00Z"}, "a window that ends before it starts"},
		{"an unparsable now", []string{"dogfood", "question", "--ledger", ledger, "--kind", "uid", "--mode", "link", "--tokens", "840", "--wall", "312ms", "--now", "yesterday"}, "is not an RFC3339 instant"},
	}
	for _, r := range refusals {
		code, stdout, stderr := invoke(r.args...)
		if code != 2 {
			t.Errorf("%s: exit=%d, want 2\nstdout: %s\nstderr: %s", r.name, code, stdout, stderr)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("%s: a refusal wrote stdout: %q", r.name, stdout)
		}
		if !strings.Contains(stderr, r.want) {
			t.Errorf("%s: the refusal does not name %q\nstderr: %s", r.name, r.want, stderr)
		}
	}
}
