package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rebind is the supported way to change the session the daemon delivers
// into (docs/SPEC-FRIEND.md, "A gone session target"): a target that is gone
// is refused, the new one rewrites the plist's --session, retires the old
// id in the state's target.json, writes the push proof down so a fresh
// session check is owed, and reloads the agent; install and run refuse the
// retired id afterwards, so an old command line cannot resurrect it.
func TestRebindRetiresTheOldTargetAndOwesAFreshProof(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	w.exec = func(ctx context.Context, dir, prog string, args []string, stdin string) (string, int, error) {
		if args[0] == "session" {
			return `[{"id":"ses_1","directory":"/w/bob","updated":1},{"id":"ses_2","directory":"/w/bob","updated":2},{"id":"ses_3","directory":"/w/amy","updated":3}]`, 0, nil
		}
		return r.opencode(ctx, dir, prog, args, stdin)
	}
	cli := testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
	plist := filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist")
	state := friend.DefaultStateDir(r.home, "bob")

	cli.Do(t, "rebind", "--as", "bob", "--session", "ses_2").Exit(2).Err("no agent installed for bob")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--session", "ses_1").Exit(0).Out("NOTE target: ses_1 recorded in " + filepath.Join(state, friend.TargetFile))
	before, err := os.ReadFile(plist)
	require.NoError(t, err)
	r.launchctl = nil

	cli.Do(t, "rebind", "--as", "bob", "--session", "ses_3").Exit(2).Err("the new target is gone: opencode session ses_3 is moved")
	cli.Do(t, "rebind", "--as", "bob", "--session", "ses_2", "--dry-run").Exit(0).Out("REBIND OK", "session=ses_2", "was=ses_1", "retired=ses_1", "NOTE nothing was written")
	after, err := os.ReadFile(plist)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a refusal and a dry run write nothing")
	assert.Empty(t, r.launchctl)

	cli.Do(t, "rebind", "--as", "bob", "--session", "ses_2").Exit(0).Out("REBIND OK", "session=ses_2", "was=ses_1",
		`REBIND RAN command="launchctl bootout gui/501/com.nova.friend-bob"`, `REBIND RAN command="launchctl bootstrap gui/501 `+plist+`"`,
		"NOTE push proof: down (rebound from ses_1 to ses_2")
	after, err = os.ReadFile(plist)
	require.NoError(t, err)
	assert.Contains(t, string(after), "<string>--session</string>\n    <string>ses_2</string>")
	assert.NotContains(t, string(after), "ses_1")
	assert.Equal(t, strings.Replace(string(before), "<string>ses_1</string>", "<string>ses_2</string>", 1), string(after), "only the session changed")
	bound, found, err := friend.ReadTarget(state)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "ses_2", bound.Session)
	assert.Equal(t, []string{"ses_1"}, bound.Retired)
	proofs, _, err := (&bus.Bus{Store: r.store}).PushProofs(context.Background(), "bob")
	require.NoError(t, err)
	require.Len(t, proofs, 1)
	assert.False(t, proofs[0].Up, "the old proof is down until the new session answers a check")
	assert.Contains(t, proofs[0].Reason, "rebound from ses_1 to ses_2")

	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--session", "ses_1").Exit(2).Err("session ses_1 was rebound away from", "nova-friend rebind --as bob --session <id>")
	cli.Do(t, "run", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--session", "ses_1", "--state-dir", state, "--dry-run").Exit(2).Err("session ses_1 was rebound away from")
	cli.Do(t, "run", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--session", "ses_2", "--state-dir", state, "--dry-run").Exit(0)
}

// run reads the named session's lifecycle at its start: a session that is
// gone owes no push proof and gets no check; the daemon starts
// target-invalid, tells the coordinator once and the friend once, delivers
// nothing, and status says so with the rebind line (docs/SPEC-FRIEND.md, "A
// gone session target"; tla/Delivery.tla, Prove).
func TestRunStartsTargetInvalidOnAGoneSession(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	_, err := (&bus.Bus{Store: r.store}).Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Subject: "card c9", Body: "go\n"})
	require.NoError(t, err)
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	var turns []string
	w.exec = func(_ context.Context, dir, _ string, args []string, _ string) (string, int, error) {
		if args[0] == "session" {
			return `[{"id":"ses_1","directory":"` + dir + `","updated":1}]`, 0, nil
		}
		turns = append(turns, strings.Join(args, " "))
		return "", 0, nil
	}
	w.beat = func(context.Context, string, string, time.Time, time.Time) (string, error) { return "", nil }
	stopAfter(&w, &cancel, 8*time.Minute)
	dir := t.TempDir()
	var out, errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--session", "ses_gone", "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Empty(t, turns, "nothing handed into a session that is gone: no proof, no check, no turn")
	assert.Contains(t, out.String(), "push proof: not asked: opencode session ses_gone is deleted")
	assert.Contains(t, out.String(), "session=target-invalid at the start")
	es, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 0)
	require.NoError(t, err)
	told := 0
	for _, e := range es {
		if strings.Contains(e.Message().Subject, "session target-invalid") {
			told++
		}
	}
	assert.Equal(t, 1, told, "one blocker to the coordinator")
	st, _, err := friend.ReadStatus(friend.StateDirIn(dir))
	require.NoError(t, err)
	assert.Equal(t, friend.SessionTargetInvalid, st.Session)
	r.now = st.At
	r.cli().Do(t, "status", "--as", "bob", "--dir", dir).Out("session=target-invalid", "session_id=ses_gone", "nova-friend rebind --as bob --session <id>")
}
