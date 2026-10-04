package update

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// busFake is the Redis bus as a child-runner fake: it records every child
// argv and answers every nova-bus send with the same confirmation line. Calls
// for any other binary run for real.
type busFake struct {
	mu    sync.Mutex
	calls [][]string
	line  string
}

func (f *busFake) process(_ context.Context, args []string, _ io.Reader, _ int) ProcessResult {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()
	return ProcessResult{Stdout: f.line}
}

func (f *busFake) sends() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, argv := range f.calls {
		if len(argv) > 0 && argv[0] == "nova-bus" {
			out = append(out, argv)
		}
	}
	return out
}

func (f *busFake) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// requireRedisSend asserts the one-send shape: exactly one bus child,
// `nova-bus send --as` carrying --to, --subject and --stdin, and no prepare.
func requireRedisSend(t *testing.T, sent [][]string) {
	t.Helper()
	require.Len(t, sent, 1, "want exactly one bus child, got %q", sent)
	argv := sent[0]
	require.GreaterOrEqual(t, len(argv), 3, "bus child has no verb: %q", argv)
	assert.Equal(t, []string{"nova-bus", "send", "--as"}, argv[:3], "the one bus child is not a Redis send: %q", argv)
	joined := strings.Join(argv, " ")
	for _, want := range []string{"--to", "--subject", "--stdin"} {
		assert.Contains(t, joined, want, "the send carries no %s: %q", want, argv)
	}
	assert.NotContains(t, joined, "prepare", "the retired prepare step still runs: %q", argv)
}

// The Redis bus carries the report and the adoption receipt in one send: one
// `nova-bus send --as <me> --to <recipients> --subject <line> --stdin` child,
// the store from NOVA_BUS_REDIS as nova-bus reads it, confirmed by the bus's
// own `SEND OK id=<id>` line, whose id is what the receipt records. No
// prepare step runs.
func TestReportSendUsesTheRedisBus(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2026, 10, 4, 17, 0, 0, 0, time.UTC)
	fake := &busFake{line: "SEND OK id=01ABC to=x cc=- at=2026-10-04T17:00:00Z\n"}
	env := Environment{
		Now: func() time.Time { return fixed },
		Process: func(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
			if len(args) > 0 && args[0] == "nova-bus" {
				return fake.process(ctx, args, input, cap)
			}
			return process(ctx, args, input, cap)
		},
	}

	p := manifest(t, row("x", "tool", "v1.2.3", "npm:unused", "none"))
	code, out, errs := run(t, env, "report", "--file", p, "--send",
		"--snapshot", filepath.Join(t.TempDir(), "s.json"),
		"--as", "coordinator", "--to", "duty",
		"--bus", t.TempDir(), "--remote", "origin", "--branch", "main")
	require.Zero(t, code, "report --send failed:\n%s\n%s", out, errs)
	requireRedisSend(t, fake.sends())
	assert.Contains(t, out, "REPORT SENT", "no SENT receipt:\n%s\n%s", out, errs)
	assert.Contains(t, out, "id\\x3d01ABC", "the receipt does not name the bus's id:\n%s\n%s", out, errs)

	fake.reset()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	line, err := postAdoptReceipt(ctx,
		options{as: "coordinator", to: "duty"}, []byte("adoption note\n"), fixed, env)
	require.NoError(t, err)
	requireRedisSend(t, fake.sends())
	assert.Contains(t, line, "id=01ABC", "the adoption receipt does not name the bus's id: %q", line)
}
