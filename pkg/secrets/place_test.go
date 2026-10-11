package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testguard"
)

// TestPlaceSSHSeamPanicsUnderTheGuard pins 47d81e9c: sshPlaceSecret calls
// testguard.RefuseHosts before the child. Reverting place.go left
// ./pkg/secrets green because the package had no test of that seam. The
// armed guard is injected per test, so the test opens with t.Parallel and never
// sets NOVA_TEST_NO_HOST in the process environment.
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
	_ = sshPlaceSecret(nil, guard, "ssh", "bench.invalid", "/tmp/secret", "value")
}
