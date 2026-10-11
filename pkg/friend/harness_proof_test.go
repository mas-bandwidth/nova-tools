package friend

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOneShot is a fake one-shot harness: it records the argv it was run with
// and answers with out, exit and err.
type fakeOneShot struct {
	gotEnv, gotProg string
	gotArgs         []string
	out             string
	exit            int
	err             error
}

func (f *fakeOneShot) run(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
	f.gotArgs = args
	rest := args
	if len(rest) > 0 && strings.Contains(rest[0], "=") {
		f.gotEnv, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 {
		f.gotProg = rest[0]
	}
	return f.out, f.exit, f.err
}

func TestProveHarnessReadsTerminalReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		harness  string
		cfgDir   string
		out      string
		exit     int
		err      error
		wantOK   bool
		wantReas string
		wantProg string
		wantEnv  string
	}{
		{
			name:     "refused login prints api_error and is not ok",
			harness:  "claude",
			cfgDir:   "/home/nova/claude",
			out:      `{"type":"result","subtype":"error_during_execution","terminal_reason":"api_error","duration_api_ms":0}`,
			exit:     1,
			wantOK:   false,
			wantReas: "api_error",
			wantProg: "claude",
			wantEnv:  "CLAUDE_CONFIG_DIR=/home/nova/claude",
		},
		{
			name:     "an answered turn is ok",
			harness:  "claude",
			cfgDir:   "/home/nova/claude",
			out:      `{"type":"result","terminal_reason":"success","duration_api_ms":900}`,
			exit:     0,
			wantOK:   true,
			wantReas: "success",
			wantProg: "claude",
			wantEnv:  "CLAUDE_CONFIG_DIR=/home/nova/claude",
		},
		{
			name:     "a nonzero exit with no reason is not ok",
			harness:  "claude",
			cfgDir:   "/home/nova/claude",
			out:      "",
			exit:     2,
			wantOK:   false,
			wantReas: "exit 2",
			wantProg: "claude",
			wantEnv:  "CLAUDE_CONFIG_DIR=/home/nova/claude",
		},
		{
			name:     "dsh runs its one-turn call",
			harness:  "dsh",
			cfgDir:   "",
			out:      `{"terminal_reason":"success"}`,
			exit:     0,
			wantOK:   true,
			wantReas: "success",
			wantProg: "dsh",
		},
		{
			name:     "gemini runs its one-turn call",
			harness:  "gemini",
			cfgDir:   "",
			out:      `{"terminal_reason":"success"}`,
			exit:     0,
			wantOK:   true,
			wantReas: "success",
			wantProg: "gemini",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeOneShot{out: tc.out, exit: tc.exit, err: tc.err}
			p := ProveHarness(context.Background(), tc.harness, tc.cfgDir, f.run, time.Unix(0, 0))
			assert.Equal(t, tc.wantOK, p.OK)
			assert.Equal(t, tc.wantReas, p.Reason)
			assert.Equal(t, tc.wantProg, f.gotProg)
			if tc.wantEnv != "" {
				assert.Equal(t, []string{tc.wantEnv, tc.wantProg, "-p", "ok", "--output-format", "json", "--max-turns", "1"}, f.gotArgs)
			}
		})
	}
}

func TestProveHarnessRefusesUnknown(t *testing.T) {
	t.Parallel()
	f := &fakeOneShot{}
	p := ProveHarness(context.Background(), "codex", "/x", f.run, time.Unix(0, 0))
	require.Error(t, p.Err)
	assert.False(t, p.OK)
	assert.Nil(t, f.gotArgs)
}

func TestProveHarnessRunErrorIsNotOK(t *testing.T) {
	t.Parallel()
	f := &fakeOneShot{err: errors.New("claude: not found")}
	p := ProveHarness(context.Background(), "claude", "/x", f.run, time.Unix(0, 0))
	require.Error(t, p.Err)
	assert.False(t, p.OK)
	assert.Contains(t, ProofReasonLine(p, "/x"), "cannot answer")
}

func TestProofReasonLineNamesHarness(t *testing.T) {
	t.Parallel()
	line := ProofReasonLine(HarnessProof{Harness: "claude", Reason: "api_error"}, "/home/nova/claude")
	assert.Equal(t, "harness claude cannot answer: api_error (config dir /home/nova/claude)", line)
}
