//go:build functional

package card_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// memLedger is a copy's ledger in memory: the card as dealt, every move 0,
// the end kept for the test. It never harvests, as CopyLedger.End would not
// from a FAILED end.
type memLedger struct {
	card card.WrapperCard
	mu   sync.Mutex
	end  *card.WrapperEnd
}

func (m *memLedger) Card(context.Context) (card.WrapperCard, error) { return m.card, nil }
func (m *memLedger) Claim(context.Context, string) (int, error)     { return 0, nil }
func (m *memLedger) Launched(context.Context, string, string, time.Duration) (int, error) {
	return 0, nil
}
func (m *memLedger) Beat(context.Context) (int, error) { return 0, nil }
func (m *memLedger) End(_ context.Context, end card.WrapperEnd) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.end = &end
	return 0, nil
}

// TestCopyWrapperRefusesThePROnARed is nova-tools#4313 through the wrapper:
// a work copy whose harness said DONE and committed is held to the spec gate
// before its end; on a red the end is FAILED with the gate's reason (so the
// copy's ledger opens no PR), RESULT.md in the results carries the red names
// under ## Gates and wrapper.line names the gate; on a green the end is DONE
// with the gate passed. A sprint card (no copy: nova-card <S>/<label>/<n>,
// harvest #2932) is held to the same gate (the fix round's item 3 on
// #4401): its red is a FAILED end, which ns_harvest_due never offers.
func TestCopyWrapperRefusesThePROnARed(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	red := gateAnswer{exit: 1, out: "RED package=./x test=TestZ\nnova-ci local: packages=1 seconds=0.4 red=1 make-exit=1\n"}
	green := gateAnswer{exit: 0, out: "nova-ci local: packages=1 seconds=0.4 red=0 make-exit=0\n"}
	for _, tc := range []struct {
		name    string
		ci      gateAnswer
		outcome string
		reason  string
		copy    string // "" is a sprint card
	}{
		{"red", red, "FAILED", card.GateCIRed, "p1~1"},
		{"green", green, "DONE", "done", "p1~1"},
		{"sprint red", red, "FAILED", card.GateSprintReason, ""},
		{"sprint green", green, "DONE", "done", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			origin := newOrigin(t)
			base := gitIn(t, filepath.Dir(origin), "--git-dir", origin, "rev-parse", "refs/heads/dev")
			id := card.Identity{Sprint: card.CopySprint, Label: card.CopyCardLabel("p1~1"), BaseSHA: base[:8], Bench: "wrap-bench", Attempt: 1}
			if tc.copy == "" {
				id.Sprint, id.Label = "s1", "c1"
			}
			gate := filepath.Join(t.TempDir(), "gate")
			h := newHarnessRun(t, id, self)
			// the fake harness's switches go on the harness's own environment
			// (cfg.HarnessEnv), never the test process's
			h.cfg.HarnessEnv = append(os.Environ(), fakeHarnessEnv+"=repo", fakeGateEnv+"="+gate, fakeOriginEnv+"="+origin)
			h.cfg.Copy = tc.copy
			fake := &gateFake{ci: tc.ci}
			h.cfg.Run = fake.run
			// the fixture's diff is work.txt: TEST: none <why> excuses the
			// class test, and CI's answer decides
			mem := &memLedger{card: card.WrapperCard{State: "dealt", Bench: id.Bench, Attempt: 1, Identity: id.String(),
				Kind: "fix", BaseSHA: base, Test: "none the fixture writes one text file"}}
			ledger := &observed{inner: mem, events: make(chan string, 64)}
			got := make(chan card.WrapperReport, 1)
			go func() { got <- card.RunWrapper(ctx, h.cfg, ledger) }()
			h.waitFor(ledger, "launched")
			h.waitFor(ledger, "beat")
			if err := os.WriteFile(gate, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			rep := h.report(got)
			if rep.Code != card.WrapperExitEnded || rep.Outcome != tc.outcome || rep.Reason != tc.reason {
				t.Fatalf("report %s; want %s %s ended", rep.Line(), tc.outcome, tc.reason)
			}
			end := mem.end
			if end == nil || end.Gate == nil || end.Outcome != tc.outcome || end.Reason != tc.reason || end.Commit != "COMMITTED" {
				t.Fatalf("end %+v, want %s %s with a commit and the gate", end, tc.outcome, tc.reason)
			}
			raw, err := os.ReadFile(filepath.Join(h.results, "RESULT.md"))
			if err != nil {
				t.Fatal(err)
			}
			line, _ := os.ReadFile(filepath.Join(h.results, "wrapper.line"))
			calls := strings.Join(fake.calls, "\n")
			if !strings.Contains(calls, "nova-ci local --base "+base) || strings.Contains(calls, "go test") {
				t.Errorf("calls:\n%s\nwant nova-ci local at the base and no class test (TEST: none)", calls)
			}
			switch strings.TrimPrefix(tc.name, "sprint ") {
			case "red":
				if !strings.Contains(end.Why, "RED package=./x test=TestZ") || strings.Join(end.Gate.Reds, "") != "RED package=./x test=TestZ" {
					t.Errorf("end why %q reds %q: want the red name", end.Why, end.Gate.Reds)
				}
				if !strings.Contains(string(raw), "\n## Gates\n") || !strings.Contains(string(raw), "\n- RED package=./x test=TestZ\n") {
					t.Errorf("RESULT.md lacks the red under ## Gates:\n%s", raw)
				}
				if !strings.Contains(string(line), "gate=ci-red") || !strings.Contains(string(line), "reason="+tc.reason) {
					t.Errorf("wrapper.line %q", line)
				}
			case "green":
				if !end.Gate.Passed() || end.Why != "" {
					t.Errorf("gate %+v why %q, want passed", end.Gate, end.Why)
				}
				if !strings.Contains(string(raw), "\n## Gates\n- TEST: none (the fixture writes one text file); no class test required\n- nova-ci local: packages=1") {
					t.Errorf("RESULT.md:\n%s", raw)
				}
				if !strings.Contains(string(line), "gate=pass") {
					t.Errorf("wrapper.line %q", line)
				}
			}
			h.assertNoJobDir()
		})
	}
}
