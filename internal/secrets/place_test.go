package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// TestPlaceSSHSeamPanicsUnderTheGuard pins 47d81e9c: sshPlaceSecret calls
// testguard.RefuseHosts before the child. Reverting place.go left
// ./internal/secrets green because the package had no test of that seam.
func TestPlaceSSHSeamPanicsUnderTheGuard(t *testing.T) {
	t.Parallel()
	guard := testguard.NewGuard(true)
	defer func() {
		r := recover()
		require.NotNil(t, r, "sshPlaceSecret ran a child under the guard; an unfaked seam must refuse before it reaches a host")
		msg, _ := r.(string)
		for _, want := range []string{testguard.EnvNoHost, "ssh", "bench.invalid", "testguard.AllowHosts"} {
			assert.Contains(t, msg, want, "the panic must name %q so the reader sees the command and the remedy; got %q", want, msg)
		}
	}()
	_ = sshPlaceSecret(nil, guard.RefuseHosts, "ssh", "bench.invalid", "/tmp/secret", "value")
}
