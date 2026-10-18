package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friendEnv is the test Env with the platform the checks enumerate, injected
// through the same seam the checks read, so both the launchd and the systemd
// paths run on either bench.
type friendEnv struct {
	fakeEnv
	goos string
}

func (e friendEnv) GOOS() string { return e.goos }

// writeFriendPlist writes a launchd plist for friend name under root/home, with
// the daemon arguments after `nova-friend run`.
func writeFriendPlist(t *testing.T, root, name string, daemonArgs ...string) string {
	t.Helper()
	dir := filepath.Join(root, "home", "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	var b strings.Builder
	b.WriteString("<plist><dict><key>ProgramArguments</key><array>")
	b.WriteString("<string>nova-friend</string><string>run</string>")
	for _, a := range daemonArgs {
		b.WriteString("<string>" + a + "</string>")
	}
	b.WriteString("</array></dict></plist>")
	path := filepath.Join(dir, "com.nova.friend-"+name+".plist")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	return path
}

// writeFriendUnit writes a systemd user unit for friend name under
// root/home/.config/systemd/user, the daemon arguments on one ExecStart line, as
// nova-friend install writes it on linux.
func writeFriendUnit(t *testing.T, root, name string, daemonArgs ...string) string {
	t.Helper()
	dir := filepath.Join(root, "home", ".config", "systemd", "user")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	line := "ExecStart=nova-friend run"
	for _, a := range daemonArgs {
		line += " " + a
	}
	path := filepath.Join(dir, "nova-friend-"+name+".service")
	require.NoError(t, os.WriteFile(path, []byte(line+"\n"), 0o644))
	return path
}

// The friend-seat check is retired (check_friend_seat.go, 2026-10-17): the friend daemons it
// read are gone, so the doctor names no friend-seat.
func TestDoctorFriendSeatIsRetired(t *testing.T) {
	t.Parallel()
	assert.NotContains(t, Default.Names(), "friend-seat")
}

// The friend checks that remain enumerate the units a machine loads on either platform,
// through the injected one: the launchd plist's ProgramArguments, the systemd unit's ExecStart.
func TestFriendDaemonsAreEnumeratedOnBothPlatforms(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--coordinator", "old")
	darwin := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: root}}
	got := friendDaemons(darwin)
	require.Len(t, got, 1)
	assert.Equal(t, "ada", got[0].Name)
	assert.Equal(t, "com.nova.friend-ada", got[0].Label)
	assert.Equal(t, "old", argValue(got[0].Argv, "--coordinator"))

	linuxRoot := t.TempDir()
	writeFriendUnit(t, linuxRoot, "ada", "--as", "ada", "--coordinator", "old")
	linux := friendEnv{goos: "linux", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: linuxRoot}}
	got = friendDaemons(linux)
	require.Len(t, got, 1)
	assert.Equal(t, "nova-friend-ada", got[0].Label)
	assert.Equal(t, "old", argValue(got[0].Argv, "--coordinator"))

	empty := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "nowhere"}, root: root}}
	_, ok := friendDaemonsOrOK(empty)
	require.NotNil(t, ok)
	assert.Contains(t, ok.Evidence, "no friend daemons here")
}
