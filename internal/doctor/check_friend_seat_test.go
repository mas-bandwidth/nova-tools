package doctor

import (
	"context"
	"errors"
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

func TestDoctorFriendSeatHoldsTheDaemonToTheServerSeat(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--coordinator", "old", "--harness", "claude")
	fake := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: root,
		exec: func(name string, _ ...string) (string, error) {
			if name == "nova-sprint" {
				return `{"coordinator":"new"}`, nil
			}
			return "", errors.New("unexpected " + name)
		}}}

	r := checkFriendSeat(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "plist says old, seat is new")
	assert.Contains(t, r.Fix, "nova-friend install")

	fake.exec = func(string, ...string) (string, error) { return `{"coordinator":"old"}`, nil }
	r = checkFriendSeat(context.Background(), fake)
	assert.Equal(t, OK, r.Status)

	empty := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "nowhere"}, root: root}}
	r = checkFriendSeat(context.Background(), empty)
	assert.Equal(t, OK, r.Status)
	assert.Contains(t, r.Evidence, "no friend daemons here")

	// The linux path is enumerated and its ExecStart= argv parsed on this bench
	// too, through the injected platform.
	linuxRoot := t.TempDir()
	writeFriendUnit(t, linuxRoot, "ada", "--as", "ada", "--coordinator", "old", "--harness", "claude")
	linux := friendEnv{goos: "linux", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: linuxRoot,
		exec: func(name string, _ ...string) (string, error) {
			if name == "nova-sprint" {
				return `{"coordinator":"new"}`, nil
			}
			return "", errors.New("unexpected " + name)
		}}}
	r = checkFriendSeat(context.Background(), linux)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "plist says old, seat is new")
}
