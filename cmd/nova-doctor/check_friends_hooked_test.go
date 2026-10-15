package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookEnv is the fake world the check reads: Exec answers from a table of
// command lines (nova-config, nova-hook, the service manager and the service's
// binary), so no store, process, socket or clock is touched.
type hookEnv struct {
	goos  string
	lines map[string]answer
}

// answer is one command line's output or error.
type answer struct {
	out string
	err error
}

func (hookEnv) Getenv(string) string                  { return "" }
func (hookEnv) Now() time.Time                        { return time.Time{} }
func (hookEnv) ReadFile(string) ([]byte, error)       { return nil, fs.ErrNotExist }
func (hookEnv) ReadDir(string) ([]fs.DirEntry, error) { return nil, fs.ErrNotExist }
func (hookEnv) Dial(context.Context, string, string) error {
	return errors.New("no network here")
}
func (e hookEnv) GOOS() string { return e.goos }
func (e hookEnv) Exec(_ context.Context, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	a, ok := e.lines[line]
	if !ok {
		return "", errors.New("exit status 1: unexpected " + line)
	}
	return a.out, a.err
}

const (
	installedLine = "nova-hook v2 darwin/arm64 go1.26.6"
	binary        = "/opt/nova/bin/nova-hook"
)

// friend is one name's row in the fake hook record.
type friend struct {
	name, role, state, version string
}

// rosterDoc is the `nova-config friend list --json` document for names.
func rosterDoc(t *testing.T, names ...string) string {
	t.Helper()
	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		items = append(items, map[string]any{"kind": "friend", "fields": map[string]any{"name": n, "pronouns": "they/them"}})
	}
	raw, err := json.Marshal(map[string]any{"items": items})
	require.NoError(t, err)
	return string(raw)
}

// friendsDoc is the `nova-hook friends --json` document the fake store answers
// with, built through the same JSON the tool prints.
func friendsDoc(t *testing.T, serve string, friends ...friend) string {
	t.Helper()
	items := make([]map[string]any, 0, len(friends))
	for _, f := range friends {
		items = append(items, map[string]any{"kind": "name", "fields": map[string]any{
			"name": f.name, "role": f.role, "state": f.state, "version": f.version}})
	}
	raw, err := json.Marshal(map[string]any{
		"facts": map[string]any{"serve_alive": serve != "", "serve_version": serve},
		"items": items,
	})
	require.NoError(t, err)
	return string(raw)
}

// launchdUp is `launchctl list` for a loaded, running job.
func launchdUp(name string) string {
	return `{
	"Label" = "nova-hook.client.` + name + `";
	"LastExitStatus" = 0;
	"PID" = 4242;
	"ProgramArguments" = (
		"` + binary + `";
		"hook";
	);
};
`
}

// world is a darwin fake where every named friend is rostered, up on v2 in
// the record, and has a running launchd service on v2; a case edits it.
func world(t *testing.T, friends ...friend) hookEnv {
	t.Helper()
	names := make([]string, 0, len(friends))
	e := hookEnv{goos: "darwin", lines: map[string]answer{
		"nova-hook version": {out: installedLine},
		binary + " version": {out: installedLine},
	}}
	for _, f := range friends {
		names = append(names, f.name)
		e.lines["launchctl list nova-hook.client."+f.name] = answer{out: launchdUp(f.name)}
	}
	e.lines["nova-config friend list --json"] = answer{out: rosterDoc(t, names...)}
	e.lines["nova-hook friends --json"] = answer{out: friendsDoc(t, "v2", friends...)}
	return e
}

// assertFail is a fail whose evidence carries want and whose fix names who.
func assertFail(t *testing.T, got doctor.Result, want, who string) {
	t.Helper()
	assert.Equal(t, doctor.Fail, got.Status, got)
	assert.Contains(t, got.Evidence, want)
	assert.Contains(t, got.Fix, who)
}

// TestFriendsHookedAllGood: every rostered friend up on the installed build
// with its service running that build is ok. It is able to fail: any fault
// below turns it.
func TestFriendsHookedAllGood(t *testing.T) {
	t.Parallel()
	got := checkFriendsHooked(context.Background(), world(t, friend{"ada", "friend", "up", "v2"}, friend{"bob", "comms", "up", "v2"}))
	assert.Equal(t, doctor.OK, got.Status, got)
	assert.Contains(t, got.Evidence, "ada (friend), bob (comms)")
}

// TestFriendsHookedRosterIsNovaConfig pins rule 2: the roster is nova-config's
// friend list, never the names ever hooked. A hooked name off the roster is
// not judged, and a rostered friend with no hook record is a fail.
func TestFriendsHookedRosterIsNovaConfig(t *testing.T) {
	t.Parallel()
	t.Run("a hooked name off the roster is not judged", func(t *testing.T) {
		t.Parallel()
		e := world(t, friend{"ada", "friend", "up", "v2"})
		e.lines["nova-hook friends --json"] = answer{out: friendsDoc(t, "v2", friend{"ada", "friend", "up", "v2"}, friend{"old", "comms", "down", "v1"})}
		got := checkFriendsHooked(context.Background(), e)
		assert.Equal(t, doctor.OK, got.Status, got)
		assert.NotContains(t, got.Evidence, "old")
	})
	t.Run("a rostered friend with no hook record fails", func(t *testing.T) {
		t.Parallel()
		e := world(t, friend{"ada", "friend", "up", "v2"}, friend{"carl", "friend", "up", "v2"})
		e.lines["nova-hook friends --json"] = answer{out: friendsDoc(t, "v2", friend{"ada", "friend", "up", "v2"})}
		assertFail(t, checkFriendsHooked(context.Background(), e), "friend carl has no hook record", "carl")
	})
}

// TestFriendsHookedEmptyRosterFails pins rule 2's other half: an empty roster
// is a fail, never "every friend is up".
func TestFriendsHookedEmptyRosterFails(t *testing.T) {
	t.Parallel()
	e := world(t)
	got := checkFriendsHooked(context.Background(), e)
	assert.Equal(t, doctor.Fail, got.Status, got)
	assert.Contains(t, got.Evidence, "roster is empty")
	assert.NotContains(t, got.Evidence, "every")
}

// TestFriendsHookedEveryRole pins rule 3: a rostered friend is checked whatever
// role its record shows; coordinator, member and reader are reported, and fail
// when they are not up.
func TestFriendsHookedEveryRole(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"coordinator", "member", "reader"} {
		role := role
		t.Run(role+" up is reported", func(t *testing.T) {
			t.Parallel()
			got := checkFriendsHooked(context.Background(), world(t, friend{"ada", role, "up", "v2"}))
			assert.Equal(t, doctor.OK, got.Status, got)
			assert.Contains(t, got.Evidence, "ada ("+role+")")
		})
		t.Run(role+" down fails", func(t *testing.T) {
			t.Parallel()
			got := checkFriendsHooked(context.Background(), world(t, friend{"ada", role, "down", "v2"}))
			assertFail(t, got, "friend ada ("+role+") is down", "ada")
		})
	}
}

// TestFriendsHookedBuilds pins the build comparisons: a friend hooked by
// another build, and a running heartbeat server of another build, each fail.
func TestFriendsHookedBuilds(t *testing.T) {
	t.Parallel()
	got := checkFriendsHooked(context.Background(), world(t, friend{"ada", "friend", "up", "v1"}))
	assertFail(t, got, "friend ada (friend) is hooked by v1", "nova-update")
	e := world(t, friend{"ada", "friend", "up", "v2"})
	e.lines["nova-hook friends --json"] = answer{out: friendsDoc(t, "v1", friend{"ada", "friend", "up", "v2"})}
	assertFail(t, checkFriendsHooked(context.Background(), e), "the running hook serve is v1", "hook-serve")
}

// TestFriendsHookedServiceAdoption pins rule 4: the supervised hook service
// exists, is running, and its binary is the installed build, on launchd and
// on systemd.
func TestFriendsHookedServiceAdoption(t *testing.T) {
	t.Parallel()
	ada := friend{"ada", "friend", "up", "v2"}
	t.Run("launchd service not loaded", func(t *testing.T) {
		t.Parallel()
		e := world(t, ada)
		delete(e.lines, "launchctl list nova-hook.client.ada")
		assertFail(t, checkFriendsHooked(context.Background(), e), "no hook service nova-hook.client.ada loaded", "ada")
	})
	t.Run("launchd service loaded, not running", func(t *testing.T) {
		t.Parallel()
		e := world(t, ada)
		e.lines["launchctl list nova-hook.client.ada"] = answer{out: strings.Replace(launchdUp("ada"), "\t\"PID\" = 4242;\n", "", 1)}
		assertFail(t, checkFriendsHooked(context.Background(), e), "nova-hook.client.ada is not running", "ada")
	})
	t.Run("launchd service runs another build", func(t *testing.T) {
		t.Parallel()
		e := world(t, ada)
		e.lines[binary+" version"] = answer{out: "nova-hook v1 darwin/arm64 go1.26.6"}
		assertFail(t, checkFriendsHooked(context.Background(), e), "nova-hook.client.ada runs v1, installed nova-hook is v2", "ada")
	})
	const show = "systemctl --user show nova-hook-ada.service --property=LoadState,ActiveState,ExecStart"
	linux := func(t *testing.T, out string) hookEnv {
		e := world(t, ada)
		e.goos = "linux"
		delete(e.lines, "launchctl list nova-hook.client.ada")
		e.lines[show] = answer{out: out}
		return e
	}
	execStart := "ExecStart={ path=" + binary + " ; argv[]=" + binary + " hook --as ada ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }\n"
	t.Run("systemd service running the installed build", func(t *testing.T) {
		t.Parallel()
		got := checkFriendsHooked(context.Background(), linux(t, "LoadState=loaded\nActiveState=active\n"+execStart))
		assert.Equal(t, doctor.OK, got.Status, got)
	})
	t.Run("systemd unit not found", func(t *testing.T) {
		t.Parallel()
		got := checkFriendsHooked(context.Background(), linux(t, "LoadState=not-found\nActiveState=inactive\nExecStart=\n"))
		assertFail(t, got, "no hook service nova-hook-ada.service loaded", "ada")
	})
	t.Run("systemd unit not running", func(t *testing.T) {
		t.Parallel()
		got := checkFriendsHooked(context.Background(), linux(t, "LoadState=loaded\nActiveState=failed\n"+execStart))
		assertFail(t, got, "nova-hook-ada.service is not running", "ada")
	})
}

// TestFriendsHookedUnreadableFails pins rule 5: any input that cannot be read
// (a command that fails, a document that does not parse or carries no items)
// is a fail, never a pass.
func TestFriendsHookedUnreadableFails(t *testing.T) {
	t.Parallel()
	ada := friend{"ada", "friend", "up", "v2"}
	broken := errors.New("exit status 1")
	cases := []struct {
		name, line string
		a          answer
		want       string
	}{
		{"nova-config fails", "nova-config friend list --json", answer{err: broken}, "nova-config friend list did not answer"},
		{"nova-config prints no JSON", "nova-config friend list --json", answer{out: "FRIEND name=ada"}, "nova-config friend list printed no JSON"},
		{"nova-config prints no items", "nova-config friend list --json", answer{out: "{}"}, "nova-config friend list printed no items"},
		{"nova-hook friends fails", "nova-hook friends --json", answer{err: broken}, "nova-hook friends did not answer"},
		{"nova-hook friends prints no JSON", "nova-hook friends --json", answer{out: "FRIENDS OK"}, "nova-hook friends printed no JSON"},
		{"nova-hook friends prints no items", "nova-hook friends --json", answer{out: `{"facts":{}}`}, "nova-hook friends printed no items"},
		{"nova-hook version fails", "nova-hook version", answer{err: broken}, "nova-hook version did not answer"},
		{"nova-hook version unparsable", "nova-hook version", answer{out: "garbage"}, "nova-hook version printed no version line"},
		{"service binary version fails", binary + " version", answer{err: broken}, "did not answer version"},
		{"service binary version unparsable", binary + " version", answer{out: "garbage"}, "printed no version line"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := world(t, ada)
			e.lines[tc.line] = tc.a
			got := checkFriendsHooked(context.Background(), e)
			assert.Equal(t, doctor.Fail, got.Status, got)
			assert.Contains(t, got.Evidence, tc.want)
		})
	}
}
