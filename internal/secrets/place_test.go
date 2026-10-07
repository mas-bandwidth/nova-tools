package secrets

import (
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// guardLock serializes access to the global testguard state to prevent race
// conditions when tests run in parallel.
var guardLock sync.Mutex

// TestPlaceSSHSeamPanicsUnderTheGuard pins 47d81e9c: sshPlaceSecret calls
// testguard.RefuseHosts before the child. Reverting place.go left
// ./internal/secrets green because the package had no test of that seam.
func TestPlaceSSHSeamPanicsUnderTheGuard(t *testing.T) {
	t.Parallel()
	// Use a path that the guard will recognize as a real program (not in a temp dir).
	// We use /usr/bin/ssh as the path; on systems without ssh, the guard will still
	// resolve it and recognize it as non-fake, causing the expected panic.
	guardLock.Lock()
	defer guardLock.Unlock()
	oldNoHost := os.Getenv(testguard.EnvNoHost)
	os.Setenv(testguard.EnvNoHost, "1")
	defer os.Setenv(testguard.EnvNoHost, oldNoHost)
	testguard.Reload()

	defer func() {
		r := recover()
		require.NotNil(t, r, "sshPlaceSecret ran a child under the guard; an unfaked seam must refuse before it reaches a host")
		msg, _ := r.(string)
		for _, want := range []string{testguard.EnvNoHost, "ssh", "bench.invalid", "testguard.AllowHosts"} {
			assert.Contains(t, msg, want, "the panic must name %q so the reader sees the command and the remedy; got %q", want, msg)
		}
	}()
	_ = sshPlaceSecret(nil, "/usr/bin/ssh", "bench.invalid", "/tmp/secret", "value")
}
