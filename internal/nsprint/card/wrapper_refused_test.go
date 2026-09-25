package card_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// The refusing fake's own switches (the wrapper strips NOVA_CARD_* from the
// harness's environment, so they carry names of their own).
const (
	fakeRefusalEnv = "WRAPPER_FAKE_REFUSAL" // refuse: the reason the fake native prints
	fakePoolEnv    = "WRAPPER_FAKE_POOL"    // refuse-identity: a pool root with no identity.tsv
)

// fakeRefusal is the bench harness with a native that refuses: native's
// stderr is kept at out/native.err, as the fleet's harness keeps it, and the
// harness exits with native's 2. refuse prints `NATIVE REFUSED: <reason>`;
// refuse-identity runs the real pool identity check against a pool root with
// no identity.tsv and prints its refusal exactly as native does.
func fakeRefusal(mode string) int {
	reason := os.Getenv(fakeRefusalEnv)
	if mode == "refuse-identity" {
		_, err := swarm.LoadPoolIdentity(os.Getenv(fakePoolEnv))
		if err == nil {
			fmt.Println("fake harness: the pool has an identity; nothing to refuse")
			return 9
		}
		reason = err.Error()
	}
	out := os.Getenv("NOVA_CARD_OUT")
	line := "NATIVE REFUSED: " + oneline.Escape(reason) + "\n"
	if err := os.WriteFile(filepath.Join(out, "native.err"), []byte(line), 0o644); err != nil {
		fmt.Println("fake harness:", err)
		return 9
	}
	fmt.Println("2026-09-24T00:00:00Z END native rc=2 wall_s=0")
	return 2
}

// TestWrapperEndsANativeRefusalFailedRefused is the DONE-WHEN control of
// #3194 and #3193: a harness whose native refuses the card (exit 2 with a
// NATIVE REFUSED line) ends the card FAILED reason=refused, never a bare
// crash, with that line as the card's why on the record in Redis, in the
// report, in wrapper.line and in the w_why fact on the result hash (#3693's
// record path); and the no-identity.tsv refusal carries the
// remedy, `make -C fleet converge`, onto the card record. The negative
// control -- a non-zero exit with no refusal line stays FAILED crash -- is
// TestWrapperOwnsOneCardEndToEnd's fail case.
func TestWrapperEndsANativeRefusalFailedRefused(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		mode string
		want []string // the why must carry each
	}{
		{"refuse", []string{"NATIVE REFUSED: ", "no budget word given"}},
		{"refuse-identity", []string{"NATIVE REFUSED: ", "identity.tsv", "make -C fleet converge"}},
	}
	for i, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			ctx := context.Background()
			st, client := newSprint(t)
			id := card.Identity{Sprint: "control-refused", Label: "card-" + tc.mode, BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
			token := attemptToken(1, fmt.Sprintf("%032x", i+17))
			seedCard(t, ctx, client, id, "dealt", token)
			t.Setenv(fakeHarnessEnv, tc.mode)
			t.Setenv(fakeRefusalEnv, "no budget word given")
			t.Setenv(fakePoolEnv, t.TempDir()) // a pool root with no identity.tsv

			h := newHarnessRun(t, id, self)
			// The Redis ledger itself (a ResultRecorder), so the record path
			// of #3693 runs: the refusing harness exits on its own.
			rep := card.RunWrapper(ctx, h.cfg, &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token})

			if rep.Code != card.WrapperExitEnded || rep.Outcome != "FAILED" || rep.Reason != "refused" || rep.Exit != 2 {
				t.Fatalf("report %s why=%q; want FAILED refused exit=2 code=0", rep.Line(), rep.Why)
			}
			if !strings.HasPrefix(rep.Why, card.NativeRefusedMark) {
				t.Fatalf("report why %q is not the refusal line", rep.Why)
			}
			for _, w := range tc.want {
				if !strings.Contains(rep.Why, w) {
					t.Fatalf("report why %q lacks %q", rep.Why, w)
				}
			}
			hash := hashOf(t, ctx, client, id.Sprint, id.Label)
			if hash["state"] != "ended" || hash["outcome"] != "FAILED" || hash["reason"] != "refused" || hash["exit"] != "2" {
				t.Fatalf("card hash %v, want ended FAILED refused exit 2", hash)
			}
			// #3695: the end is the card model's one move, to done/fail.
			if hash["where"] != "done" || hash["where_ok"] != "fail" {
				t.Fatalf("card where=%q where_ok=%q, want done fail (the refused end is a fail)", hash["where"], hash["where_ok"])
			}
			if hash["why"] != rep.Why || hash["why_at"] != hash["ended_at"] {
				t.Fatalf("card why %q at %q, want the refusal line %q at the end %q", hash["why"], hash["why_at"], rep.Why, hash["ended_at"])
			}
			// #3693's record path: the wrapper's w_* facts on the attempt's
			// result hash carry the same reason and the same line.
			facts, err := client.HGetAll(ctx, card.CardKey(id.Sprint, id.Label)+":result:a1").Result()
			if err != nil || facts["w_outcome"] != "FAILED" || facts["w_reason"] != "refused" || facts["w_exit"] != "2" || facts["w_why"] != rep.Why {
				t.Fatalf("result facts %v err %v, want w_outcome FAILED w_reason refused w_exit 2 w_why the refusal line %q", facts, err, rep.Why)
			}
			log := logBy(t, ctx, client, id.Sprint)
			if n := len(log["ended"]); n != 1 || log["ended"][0]["reason"] != "refused" {
				t.Fatalf("end records %v, want one with reason refused", log["ended"])
			}
			rec, err := card.ReadEndRecord(h.results)
			if err != nil || rec.Outcome != "FAILED" || rec.Reason != "refused" || rec.ExitCode != 2 {
				t.Fatalf("end record %+v err %v", rec, err)
			}
			line, err := os.ReadFile(filepath.Join(h.results, "wrapper.line"))
			if err != nil || !strings.Contains(string(line), "reason=refused") || !strings.Contains(string(line), card.NativeRefusedMark) {
				t.Fatalf("wrapper.line %q err %v, want reason=refused and the refusal line", line, err)
			}
			if _, err := os.Stat(filepath.Join(h.results, "native.err")); err != nil {
				t.Fatalf("the refusal's evidence file was not copied to the results: %v", err)
			}
			h.assertNoJobDir()
		})
	}
}

// TestNativeRefusalReadsTheLineFromTheMark holds NativeRefusal to its
// sources: a stamped log line is read from the mark on, a job with only other
// output holds no refusal, and a later source is read when the first is silent.
func TestNativeRefusalReadsTheLineFromTheMark(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	if err := os.MkdirAll(filepath.Join(job, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(job, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("harness.log", "START\nEND native rc=1\n")
	write("out/native.out", "NATIVE INCOMPLETE nothing\n")
	if line, ok := card.NativeRefusal(job); ok {
		t.Fatalf("no refusal was printed, got %q", line)
	}
	write("out/harness.log", "2026-09-24T00:00:00Z NATIVE REFUSED: pool /p has no identity row  \n")
	if line, ok := card.NativeRefusal(job); !ok || line != "NATIVE REFUSED: pool /p has no identity row" {
		t.Fatalf("got %q %t, want the line from the mark, trimmed", line, ok)
	}
}
