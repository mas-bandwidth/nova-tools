package secrets

import (
	"strings"
	"testing"
)

// TestSeatRuleGateIsExactlyTwoRecipients pins this spec sentence verbatim:
//
//	"A seat file is opened only by its seat key and the recovery key,
//	 exactly two recipients, enforced by the seat-rule gate on .sops.yaml,
//	 because a third recipient is the grant every review is meant to catch."
//
// The gate rejects any rule whose age-recipient count differs from two.
func TestSeatRuleGateIsExactlyTwoRecipients(t *testing.T) {
	t.Run("refuses a rule with three recipients", func(t *testing.T) {
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		head := gateCommit(t, dir, map[string]string{
			".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey, gateThirdKey),
			"rowan.yaml": gateSealedFile(),
		})
		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		if code != 2 {
			t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
		}
		if !strings.Contains(line, "GATE REFUSE rule=1") {
			t.Fatalf("RunGate line = %q, want REFUSE naming rule=1", line)
		}
		if !strings.Contains(line, "exactly two") {
			t.Fatalf("RunGate line = %q, want it to mention 'exactly two'", line)
		}
	})

	t.Run("refuses a rule with one recipient (missing recovery)", func(t *testing.T) {
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		// Rule with only the seat key, no recovery key at all — just one recipient.
		sopsOne := "creation_rules:\n" +
			"  - path_regex: ^rowan\\.yaml$\n" +
			"    age: " + gateSeatKey + "\n"
		head := gateCommit(t, dir, map[string]string{
			".sops.yaml": sopsOne,
			"rowan.yaml": gateSealedFile(),
		})
		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		if code != 2 {
			t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
		}
		if !strings.Contains(line, "GATE REFUSE") {
			t.Fatalf("RunGate line = %q, want a refusal", line)
		}
	})
}
