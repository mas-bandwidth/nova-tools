package secrets

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

func armHostGuard(t *testing.T) {
	t.Helper()
	t.Setenv(testguard.EnvNoHost, "1")
	testguard.Reload()
	t.Cleanup(func() {
		os.Unsetenv(testguard.EnvNoHost)
		testguard.Reload()
	})
}

// TestPlaceSSHSeamPanicsUnderTheGuard pins 47d81e9c: sshPlaceSecret calls
// testguard.RefuseHosts before the child. Reverting place.go left
// ./internal/secrets green because the package had no test of that seam.
func TestPlaceSSHSeamPanicsUnderTheGuard(t *testing.T) {
	armHostGuard(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("sshPlaceSecret ran a child under the guard; an unfaked seam must refuse before it reaches a host")
		}
		msg, _ := r.(string)
		for _, want := range []string{testguard.EnvNoHost, "ssh", "bench.invalid", "testguard.AllowHosts"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the panic must name %q so the reader sees the command and the remedy; got %q", want, msg)
			}
		}
	}()
	_ = sshPlaceSecret("ssh", "bench.invalid", "/tmp/secret", "value")
}
