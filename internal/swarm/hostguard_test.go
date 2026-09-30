package swarm

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// 47d81e9c put testguard.RefuseHosts on the swarm ssh/scp/rsync seams. Reverting
// bench.go and benchpull.go kept this package green: the class test that
// landed with it lives in internal/ci, not here.

func armHostGuard(t *testing.T) {
	t.Helper()
	// Reload after t.Setenv restores the incoming value. Cleanups are LIFO:
	// register this first so it runs last. Do not Unsetenv: this helper did
	// not own the incoming NOVA_TEST_NO_HOST=1 that make test sets.
	t.Cleanup(testguard.Reload)
	t.Setenv(testguard.EnvNoHost, "1")
	testguard.Reload()
}

// TestHostGuardCleanupRestoresCachedState is the isolation defect on #2152:
// with incoming NOVA_TEST_NO_HOST=1, armHostGuard's Unsetenv+Reload ran
// before t.Setenv restored the variable, so later tests saw env=1 while
// testguard.Refusing() was false. No child, no network.
func TestHostGuardCleanupRestoresCachedState(t *testing.T) {
	t.Cleanup(testguard.Reload)
	t.Setenv(testguard.EnvNoHost, "1")
	testguard.Reload()
	t.Run("arm", func(t *testing.T) {
		armHostGuard(t)
		if !testguard.Refusing() {
			t.Fatal("the armed subtest must refuse")
		}
	})
	if os.Getenv(testguard.EnvNoHost) != "1" {
		t.Fatalf("environment was %q, want 1", os.Getenv(testguard.EnvNoHost))
	}
	if !testguard.Refusing() {
		t.Fatal("environment was restored to 1 but cached host guard remained disabled")
	}
}

func mustPanicHost(t *testing.T, wantProg string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("an unfaked %s seam ran a child under the guard", wantProg)
		}
		msg, _ := r.(string)
		for _, want := range []string{testguard.EnvNoHost, wantProg, "bench.invalid", "testguard.AllowHosts"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the panic must name %q; got %q", want, msg)
			}
		}
	}()
	fn()
}
