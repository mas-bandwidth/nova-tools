package secrets

import (
	"strings"
	"testing"
)

// docs/SPEC-SECRETS.md (keygen, and invariant 11) fixes the receipt's order: the rule block
// first, its NEXT: line last within it, then SECRETS KEYGEN OK as the last machine-readable
// line, then the two plain closing lines. TestKeygenEndsWithThePlainClosingLine (#1560) pins
// the closing lines; this test pins where the OK line sits relative to them and to the rule
// block, so a caller that parses the receipt finds the verdict right after the rule block.
func TestKeygenPrintsTheOKLineAfterTheRuleBlock(t *testing.T) {
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
			full := strings.Join(lines, "\n")
			if len(lines) < 4 {
				t.Fatalf("keygen printed fewer than four lines:\n%s", full)
			}
			ok := len(lines) - 3
			if !strings.HasPrefix(lines[ok], "SECRETS KEYGEN OK ") {
				t.Errorf("SECRETS KEYGEN OK is not the line just before the two plain closing lines:\n%s", full)
			}
			if lines[ok-1] != KeygenNextLine {
				t.Errorf("the rule block's NEXT: line does not sit directly above the OK line:\n%s", full)
			}
			for i, l := range lines[:ok] {
				if !strings.HasPrefix(l, "SECRETS RULE ") {
					t.Errorf("line %d above the OK line is not part of the rule block: %q\n%s", i, l, full)
				}
			}
			for i, l := range lines[ok+1:] {
				if strings.HasPrefix(l, "SECRETS ") {
					t.Errorf("machine-readable line after the OK line at %d: %q\n%s", ok+1+i, l, full)
				}
			}
		})
	}
}
