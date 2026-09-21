package secrets

import (
	"strings"
	"testing"
)

// docs/SPEC-SECRETS.md:408 demands: "The order is the contract, and the OK line is LAST."
// The example block at lines 398-406 shows SECRETS KEYGEN OK as the last line.
// TestKeygenPrintsTheOKLineLast pins that the machine-readable OK line is the last
// line of the receipt, for both --store and --store-less runs.
func TestKeygenPrintsTheOKLineLast(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		recoveryKey string
		placeholder bool
	}{
		{"with --store", "age1recovery", false},
		{"without --store", "<recovery key>", true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lines := keygenLines("rowan", "/k/rowan.key", "age1pub", tc.recoveryKey, tc.placeholder)
			if len(lines) == 0 {
				t.Fatal("keygen printed zero lines")
			}
			last := lines[len(lines)-1]
			if !strings.HasPrefix(last, "SECRETS KEYGEN OK ") {
				t.Errorf("the last line is not SECRETS KEYGEN OK; last line: %q\nfull output:\n%s",
					last, strings.Join(lines, "\n"))
			}
		})
	}
}
