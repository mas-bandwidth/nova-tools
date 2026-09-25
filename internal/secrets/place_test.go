package secrets

import (
	"os"
	"path/filepath"
	"strconv"
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

// TestPlaceSSHWritesTheValueThroughBenchsh (#3350): sshPlaceSecret runs through
// internal/benchsh, the value rides stdin after the one exec line, and the file lands
// byte for byte at the quoted path with mode 0600. The fake ssh plays the machine
// locally (its login shell is `bash -c` on the remote command word) and records argv, so
// the test also proves the value never reaches argv.
func TestPlaceSSHWritesTheValueThroughBenchsh(t *testing.T) {
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv")
	ssh := filepath.Join(dir, "ssh")
	fake := "#!/bin/bash\nprintf '%s\\n' \"$@\" > " + strconv.Quote(argvLog) + "\nwhile [ \"$1\" = \"-o\" ]; do shift 2; done\nshift\nexec bash -c \"$1\"\n"
	if err := os.WriteFile(ssh, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "it's here", "sub dir", "secret")
	value := "line one\nit's $HOME `x` \"q\"\nno trailing newline"
	if err := sshPlaceSecret(ssh, "bench.example", dest, value); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != value {
		t.Fatalf("placed %q (%v), want the value byte for byte", got, err)
	}
	if fi, err := os.Stat(dest); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v (%v), want 0600", fi.Mode().Perm(), err)
	}
	argv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argv), "\nbash -s -- ") || strings.Contains(string(argv), "no trailing newline") {
		t.Fatalf("ssh argv %q: want internal/benchsh's bash -s -- and no value", argv)
	}
}
