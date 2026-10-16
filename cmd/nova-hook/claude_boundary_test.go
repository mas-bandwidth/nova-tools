package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The installed Stop hook advances the generation for the session's own turn,
// and leaves it alone for a headless claude child (a friend lane's card, read or
// harness proof) whose CLAUDE_CONFIG_DIR is the directory the hook went in:
// 2026-10-16, the daemon's proof advanced a session's generation every minute.
func TestTheClaudeBoundarySkipsAHeadlessChild(t *testing.T) {
	t.Parallel()
	state, settings := t.TempDir(), t.TempDir()
	boundary := func(env map[string]string) testkit.Result {
		r := newRig(t)
		w := r.world()
		base := w.getenv
		w.getenv = func(k string) string {
			if v, ok := env[k]; ok {
				return v
			}
			return base(k)
		}
		return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
			return run(args, stdin, stdout, stderr, w)
		}).Run("hook", "--as", "bob", "--role", "friend", "--harness", "claude", "--claude-state", state, "--claude-settings", settings, "--claude-boundary")
	}
	gen := func() string {
		b, err := os.ReadFile(filepath.Join(state, "generation"))
		if os.IsNotExist(err) {
			return ""
		}
		require.NoError(t, err)
		return string(b)
	}

	out := boundary(nil)
	require.Zero(t, out.Code, out.Stdout+out.Stderr)
	assert.Contains(t, out.Stdout, "state=boundary generation=1")
	assert.Equal(t, "1\n", gen())

	out = boundary(map[string]string{notice.ClaudeChildEnv: "1"})
	require.Zero(t, out.Code, out.Stdout+out.Stderr)
	assert.Contains(t, out.Stdout, "state=boundary-skipped")
	assert.Equal(t, "1\n", gen(), "a child's Stop leaves the session's generation alone")

	out = boundary(nil)
	require.Zero(t, out.Code, out.Stdout+out.Stderr)
	assert.Equal(t, "2\n", gen())
}

// The claude route's own challenge is the Core's notice: held pending in the
// notices directory and said as a PUSH line on the stdout its Monitor follows.
// Dogfood 2026-10-16: the client printed its challenges as bare CHALLENGE lines,
// so a session that follows only PUSH lines never saw one.
func TestTheClaudeRoutesChallengeIsANotice(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()
	state, settings := t.TempDir(), t.TempDir()
	w.claudeInstall = notice.InstallClaude
	w.files = filesFunc(func(path string, data []byte, _ bool) error { return os.WriteFile(path, data, 0o600) })
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	var out strings.Builder
	code := run([]string{"hook", "--as", "ada", "--role", "friend", "--harness", "claude", "--claude-state", state, "--claude-settings", settings}, strings.NewReader(""), &out, io.Discard, w)
	assert.Zero(t, code, out.String())
	nonce := regexp.MustCompile(`HOOK CHALLENGE nonce=(\S+)`).FindStringSubmatch(out.String())
	require.Len(t, nonce, 2, "the route issues a challenge: "+out.String())
	assert.Regexp(t, `HOOK PUSH push=claude .*nonce=`+nonce[1], out.String())
	held, err := filepath.Glob(filepath.Join(state, "notices", "notice-*.json"))
	require.NoError(t, err)
	assert.NotEmpty(t, held, "the challenge is the pending notice's")
}
