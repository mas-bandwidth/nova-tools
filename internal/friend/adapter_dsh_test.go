package friend

import (
	"context"
	"fmt"
	"io"
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
// session, preset "minimal"), and a headless profile with no provider key
// stops at MISSING_CREDENTIAL: either way the session cannot take a turn at
// all. Read from the output whatever the exit code (the finding of
// 2026-10-05: from 2026-10-04 her turns printed the preset refusal and exited
// 0, and four hours of messages counted as delivered), the delivery is a
// SessionRefused naming the session and the reason, and the output is not
// written to the record. Any other nonzero exit stays the harness's exit.
func TestDSHRefusesASessionThatCannotTakeATurnWhateverTheExit(t *testing.T) {
	t.Parallel()
	preset := `dsh: session "session-zhi" runs under agent preset "minimal", which the one-shot runner does not compose` + "\n"
	credential := "turn 1: MISSING_CREDENTIAL: no provider key for the headless profile (DEEPSEEK_API_KEY=sk-fake-not-a-key)\n"
	for _, c := range []struct {
		name, out string
		exit      int
		want      SessionRefused
		said      string
	}{
		{"preset at exit 0", preset, 0, SessionRefused{Harness: "dsh", Session: "session-zhi", Reason: "agent preset minimal"}, "dsh session session-zhi: agent preset minimal"},
		{"preset at exit 1", preset, 1, SessionRefused{Harness: "dsh", Session: "session-zhi", Reason: "agent preset minimal"}, "dsh session session-zhi: agent preset minimal"},
		{"missing credential at exit 0", credential, 0, SessionRefused{Harness: "dsh", Session: "session-zhi", Reason: "missing credential", HarnessWide: true}, "dsh: missing credential"},
		{"missing credential at exit 1", credential, 1, SessionRefused{Harness: "dsh", Session: "session-zhi", Reason: "missing credential", HarnessWide: true}, "dsh: missing credential"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var turn strings.Builder
			fe := &fakeStdinExec{exit: c.exit, out: c.out}
			d := &DSH{Dir: "/w/zhi", Session: "session-zhi", Run: fe.run, Program: "dsh", Out: &turn}
			exit, err := d.Deliver(context.Background(), "hello")
			assert.Equal(t, c.exit, exit)
			var refused SessionRefused
			require.ErrorAs(t, err, &refused)
			assert.Equal(t, c.want, refused)
			assert.Equal(t, c.said, refused.Down())
			assert.Equal(t, c.said, err.Error())
			assert.NotContains(t, err.Error(), "sk-fake", "no credential value is ever said")
			assert.Empty(t, turn.String(), "the refusal is not written to the record: the daemon says the reason once")
		})
	}

	fe := &fakeStdinExec{exit: 1, out: "dsh: unknown session \"session-gone\"\n"}
	d := &DSH{Dir: "/w/zhi", Session: "session-gone", Run: fe.run, Program: "dsh"}
	exit, err := d.Deliver(context.Background(), "hello")
	require.NoError(t, err, "any other refusal is a failed delivery, as before")
	assert.Equal(t, 1, exit)
}

// fakeDSHEnv names, in a fake dsh's environment, the file that says what the
// fake does with its turn: "preset" prints the one-shot runner's refusal,
// "credential" stops at MISSING_CREDENTIAL, anything else takes the turn;
// each exits 0, as the real runner did on 2026-10-04.
const fakeDSHEnv = "NOVA_FRIEND_FAKE_DSH"

// This test binary is the fake dsh when started as `<bin> headless
// --session-id <id> -` with fakeDSHEnv set; it never runs the tests then.
func init() {
	mode := os.Getenv(fakeDSHEnv)
	if mode == "" || len(os.Args) != 5 || os.Args[1] != "headless" {
		return
	}
	os.Exit(fakeDSH(mode, os.Args[3]))
}

func fakeDSH(modeFile, id string) int {
	mode, _ := os.ReadFile(modeFile) // ignored: no mode file takes the turn
	text, _ := io.ReadAll(os.Stdin)  // ignored: the turn's text is only counted
	switch strings.TrimSpace(string(mode)) {
	case "preset":
		fmt.Printf("dsh: session %q runs under agent preset \"minimal\", which the one-shot runner does not compose\n", id)
	case "credential":
		fmt.Printf("turn 1: MISSING_CREDENTIAL: no provider key for the headless profile (DEEPSEEK_API_KEY=sk-fake-not-a-key)\n")
	default:
		fmt.Printf("turn taken in %s: %d bytes\n", id, len(text))
	}
	return 0
}

func TestDSHRouteIsDeferWithTheBusReadTheSessionRuns(t *testing.T) {
	t.Parallel()
	d := &DSH{Dir: "/w/zhi", Session: "session-zhi", Program: "dsh"}
	route, line, err := d.Route(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "defer", route, "no push into the open desktop session was found")
	assert.Contains(t, line, "nova-bus wait", "the session reads the bus itself")
}
