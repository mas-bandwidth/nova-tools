package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/require"
)

// fixtureCommit names deterministic fixture content before independent reads.
func fixtureCommit(symbol string) string {
	sum := sha256.Sum256([]byte(symbol))
	return fmt.Sprintf("%x", sum[:20])
}

// pinnedFixtureFinish gives model fixtures a commit pin at the normal finish
// boundary, so independent reads and final proof always refer to the same head.
func (ta *testApp) pinnedFixtureFinish(line string) string {
	ta.t.Helper()
	if !strings.HasPrefix(line, "finish ") || strings.Contains(line, "--failed") {
		return ta.ok(line)
	}
	args := split(line)
	for i, a := range args {
		if a == "--head" && i+1 < len(args) {
			if len(args[i+1]) == 40 {
				return ta.ok(line)
			}
			return ta.ok(strings.Replace(line, "--head "+args[i+1], "--head "+fixtureCommit(args[i+1]), 1))
		}
	}
	return ta.ok(line + " --head " + fixtureCommit(line))
}

// verifiedLanding is an explicit outside development event for lifecycle/display
// fixtures with no Git backend (SPEC-SPRINT section 7). Ordinary CLI merge and
// real work-branch land tests never receive this proof. Promotion tests own the
// real Git/GitHub validation; these fixtures pin deterministic accepted content.
func (ta *testApp) verifiedLanding(line string) string {
	ta.t.Helper()
	code, out, errs := ta.verifiedLandingResult(line)
	require.Equal(ta.t, 0, code, "verified development fixture: %s%s", out, errs)
	return out
}

func (ta *testApp) verifiedLandingResult(line string) (int, string, string) {
	ta.t.Helper()
	args := split(line)
	require.Equal(ta.t, "merge", args[0])
	req := sprint.MergeReq{Batch: 10, Who: "coordinator"}
	op := ""
	for i := 1; i < len(args); i += 2 {
		require.Less(ta.t, i+1, len(args))
		switch args[i] {
		case "--stream":
			req.Stream = args[i+1]
		case "--batch":
			n, err := strconv.Atoi(args[i+1])
			require.NoError(ta.t, err)
			req.Batch = n
		case "--op":
			op = args[i+1]
		default:
			ta.t.Fatalf("verified fixture does not accept %s", args[i])
		}
	}
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(ta.t, err)
	ctx := context.Background()
	snap, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	require.NoError(ta.t, err)
	req.Dev = &sprint.DevReceipt{Epoch: snap.Epoch, Repo: "fixture/repository", Branch: "dev", Tip: strings.Repeat("a", 40), CandidateTip: strings.Repeat("b", 40), CITip: strings.Repeat("b", 40), CIReceipt: "fixture exact candidate CI", Review: "fixture independent review", BatchID: "fixture-" + req.Stream, VerifiedAt: t0}
	step := store.MergeStep(req)
	step.Plan = func(s *sprint.Snapshot) sprint.Plan {
		queued := s.Merge.Cell(req.Stream, sprint.Queued)
		if req.Batch > 0 && req.Batch < len(queued) {
			queued = queued[:req.Batch]
		}
		proof := *req.Dev
		for _, c := range queued {
			pr := s.Work.Card(c.ID)
			require.NotNil(ta.t, pr)
			require.Len(ta.t, pr.F("head"), 40, "fixture finishes pin accepted content before reads")
			proof.Entries = append(proof.Entries, sprint.PinnedCard{ID: pr.ID, Head: pr.F("head"), Attempt: pr.F("attempt")})
		}
		withProof := req
		withProof.Dev = &proof
		return sprint.MergeStep(s, withProof)
	}
	var out, errs bytes.Buffer
	code := ta.a.runStep("merge", common{redis: "mem:0", actor: "coordinator", epoch: int64(snap.Epoch), op: op}, st, step, &out, &errs)
	return code, out.String(), errs.String()
}
