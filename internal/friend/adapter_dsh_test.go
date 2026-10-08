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

// fakeDSHChild is set in the environment of this test binary started again as
// dsh (`<bin> headless --session-id <id> -`): it is then the fake dsh, and
// never the tests. Its value is what the turn answers: "preset", the one-shot
// runner's refusal of a session under preset "minimal"; "credential",
// MISSING_CREDENTIAL with a key's value in it; both exit 0, as dsh did
// (measured 2026-10-06). While the file fakeDSHFixed names exists, the turn
// is taken: it prints and exits 0.
const (
	fakeDSHChild = "NOVA_FRIEND_FAKE_DSH"
	fakeDSHFixed = "NOVA_FRIEND_FAKE_DSH_FIXED"
	fakeDSHKey   = "sk-fake-credential-0123456789" // printed by the fake; never on the record
)

func init() {
	if mode := os.Getenv(fakeDSHChild); mode != "" && len(os.Args) > 1 && os.Args[1] == "headless" {
		os.Exit(fakeDSH(mode))
	}
}

func fakeDSH(mode string) int {
	text, _ := io.ReadAll(os.Stdin) // ignored: the fake answers whatever the text
	session := os.Args[len(os.Args)-2]
	if _, err := os.Stat(os.Getenv(fakeDSHFixed)); err == nil {
		fmt.Printf("turn taken in %s: %d bytes\n", session, len(text))
		return 0
	}
	switch mode {
	case "preset":
		fmt.Printf("dsh: session %q runs under agent preset \"minimal\", which the one-shot runner does not compose\n", session)
	case "credential":
		fmt.Printf("dsh: provider deepseek: MISSING_CREDENTIAL (DEEPSEEK_API_KEY=%s rejected)\n", fakeDSHKey)
	}
	return 0
}

// fakeDSHExec runs this test binary as the fake dsh in mode; fixed is the
// file whose existence makes its turns succeed.
func fakeDSHExec(mode, fixed string) Exec {
	return envExec(fakeDSHChild+"="+mode, fakeDSHFixed+"="+fixed)
}

// A turn whose output carries the preset refusal or MISSING_CREDENTIAL is a
// failed delivery whatever its exit code: a SessionRefused naming the
// session and the reason, the output never on the record (a key's value is
// in it), and still the Deferred it is for the push proof's remedy. A turn
// the session takes is delivered.
func TestDSHReadsARefusalFromTheOutputWhateverTheExit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ mode, reason, remedy string }{
		{"preset", "dsh session session-zhi: agent preset minimal", "with no agent preset"},
		{"credential", "dsh: missing credential", ""},
	} {
		t.Run(c.mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			fixed := filepath.Join(t.TempDir(), "fixed")
			var turn strings.Builder
			d := &DSH{Dir: dir, Session: "session-zhi", Run: fakeDSHExec(c.mode, fixed), Program: os.Args[0], Out: &turn}
			exit, err := d.Deliver(context.Background(), "hello")
			assert.Equal(t, 0, exit)
			var refused SessionRefused
			require.ErrorAs(t, err, &refused, "exit 0 and the refusal in the output: not delivered")
			assert.Equal(t, "session-zhi", refused.Session)
			assert.Equal(t, c.reason, refused.Reason)
			var deferred Deferred
			require.ErrorAs(t, err, &deferred)
			if c.remedy != "" {
				assert.Contains(t, deferred.Remedy, c.remedy)
			}
			assert.NotContains(t, err.Error(), fakeDSHKey)
			assert.Empty(t, turn.String(), "the refused turn's output is not kept")

			require.NoError(t, os.WriteFile(fixed, nil, 0o600))
			exit, err = d.Deliver(context.Background(), "hello")
			require.NoError(t, err)
			assert.Equal(t, 0, exit)
			assert.Equal(t, "turn taken in session-zhi: 5 bytes\n", turn.String())
		})
	}
}

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
// session, preset "minimal"): nothing was delivered, so the delivery is a
// SessionRefused (a Deferred for the message), never a failure the daemon
// gives up on. Any other nonzero exit stays the harness's exit.
func TestDSHDefersASessionUnderAPresetTheOneShotRunnerDoesNotCompose(t *testing.T) {
	t.Parallel()
	refusal := `dsh: session "session-zhi" runs under agent preset "minimal", which the one-shot runner does not compose` + "\n"
	var turn strings.Builder
	fe := &fakeStdinExec{exit: 1, out: refusal}
	d := &DSH{Dir: "/w/zhi", Session: "session-zhi", Run: fe.run, Program: "dsh", Out: &turn}
	exit, err := d.Deliver(context.Background(), "hello")
	assert.Equal(t, 0, exit)
	var refused SessionRefused
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "dsh session session-zhi: agent preset minimal", refused.Reason)
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

func TestDSHRouteIsPushByAHeadlessTurn(t *testing.T) {
	t.Parallel()
	d := &DSH{Dir: "/w/zhi", Session: "session-zhi", Program: "dsh"}
	route, line, err := d.Route(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "push", route, "every delivery is a headless turn into her session")
	assert.Equal(t, "dsh headless --session-id session-zhi -", line)
}

func TestDSHDeliversIntoTheOpenDesktopSession(t *testing.T) {
	t.Parallel()
	// Verifies that delivery into an open desktop session (which runs under an agent preset
	// such as "minimal") is Deferred rather than failed; Route says push, the headless turn.
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
	assert.Equal(t, "push", route)
	assert.Contains(t, line, "dsh headless --session-id session-zhi")
}
