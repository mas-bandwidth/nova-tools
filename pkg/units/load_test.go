package units

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadRefusesToTouchTheHostUnderTheTestGuard pins units.Load's first rule: under
// NOVA_TEST_NO_HOST -- which the Makefile and every card export -- Load is the one
// loader every unit install drives (the disk-guard unit of nova-swarm reaches it
// through unitLoad in cmd/nova-swarm/install.go), and it must refuse before any
// launchctl or systemctl child runs, so a test that reaches the loader starts no
// service on the machine it runs on. Break the guard -- drop the refusal or change
// its words -- and this goes red.
func TestLoadRefusesToTouchTheHostUnderTheTestGuard(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_TEST_NO_HOST") == "" {
		t.Skip("NOVA_TEST_NO_HOST is not set: arming Load here would start a real launchctl or systemctl child")
	}
	const want = "NOVA_TEST_NO_HOST is set: no service is loaded or unloaded on this machine"
	for _, tc := range []struct {
		name, goos, op, path string
	}{
		{"linux_load", "linux", "load", filepath.Join("dir", "nova-swarm-disk-guard.service")},
		{"linux_unload", "linux", "unload", filepath.Join("dir", "nova-swarm-disk-guard.service")},
		{"darwin_load", "darwin", "load", filepath.Join("dir", "nova-swarm.disk-guard.plist")},
		{"darwin_unload", "darwin", "unload", filepath.Join("dir", "nova-swarm.disk-guard.plist")},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := Load(tc.goos, tc.op, tc.path)
			require.Error(t, err)
			assert.EqualError(t, err, want)
		})
	}
}
