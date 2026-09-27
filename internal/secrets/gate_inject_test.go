package secrets

import (
	"strings"
	"testing"
)

// TestGateApprovesAnInjectPullRequest: the shape `seat inject` opens is an existing
// seat file re-sealed to the recipients it had -- still encrypted, its rule in place,
// no other file touched. That is the shape `seal` opens too, and the gate approves it
// with no new rule and no widening; the recipient rule asks the registry nothing
// because .sops.yaml did not change.
func TestGateApprovesAnInjectPullRequest(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
		"rowan.yaml": gateSealedFile(),
	})
	// The re-sealed file: same recipients, new ciphertext, one more sealed name.
	head := gateCommit(t, dir, map[string]string{
		"rowan.yaml": strings.Replace(gateSealedFile(), "data:xyz", "data:fresh", 1) +
			"NOVA_REDIS_BENCH_PASSWORD: ENC[AES256_GCM,data:new,iv:abc,tag:def,type:str]\n",
	})
	registry := gateMachines(t, gateRow("mini", "-"))
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: registry})
	if code != 0 {
		t.Fatalf("RunGate = (%q, %d), want APPROVE at exit 0", line, code)
	}
	if !strings.HasPrefix(line, "GATE APPROVE files=1 ") {
		t.Fatalf("RunGate line = %q, want APPROVE of the one re-sealed file", line)
	}
}
