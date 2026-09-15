//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Test 7's linux half, and the only thing that makes `landlock_abi_unknown` more than a
// word in the exit table: an ABI forced ABOVE this tool's table is
// `SANDBOX REFUSED reason=landlock_abi_unknown` at exit 125, naming both numbers, with
// the command not run.
//
// The seam is the `available` variable, the mirror of darwin's: the refusal is the
// behaviour under test and no kernel on the fleet reports an ABI above the table, so
// without the seam this rule has no test at all on the platform whose body is built.
//
// The tripwire on the exec path is the command's own stdout. `/bin/echo` writes through
// an INHERITED descriptor, which Landlock does not govern, so a byte in the buffer means
// the tool reached `cmd.Start` whether or not the wall went up -- which is what must not
// happen, rather than "it ran but was walled".
func TestUnknownLandlockABIRefusesOnLinux(t *testing.T) {
	saved := available
	forced := maxKnownABI + 1
	available = func() (int, bool) { return forced, true }
	defer func() { available = saved }()

	write := t.TempDir()
	home := filepath.Join(write, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	p, bad := Build(in(t, write, t.TempDir(), home, "/bin/echo", "tripwire"))
	if len(bad) > 0 {
		t.Fatalf("refused at build: %v", bad)
	}

	var out, errb bytes.Buffer
	code, err := Run(p, os.Environ(), strings.NewReader(""), &out, &errb, nil)
	if code != ExitRefused {
		t.Errorf("exit %d, want %d", code, ExitRefused)
	}
	r, ok := err.(Refusal)
	if !ok || r.Reason != "landlock_abi_unknown" {
		t.Fatalf("err = %v, want a landlock_abi_unknown refusal", err)
	}
	if r.Code() != ExitRefused {
		t.Errorf("the refusal carries exit %d, want %d", r.Code(), ExitRefused)
	}
	// Both numbers, because the line is what tells the reader which kernel it has and
	// which table it needs: a refusal naming neither is a refusal nobody can act on.
	for _, n := range []int{forced, maxKnownABI} {
		if !strings.Contains(r.Text, strconv.Itoa(n)) {
			t.Errorf("the refusal does not name %d: %q", n, r.Text)
		}
	}
	if out.Len() != 0 {
		t.Errorf("the command produced output; it must not have run: %q", out.String())
	}
}
