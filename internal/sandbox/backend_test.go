package sandbox

import "testing"

// The issue's sentence, as a pin: the linux backend that Space cards run walled is the
// unshare one, and `nova-sandbox check` reports backend=unshare there. The name lives in
// backendNameFor so this test runs on every platform and goes red on the machine where
// the spec and the build would otherwise silently drift (the Landlock name the body
// once promised, before the unshare body landed).
func TestLinuxBackendIsUnshare(t *testing.T) {
	if got := backendNameFor("linux"); got != "unshare" {
		t.Fatalf("the linux backend is named %q; issue #69 names unshare (user+mount+net namespaces, read-only binds for --read, writable for --write)", got)
	}
}
