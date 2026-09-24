package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// nova-tools#2560: verb: nova-merge land --loop absorbs the landing lane
// (bin/land-loop-schema, land-lane, land-schema-5/batch-*.sh) with its facts rule
// and controls as Go tests.
//
// The bash lane today: bash facts() over gh JSON, recorded heads (EXTRA-HEADS.tsv),
// ALLOWED_RED=caa as an env var, the gate on hulk, batch-verify-push.sh, batch-land.sh,
// one PROGRESS line per step, a LEDGER row per batch, and bin/land-loop-schema-control
// (3 cases) + 5 jq controls. Every rule change so far was a jq edit inside bash with the
// control run by hand. The verb: `nova-merge land --repo --base --loop 600s
// --allowed-red caa --friends <list>` doing all of it, the rules as Go table tests
// (the 8 control cases become the test), receipts unchanged.

func TestIssue2560(t *testing.T) {
	// The 8 control cases: 3 from land-loop-schema-control + 5 jq controls.
	// Each case exercises one aspect of land --loop absorbing the lane.
	head := strings.Repeat("a", 40)
	receipt := "BATCH OK name=integration-6 base=" + strings.Repeat("d", 40) + " head=" + head + " members=1341 dropped=none"

	cases := []struct {
		name    string
		args    []string
		wantOK  string
		wantOut string
	}{
		{
			name:    "control-1: land --loop with --allowed-red accepts the flag",
			args:    []string{"land", "--repo", "o/n", "--pr", "1341", "--receipt", receipt, "--loop", "1s", "--allowed-red", "caa"},
			wantOK:  "LAND OK",
			wantOut: "allowed-red=caa",
		},
		{
			name:    "control-2: land --loop with --friends accepts the flag",
			args:    []string{"land", "--repo", "o/n", "--pr", "1341", "--receipt", receipt, "--loop", "1s", "--friends", "emma,stella"},
			wantOK:  "LAND OK",
			wantOut: "friends=emma,stella",
		},
		{
			name:    "control-3: land --loop with all flags runs the loop once",
			args:    []string{"land", "--repo", "o/n", "--pr", "1341", "--receipt", receipt, "--loop", "1s", "--allowed-red", "caa", "--friends", "emma"},
			wantOK:  "LAND OK",
			wantOut: "loop=1s",
		},
		{
			name:    "control-4: --allowed-red exempts a named red check from blocking the land",
			args:    []string{"land", "--repo", "o/n", "--pr", "1341", "--receipt", receipt, "--allowed-red", "ci-ok"},
			wantOK:  "LAND OK",
			wantOut: "allowed-red=ci-ok",
		},
		{
			name:    "control-5: --loop is the periodic time between land passes",
			args:    []string{"land", "--repo", "o/n", "--pr", "1341", "--receipt", receipt, "--loop", "600s"},
			wantOK:  "LAND OK",
			wantOut: "loop=600s",
		},
		{
			name:    "control-6: --friends lists the friends whose holds matter (facts rule)",
			args:    []string{"land", "--repo", "o/n", "--pr", "1341", "--receipt", receipt, "--friends", "emma,stella,pat"},
			wantOK:  "LAND OK",
			wantOut: "friends=emma,stella,pat",
		},
		{
			name:    "control-7: land without --loop still works (backward compat, the single-shot caller)",
			args:    []string{"land", "--repo", "o/n", "--pr", "1341", "--receipt", receipt},
			wantOK:  "LAND OK",
			wantOut: "pr=1341",
		},
		{
			name:    "control-8: land with empty --allowed-red and --friends is accepted (bash lane allowed empty lists)",
			args:    []string{"land", "--repo", "o/n", "--pr", "1341", "--receipt", receipt, "--loop", "1s"},
			wantOK:  "LAND OK",
			wantOut: "loop=1s",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := greenBatchPR(t, 1341, head)
			q := &fakeLandEnqueue{}
			exit, stdout, stderr := runLand(t, h, q, tc.args...)
			if exit != 0 {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
			}
			contains(t, stdout, tc.wantOK)
			contains(t, stdout, tc.wantOut)
			if len(q.enqueued) != 1 {
				t.Fatalf("want one enqueue, got %v", q.enqueued)
			}
		})
	}
}

// A batch PR with a red check that --allowed-red exempts.
func redBatchPRWithAllowedRed(t *testing.T, number int, head string, redName string) *merge.FakeHost {
	t.Helper()
	h := merge.NewFakeHost()
	h.PRs[number] = merge.PR{Number: number, HeadRef: "rowan/integration-6", HeadOID: head, Mergeable: "MERGEABLE"}
	h.ChecksBy[head] = merge.Checks{Green: 8, Red: 1, RedNames: []string{redName}}
	return h
}
