package friend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWakePingUnitTextAndInstall(t *testing.T) {
	t.Parallel()
	u := WakePingUnit{
		OS:     "darwin",
		Exe:    "/opt/nova/bin/nova-friend",
		As:     "ada",
		Every:  10 * time.Minute,
		Redis:  "127.0.0.1:6381",
		Server: "127.0.0.1:6390",
		Log:    "/tmp/wake.log",
	}
	text, err := u.Text()
	require.NoError(t, err)
	assert.Contains(t, text, "<string>nova-friend.wake-ping</string>")
	assert.Contains(t, text, "<string>--wake</string>")
	assert.Contains(t, text, "<string>--every</string>")
	assert.Contains(t, text, "<string>10m0s</string>")
	assert.Contains(t, text, "<string>--to-friends</string>")
	assert.Contains(t, text, "<string>--as</string>")
	assert.Contains(t, text, "<string>ada</string>")

	// Linux systemd unit
	u.OS = "linux"
	stext, err := u.Text()
	require.NoError(t, err)
	assert.Contains(t, stext, "[Unit]")
	assert.Contains(t, stext, "ExecStart=")
	assert.Contains(t, stext, "--to-friends")

	// Install and uninstall
	dir := t.TempDir()
	var loaded, unloaded []string
	in := WakePingInstaller{
		Dir:    dir,
		Load:   func(p string) error { loaded = append(loaded, p); return nil },
		Unload: func(p string) error { unloaded = append(unloaded, p); return nil },
	}
	res, err := in.Install(u)
	require.NoError(t, err)
	assert.True(t, res.Changed)
	assert.FileExists(t, res.Path)
	assert.Equal(t, []string{res.Path}, loaded)

	// Second install when unchanged
	res2, err := in.Install(u)
	require.NoError(t, err)
	assert.False(t, res2.Changed)

	// Uninstall
	ures, err := in.Uninstall("linux")
	require.NoError(t, err)
	assert.True(t, ures.Changed)
	assert.NoFileExists(t, res.Path)
	assert.Equal(t, []string{res.Path}, unloaded)
}
