//go:build functional

package card_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// TestCardEndStoresResultOnRecord is the DONE-WHEN control of
// nova-tools#3919 (card half), against the real ns_card_result and
// ns_card_end on a throwaway redis-server. The swarm-0925a cards ABSTAINed
// `done-already <sha>` and their record said reason=done where=done/ok and
// nothing else: line 2 lived only in a bench file. Now a card whose model
// writes `ABSTAIN done-already <sha>` ends ABSTAIN in done/abstain (the
// bench's abstain view holds it, and card fsck is clean), its record carries
// result_line1, result_line2, check, red, paths, result_paths, branch,
// commit_sha, wall_ms, model and provider, and its label is queued on
// s:<S>:done-already for the reconciler; a DONE card with a commit ends
// done/ok with commit_sha the pushed commit, and is not queued.
func TestCardEndStoresResultOnRecord(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const landed = "d20d73c4aa55"
	for _, tc := range []struct {
		name, mode string
	}{
		{"done-already", "native-done-already"},
		{"done-with-commit", "native-two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, client := newSprint(t)
			origin, base := newGoOrigin(t)
			id := card.Identity{Sprint: "swarm-0925a", Label: "i001-" + tc.name, BaseSHA: base[:8], Bench: "wrap-bench", Attempt: 1}
			token := attemptToken(1, strings.Repeat("f", 32))
			seedCard(t, ctx, client, id, "dealt", token)
			key := card.CardKey(id.Sprint, id.Label)
			if err := client.HSet(ctx, key, map[string]string{
				"kind": typedrec.KindFix, "repo": "mas-bandwidth/nova-tools", "base": "dev", "base_sha": base,
				"test": ". TestPass", "paths": "base.txt", "origin": "mas-bandwidth/nova-tools#3804",
			}).Err(); err != nil {
				t.Fatal(err)
			}
			gate := filepath.Join(t.TempDir(), "gate")
			if err := os.WriteFile(gate, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv(fakeHarnessEnv, tc.mode)
			t.Setenv(fakeGateEnv, gate)
			t.Setenv(fakeOriginEnv, origin)
			t.Setenv(fakeSlotEnv, filepath.Join(t.TempDir(), "slot"))
			t.Setenv(fakeDoneAlreadyEnv, landed)
			h := newHarnessRun(t, id, self)
			rep := card.RunWrapper(ctx, h.cfg, &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token})
			hs := hashOf(t, ctx, client, id.Sprint, id.Label)
			line1 := "RESULT: " + id.Sprint + "/" + id.Label + "/1 sha=000000000000"
			want := map[string]string{
				"state": "ended", "where": "done", "result_line1": line1, "red": "not-run",
				"branch": card.WrapperBranch(id.Sprint, id.Label, 1), "paths": "base.txt",
				"model": "opencode/kimi-k3", "provider": "opencode-flash",
			}
			queued := client.ZScore(ctx, "s:"+id.Sprint+":done-already", id.Label).Err() == nil
			if tc.name == "done-already" {
				if rep.Code != card.WrapperExitEnded || rep.Outcome != "ABSTAIN" || rep.Reason != "other" {
					t.Fatalf("report %s why=%q; want ENDED ABSTAIN other", rep.Line(), rep.Why)
				}
				for k, v := range map[string]string{
					"outcome": "ABSTAIN", "reason": "other", "where_ok": "abstain",
					"result_line2": "ABSTAIN done-already " + landed, "check": "not-run",
					"commit_sha": card.NoCommit, "pushed_sha": card.NoCommit, "result_paths": "",
					"why": "ABSTAIN done-already " + landed,
				} {
					want[k] = v
				}
				if !queued {
					t.Errorf("s:%s:done-already does not hold %s", id.Sprint, id.Label)
				}
				if !zHas(t, ctx, client, card.BenchCardsKey(id.Bench, "abstain"), key) {
					t.Errorf("%s is not in the bench's abstain view", key)
				}
			} else {
				if rep.Code != card.WrapperExitEnded || rep.Outcome != "DONE" {
					t.Fatalf("report %s why=%q; want ENDED DONE", rep.Line(), rep.Why)
				}
				for k, v := range map[string]string{
					"outcome": "DONE", "reason": "done", "where_ok": "ok",
					"result_line2": "DONE", "check": "pass", "result_paths": "base.txt",
				} {
					want[k] = v
				}
				if len(hs["pushed_sha"]) != 40 || hs["commit_sha"] != hs["pushed_sha"] {
					t.Errorf("commit_sha %q pushed_sha %q, want the 40-hex commit on both", hs["commit_sha"], hs["pushed_sha"])
				}
				if !strings.Contains(hs["green"], "go test . -run ^TestPass$ -count=1: pass") {
					t.Errorf("green %q, want the wrapper's passing run", hs["green"])
				}
				if queued {
					t.Errorf("a DONE card was queued for the done-already leg")
				}
			}
			for k, v := range want {
				if got, ok := hs[k]; !ok || got != v {
					t.Errorf("record %s = %q (present %v), want %q", k, got, ok, v)
				}
			}
			if hs["wall_ms"] == "" {
				t.Errorf("record wall_ms is empty")
			}
			rpt, err := card.Fsck(ctx, client, id.Sprint, false)
			if err != nil {
				t.Fatal(err)
			}
			if rpt.Drift != 0 {
				t.Errorf("fsck drift %d: %v", rpt.Drift, rpt.Lines)
			}
			cardFields, resultFields, err := card.ShowRecord(ctx, client, id.Sprint, id.Label)
			if err != nil {
				t.Fatal(err)
			}
			shown := strings.Join(card.ShowLines(cardFields, resultFields), "\n")
			if !strings.Contains(shown, "card.result_line2 "+want["result_line2"]) {
				t.Errorf("card show lacks the record's line 2:\n%s", shown)
			}
			h.assertNoJobDir()
		})
	}
}
