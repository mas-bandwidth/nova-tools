package card_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestWrapperNoCommitCodeCardEndsFail is the quack-0925d defect (2026-09-25
// 05:57Z): two code cards whose model wrote DONE and committed nothing ended
// done/ok with w_commit NO-COMMIT and pushed_sha "-", which ns_harvest_due
// skips, so they sat in done/ok forever with no PR and no refusal. Against
// the real ns_card_end on a throwaway redis-server: a code card (fix, or a
// model card with no KIND) that says DONE with NO-COMMIT ends FAILED reason
// no-commit in done/fail, its why naming the NO-COMMIT and the model's DONE
// line kept on the result record; a script, read or report card ends as
// before (DONE done); a code card whose model said ABSTAIN or BLOCKED ends
// with the model's word (#3919: ABSTAIN other in done/abstain, BLOCKED deps
// in done/fail), never no-commit.
func TestWrapperNoCommitCodeCardEndsFail(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, kind, mode string
		fail             bool
		// the model's own end (#3919): outcome, reason, where_ok
		said []string
	}{
		{"fix-said-done", "fix", "said-done", true, nil},
		{"model-said-done", "", "said-done", true, nil},
		{"script-said-done", "script", "said-done", false, nil},
		{"report-said-done", "report", "said-done", false, nil},
		{"fix-said-abstain", "fix", "said-abstain", false, []string{"ABSTAIN", "other", "abstain"}},
		{"fix-said-blocked", "fix", "said-blocked", false, []string{"BLOCKED", "deps", "fail"}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, client := newSprint(t)
			id := card.Identity{Sprint: "control-nocommit", Label: "card-" + tc.name, BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
			token := attemptToken(1, strings.Repeat(string(rune('a'+i)), 32))
			seedCard(t, ctx, client, id, "dealt", token)
			if tc.kind != "" {
				if err := client.HSet(ctx, card.CardKey(id.Sprint, id.Label), "kind", tc.kind).Err(); err != nil {
					t.Fatal(err)
				}
			}
			// The gate is open: the harness writes its RESULT.md and exits 0.
			gate := filepath.Join(t.TempDir(), "gate")
			if err := os.WriteFile(gate, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv(fakeHarnessEnv, tc.mode)
			t.Setenv(fakeGateEnv, gate)
			h := newHarnessRun(t, id, self)
			rep := card.RunWrapper(ctx, h.cfg, &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token})

			hash := hashOf(t, ctx, client, id.Sprint, id.Label)
			rec, rerr := card.ReadEndRecord(h.results)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if hash["pushed_sha"] != card.NoCommit || rec.PushedSHA != card.NoCommit {
				t.Fatalf("pushed_sha hash=%q record=%q, want - (nothing committed)", hash["pushed_sha"], rec.PushedSHA)
			}
			_, result, err := card.ShowRecord(ctx, client, id.Sprint, id.Label)
			if err != nil {
				t.Fatal(err)
			}
			if result["w_commit"] != "NO-COMMIT" {
				t.Fatalf("result w_commit %q, want NO-COMMIT", result["w_commit"])
			}
			if tc.said != nil {
				if rep.Code != card.WrapperExitEnded || rep.Outcome != tc.said[0] || rep.Reason != tc.said[1] {
					t.Fatalf("report %s why=%q; want %s %s", rep.Line(), rep.Why, tc.said[0], tc.said[1])
				}
				if hash["state"] != "ended" || hash["outcome"] != tc.said[0] || hash["reason"] != tc.said[1] ||
					hash["where"] != "done" || hash["where_ok"] != tc.said[2] || hash["why"] != result["w_line2"] {
					t.Fatalf("card hash %v, want ended %s %s in done/%s with why line 2", hash, tc.said[0], tc.said[1], tc.said[2])
				}
				return
			}
			if !tc.fail {
				if rep.Code != card.WrapperExitEnded || rep.Outcome != "DONE" || rep.Reason != "done" {
					t.Fatalf("report %s why=%q; want DONE done as before", rep.Line(), rep.Why)
				}
				if hash["state"] != "ended" || hash["outcome"] != "DONE" || hash["reason"] != "done" || hash["where"] != "done" {
					t.Fatalf("card hash %v, want ended DONE done in done as before", hash)
				}
				return
			}
			if rep.Code != card.WrapperExitEnded || rep.Outcome != "FAILED" || rep.Reason != "no-commit" {
				t.Fatalf("report %s why=%q; want ENDED FAILED no-commit code=0", rep.Line(), rep.Why)
			}
			if hash["state"] != "ended" || hash["outcome"] != "FAILED" || hash["reason"] != "no-commit" ||
				hash["where"] != "done" || hash["where_ok"] != "fail" || hash["why"] != card.NoCommitWhy {
				t.Fatalf("card hash %v, want ended FAILED no-commit in done/fail with why %q", hash, card.NoCommitWhy)
			}
			if rec.Outcome != "FAILED" || rec.Reason != "no-commit" {
				t.Fatalf("end record %+v, want FAILED no-commit", rec)
			}
			if result["w_outcome"] != "FAILED" || result["w_reason"] != "no-commit" || result["w_why"] != card.NoCommitWhy {
				t.Fatalf("result record %v, want w_outcome FAILED w_reason no-commit w_why %q", result, card.NoCommitWhy)
			}
			// The model's DONE line is kept on the result record.
			if !strings.Contains(result["raw_bytes"]+"\n"+result["w_line2"], "DONE") {
				t.Fatalf("result record lost the model's DONE line: %v", result)
			}
			if tc.kind == "fix" && result["w_line2"] != "DONE" {
				t.Fatalf("result w_line2 %q, want the model's DONE", result["w_line2"])
			}
			line, _ := os.ReadFile(filepath.Join(h.results, "wrapper.line"))
			if !strings.Contains(string(line), "outcome=FAILED reason=no-commit") || !strings.Contains(string(line), `commit="NO-COMMIT"`) {
				t.Fatalf("wrapper.line %q, want outcome=FAILED reason=no-commit commit NO-COMMIT", line)
			}
			log := logBy(t, ctx, client, id.Sprint)
			if n := len(log["ended"]); n != 1 || log["ended"][0]["reason"] != "no-commit" {
				t.Fatalf("end records %v, want one with reason no-commit", log["ended"])
			}
			h.assertNoJobDir()
		})
	}
}

// TestNoCommitEnd is the rule on its own: only a code card, harness DONE,
// NO-COMMIT and a model line 2 of DONE turns into FAILED no-commit.
func TestNoCommitEnd(t *testing.T) {
	out := t.TempDir()
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	done := card.WrapperEnd{Outcome: "DONE", Reason: "done", Commit: "NO-COMMIT", PushedSHA: card.NoCommit}
	write("RESULT: x\nDONE\n")
	if got, ok := card.NoCommitEnd("fix", out, done); !ok || got.Outcome != "FAILED" || got.Reason != "no-commit" || got.Why != card.NoCommitWhy {
		t.Fatalf("fix DONE NO-COMMIT = %+v %v, want FAILED no-commit", got, ok)
	}
	for _, kind := range []string{"script", "read", "report"} {
		if _, ok := card.NoCommitEnd(kind, out, done); ok {
			t.Fatalf("%s card with no commit failed; it legitimately commits nothing", kind)
		}
	}
	for name, end := range map[string]card.WrapperEnd{
		"committed": {Outcome: "DONE", Reason: "done", Commit: "COMMITTED", PushedSHA: strings.Repeat("a", 40)},
		"oversize":  {Outcome: "DONE", Reason: "done", Commit: "OVERSIZE big.bin", PushedSHA: card.NoCommit},
		"crashed":   {Outcome: "FAILED", Reason: "crash", Commit: "NO-COMMIT", PushedSHA: card.NoCommit},
	} {
		if _, ok := card.NoCommitEnd("fix", out, end); ok {
			t.Fatalf("%s end turned into no-commit", name)
		}
	}
	for _, body := range []string{"RESULT: x\nABSTAIN scope\n", "RESULT: x\nBLOCKED deps\n", "RESULT: x\n", "RESULT: x\nDONE but\n"} {
		write(body)
		if _, ok := card.NoCommitEnd("fix", out, done); ok {
			t.Fatalf("line 2 of %q turned into no-commit; only a model DONE does", body)
		}
	}
	if err := os.Remove(filepath.Join(out, "RESULT.md")); err != nil {
		t.Fatal(err)
	}
	if _, ok := card.NoCommitEnd("fix", out, done); ok {
		t.Fatal("no RESULT.md turned into no-commit")
	}
}
