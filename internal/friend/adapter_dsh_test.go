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
	key := filepath.Join(root, DSHSessionKey("/w/bob"))
	for name, at := range map[string]time.Time{"session-old": time.Unix(10, 0), "session-new": time.Unix(20, 0)} {
		require.NoError(t, os.MkdirAll(filepath.Join(key, name), 0o700))
		require.NoError(t, os.Chtimes(filepath.Join(key, name), at, at))
	}
	require.NoError(t, os.WriteFile(filepath.Join(key, "stray.txt"), nil, 0o600))
	assert.Equal(t, "--Volumes-nova-ai-zhi--", DSHSessionKey("/Volumes/nova/ai/zhi"))

	var turn strings.Builder
	fe := &fakeStdinExec{exit: 0, out: "got it\n"}
	d := &DSH{Dir: "/w/bob", Run: fe.run, Sessions: root, Out: &turn}
	exit, err := d.Deliver(context.Background(), "hello\nworld")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, fe.calls, 1)
	assert.Equal(t, []string{"/w/bob", DSHProgram, "headless", "--session-id", "session-new", "-"}, fe.calls[0], "the newest session; the text is not an argument")
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
