package secrets

import (
	"strings"
	"testing"
)

// TestIngestIsRotation verifies that when a value is sealed (ingested), the old
// value is revoked from the plaintext. This pins the spec sentence at
// docs/SPEC-SECRETS.md:1016: "ingest is rotation — a value enters by seal and the
// old value is revoked at the provider the same hour, because sealing the new
// value while the old one still works banks a rotation nobody finished."
func TestIngestIsRotation(t *testing.T) {
	skipPOSIXFakesOnWindows(t)

	const oldValue = "oldsecretvalue"
	const newValue = "newsecretvalue"

	f := newSealFixture(t, "TARGET: "+oldValue+"\n")

	line, err := RunSeal(f.options(t, "TARGET", newValue+"\n", true))
	if err != nil {
		t.Fatalf("RunSeal: %v", err)
	}

	// Verify the plaintext handed to encrypt contains only the new value.
	stdin := readMaybe(t, f.sopsStdin)
	if !strings.Contains(stdin, "TARGET: "+newValue) {
		t.Errorf("encrypt stdin missing the new value; got:\n%s", stdin)
	}

	// The old value must NOT appear in the plaintext - it was revoked.
	if strings.Contains(stdin, oldValue) {
		t.Errorf("encrypt stdin still contains the revoked old value %q; got:\n%s", oldValue, stdin)
	}

	// Assert exactly one TARGET line in the plaintext.
	if n := strings.Count(stdin, "TARGET:"); n != 1 {
		t.Errorf("encrypt stdin holds %d TARGET lines, want 1:\n%s", n, stdin)
	}

	// Verify the OK line does not leak the value.
	if strings.Contains(line, oldValue) || strings.Contains(line, newValue) {
		t.Errorf("value leaked into OK line: %s", line)
	}
}
