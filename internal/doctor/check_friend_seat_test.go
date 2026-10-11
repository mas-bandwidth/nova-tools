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

func TestDoctorFriendSeatHoldsTheDaemonToTheServerSeat(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--coordinator", "old", "--harness", "claude")
	fake := fakeEnv{env: map[string]string{"HOME": "home"}, root: root,
		exec: func(name string, _ ...string) (string, error) {
			if name == "nova-sprint" {
				return `{"coordinator":"new"}`, nil
			}
			return "", errors.New("unexpected " + name)
		}}

	r := checkFriendSeat(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "plist says old, seat is new")
	assert.Contains(t, r.Fix, "nova-friend install")

	fake.exec = func(string, ...string) (string, error) { return `{"coordinator":"old"}`, nil }
	r = checkFriendSeat(context.Background(), fake)
	assert.Equal(t, OK, r.Status)

	empty := fakeEnv{env: map[string]string{"HOME": "nowhere"}, root: root}
	r = checkFriendSeat(context.Background(), empty)
	assert.Equal(t, OK, r.Status)
	assert.Contains(t, r.Evidence, "no friend daemons here")
}
