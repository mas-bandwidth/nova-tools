package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDSHAdoptsTheNewestSessionOfTheDirectoryWithTheTextOnStdin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	key := filepath.Join(root, DSHSessionKey(realDir))
	for name, at := range map[string]time.Time{"session-old": time.Unix(10, 0), "session-new": time.Unix(20, 0)} {
		require.NoError(t, os.MkdirAll(filepath.Join(key, name), 0o700))
		require.NoError(t, os.Chtimes(filepath.Join(key, name), at, at))
	}
	require.NoError(t, os.WriteFile(filepath.Join(key, "stray.txt"), nil, 0o600))
	assert.Equal(t, "--Volumes-nova-ai-zhi--", DSHSessionKey("/Volumes/nova/ai/zhi"))

	var turn strings.Builder
	fe := &fakeStdinExec{exit: 0, out: "got it\n"}
	d := &DSH{Dir: dir, Run: fe.run, Sessions: root, Out: &turn}
	exit, err := d.Deliver(context.Background(), "hello\nworld")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, fe.calls, 1)
	assert.Equal(t, []string{dir, DSHProgram, "headless", "--session-id", "session-new", "-"}, fe.calls[0], "the newest session; the text is not an argument")
	assert.Equal(t, "hello\nworld", fe.stdin, "the text travels on stdin")
	assert.Equal(t, "got it\n", turn.String(), "the turn's output goes to the daemon's record")

	fe = &fakeStdinExec{exit: 1}
	d = &DSH{Dir: "/w/bob", Session: "session-named", Run: fe.run, Sessions: root, Program: "dsh"}
	exit, err = d.Deliver(context.Background(), "x")
	require.NoError(t, err)
	assert.Equal(t, 1, exit, "the exit code is the harness's")
	assert.Equal(t, []string{"/w/bob", "dsh", "headless", "--session-id", "session-named", "-"}, fe.calls[0], "a named session is not looked up")

	_, err = NewestDSHSession(root, "/w/ada")
	assert.ErrorContains(t, err, "no dsh session for /w/ada")
	d = &DSH{Dir: "/w/ada", Run: fe.run, Sessions: root}
	_, err = d.Deliver(context.Background(), "x")
	assert.ErrorContains(t, err, "no dsh session for /w/ada")

	got, err := NewDeliverer("dsh", "/w/bob", "", nil, nil)
	require.NoError(t, err)
	assert.IsType(t, &DSH{}, got)
}

func TestDSHFindsTheSessionThroughADirectorySymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	alias := filepath.Join(t.TempDir(), "working")
	require.NoError(t, os.Symlink(dir, alias))
	require.NoError(t, os.MkdirAll(filepath.Join(root, DSHSessionKey(realDir), "session-existing"), 0o700))
	fe := &fakeStdinExec{}
	d := &DSH{Dir: alias, Run: fe.run, Sessions: root}
	_, err = d.Deliver(context.Background(), "same session")
	require.NoError(t, err)
	require.Len(t, fe.calls, 1)
	assert.Equal(t, []string{alias, DSHProgram, "headless", "--session-id", "session-existing", "-"}, fe.calls[0])
}

// fakeStdinExec is fakeExec that also keeps what was handed on stdin.
type fakeStdinExec struct {
	calls [][]string
	stdin string
	exit  int
	out   string
}

func (f *fakeStdinExec) run(_ context.Context, dir, name string, args []string, stdin string) (string, int, error) {
	f.calls = append(f.calls, append([]string{dir, name}, args...))
	f.stdin = stdin
	return f.out, f.exit, nil
}

// A session that runs under an agent preset is refused by dsh's one-shot
// runner, whatever the text (measured 2026-10-04 12:50 PM ET on Zhi's
// session, preset "minimal"): nothing has failed that a retry fixes and
// nothing was delivered, so the delivery is Deferred, never a failure the
// daemon gives up on. Any other nonzero exit stays the harness's exit.
func TestDSHDefersASessionUnderAPresetTheOneShotRunnerDoesNotCompose(t *testing.T) {
	t.Parallel()
	refusal := `dsh: session "session-zhi" runs under agent preset "minimal", which the one-shot runner does not compose` + "\n"
	var turn strings.Builder
	fe := &fakeStdinExec{exit: 1, out: refusal}
	d := &DSH{Dir: "/w/zhi", Session: "session-zhi", Run: fe.run, Program: "dsh", Out: &turn}
	exit, err := d.Deliver(context.Background(), "hello")
	assert.Equal(t, 0, exit)
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	assert.Contains(t, deferred.Reason, `session-zhi runs under agent preset "minimal"`)
	assert.Contains(t, deferred.Reason, "nova-bus recv", "says what the friend does")
	assert.Empty(t, turn.String(), "the daemon says the reason, once a minute; the refusal is not written every recheck")

	fe = &fakeStdinExec{exit: 1, out: "dsh: unknown session \"session-gone\"\n"}
	d = &DSH{Dir: "/w/zhi", Session: "session-gone", Run: fe.run, Program: "dsh"}
	exit, err = d.Deliver(context.Background(), "hello")
	require.NoError(t, err, "any other refusal is a failed delivery, as before")
	assert.Equal(t, 1, exit)
}

func TestDSHRouteIsDeferWithTheBusReadTheSessionRuns(t *testing.T) {
	t.Parallel()
	d := &DSH{Dir: "/w/zhi", Session: "session-zhi", Program: "dsh"}
	route, line, err := d.Route(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "defer", route, "no push into the open desktop session was found")
	assert.Contains(t, line, "nova-bus wait", "the session reads the bus itself")
}

func TestDSHDeliversIntoTheOpenDesktopSession(t *testing.T) {
	t.Parallel()
	// Verifies that delivery into an open desktop session (which runs under an agent preset
	// such as "minimal") is Deferred rather than failed, and that Route reports defer with
	// the bus wait command.
	refusal := `dsh: session "session-zhi" runs under agent preset "minimal", which the one-shot runner does not compose` + "\n"
	var turn strings.Builder
	fe := &fakeStdinExec{exit: 1, out: refusal}
	d := &DSH{Dir: "/w/zhi", Session: "session-zhi", Run: fe.run, Program: "dsh", Out: &turn}
	exit, err := d.Deliver(context.Background(), "ping")
	assert.Equal(t, 0, exit)
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	assert.Contains(t, deferred.Reason, `session-zhi runs under agent preset "minimal"`)
	assert.Contains(t, deferred.Reason, "nova-bus recv")

	route, line, err := d.Route(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "defer", route)
	assert.Contains(t, line, "nova-bus wait --as <friend>")
}
