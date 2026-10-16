package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// terminalReason reads the terminal_reason of a claude --output-format json
// answer: the last such line, "" when none.
func TestTerminalReasonReadsTheLastJsonLine(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", terminalReason(""))
	assert.Equal(t, "", terminalReason(`{"type":"result","subtype":"success"}`))
	assert.Equal(t, "api_error", terminalReason("{\"terminal_reason\":\"api_error\",\"duration_api_ms\":0}"))
	assert.Equal(t, "end_turn", terminalReason("garbage\n{\"terminal_reason\":\"api_error\"}\n{\"terminal_reason\":\"end_turn\"}"))
}

// ClaudeProofWhy is the harness proof: claude's cheapest no-tool turn, a run
// that answers nothing or names a terminal reason that is no normal end is a
// harness that cannot answer (its config directory never signed in), and the
// reason names it (docs/SPEC-FRIEND.md, "A lane proves its harness").
func TestClaudeProofWhy(t *testing.T) {
	t.Parallel()
	run := func(out string, exit int) Exec {
		return func(_ context.Context, dir, name string, args []string, stdin string) (string, int, error) {
			assert.Equal(t, "env", name)
			assert.Equal(t, []string{"CLAUDE_CONFIG_DIR=/cfg", "NOVA_HOOK_CLAUDE_CHILD=1"}, args[:2], "the proof is a headless child: no session's Stop hook counts it")
			assert.Equal(t, []string{"claude", "-p", "ok", "--output-format", "json", "--max-turns", "1"}, args[2:])
			return out, exit, nil
		}
	}
	assert.Empty(t, ClaudeProofWhy(context.Background(), run(`{"terminal_reason":"end_turn"}`, 0), "/d", "/cfg", "claude"))
	assert.Empty(t, ClaudeProofWhy(context.Background(), run(``, 0), "/d", "/cfg", "claude"))
	// claude 2.1.289's answer to the proof on a signed-in account (2026-10-16)
	assert.Empty(t, ClaudeProofWhy(context.Background(), run(`{"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","terminal_reason":"completed"}`, 0), "/d", "/cfg", "claude"), "completed is a normal end")
	why := ClaudeProofWhy(context.Background(), run(`{"terminal_reason":"api_error","duration_api_ms":0}`, 1), "/d", "/cfg", "claude")
	assert.Contains(t, why, "harness claude cannot answer: api_error (config dir /cfg)")
	why = ClaudeProofWhy(context.Background(), run(``, 1), "/d", "/cfg", "claude")
	require.NotEmpty(t, why)
	assert.True(t, strings.Contains(why, "cannot answer"), why)
}

// proofCardRunner is a per-card claude lane's harness for the proof tests: each
// card is one turn; with fail set the turn ends instantly (exit 1, no report),
// else it writes the card's RESULT.md and ends at 0.
type proofCardRunner struct {
	mu   sync.Mutex
	runs []string
	fail bool
}

func (c *proofCardRunner) Deliver(context.Context, string) (int, error) { return 0, nil }

func (c *proofCardRunner) Refusal() string { return "" }

func (c *proofCardRunner) RunCard(_ context.Context, card Card) (LaneTurn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runs = append(c.runs, card.ID)
	if c.fail {
		return LaneTurn{Exit: 1}, NoReport{Run: "claude -p", Exit: 1, Outbox: card.Outbox, Lacks: []string{"REPORT.md", "RESULT.md"}}
	}
	if err := os.MkdirAll(card.Outbox, 0o755); err != nil {
		return LaneTurn{}, err
	}
	if err := os.WriteFile(filepath.Join(card.Outbox, "RESULT.md"), []byte("RESULT: "+card.ID+"\n"), 0o644); err != nil {
		return LaneTurn{}, err
	}
	return LaneTurn{Exit: 0}, nil
}

func (c *proofCardRunner) got() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.runs...)
}

// cardProofRig is the daemon rig over a per-card claude lane in a fixture
// directory, one-shot at width lanes, Prove set to the given function.
func cardProofRig(t *testing.T, h *proofCardRunner, width int, dir string) *rig {
	t.Helper()
	r := newRig(t)
	state := &LaneState{}
	r.d.Deliver, r.passive, r.d.Dir = h, true, dir
	r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
	r.d.Row = func() (string, int) { return ModeOneShot, width }
	r.d.Coordinator = "ada"
	r.d.LoadLanes = func() (LaneState, error) { return *state, nil }
	r.d.SaveLanes = func(s LaneState) error { *state = s; return nil }
	return r
}

// A daemon whose harness proof fails takes no card: the lane never runs a card,
// the daemon beats down with the reason, tells the coordinator once, and
// re-proves every ServedEvery until it passes (docs/SPEC-FRIEND.md, "A lane
// proves its harness").
func TestADaemonWhoseHarnessProofFailsTakesNoCard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &proofCardRunner{fail: true}
		r := cardProofRig(t, h, 1, dir)
		proves := 0
		r.d.Prove = func(context.Context) string {
			proves++
			return "harness claude cannot answer: api_error (config dir /cfg)"
		}
		var downs []string
		r.d.RefusalDown = func(_ time.Time, reason string) { downs = append(downs, reason) }
		r.run(t, int(2*ServedEvery/BeatEvery)+3)
		assert.Empty(t, h.got(), "no card is taken while the harness cannot answer")
		assert.GreaterOrEqual(t, proves, 2, "the proof re-runs every ServedEvery until it passes")
		require.NotEmpty(t, downs, "the daemon beats down with the reason")
		assert.Contains(t, downs[0], "harness claude cannot answer: api_error (config dir /cfg)")
		var told int
		for _, g := range r.adaGot(t) {
			if strings.Contains(g, "harness claude cannot answer") {
				told++
			}
		}
		assert.Equal(t, 1, told, "the coordinator is told once")
	})
}

// A proof that passes takes cards as today: the lane runs its card and finishes it
// (docs/SPEC-FRIEND.md, "A lane proves its harness").
func TestADaemonWhoseHarnessProofPassesTakesCards(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &proofCardRunner{}
		r := cardProofRig(t, h, 1, dir)
		r.d.Prove = func(context.Context) string { return "" }
		r.run(t, 30)
		assert.Equal(t, []string{"c1"}, h.got(), "the lane takes and runs the card when the proof passes")
		assert.Equal(t, "ok", r.last().Proof, "the proof's last result is ok on the status")
	})
}

// Two card runs that end with no report within InstantFail stop the lane's
// taking and re-prove the harness, instead of finishing a third card FAILED
// (docs/SPEC-FRIEND.md, "A lane proves its harness").
func TestTwoInstantFailuresReProveTheHarnessInsteadOfAThirdCard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}, {"c3", "queued"}}, []string{"c1", "c2", "c3"}, nil)
		h := &proofCardRunner{fail: true}
		r := cardProofRig(t, h, 1, dir)
		proves := 0
		r.d.Prove = func(context.Context) string {
			proves++
			if proves == 1 {
				return "" // the harness proved at start
			}
			return "harness claude cannot answer: api_error (config dir /cfg)"
		}
		var downs []string
		r.d.RefusalDown = func(_ time.Time, reason string) { downs = append(downs, reason) }
		r.run(t, 30)
		assert.Equal(t, []string{"c1", "c1"}, h.got(), "two instant runs of c1, then the harness is re-proved, not a third card")
		assert.GreaterOrEqual(t, proves, 2, "the proof re-runs after the second instant failure")
		require.NotEmpty(t, downs, "the daemon beats down once the re-proof fails")
	})
}
