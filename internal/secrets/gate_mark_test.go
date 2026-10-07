package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGateTellsAVerbMadeSealFromAHandSeal: a seat file sealed by hand (`sops <seat>.yaml`,
// `sops updatekeys <seat>.yaml`, any editor that leaves sops metadata and ENC[ values) is the
// same bytes as a file a verb wrote, so a gate that judges bytes alone approves it and every
// guarantee the verbs add on top of sops is bypassed (SPEC-SECRETS "gate"; tla/SecretsSeat.tla
// on sprint/md-secrets-h.w1.g1.e15, the MCSecretsSeatReachHandSeal config). The verbs leave a
// mark in the clear, the gate reads it, and a hand seal that lacks it is refused.
func TestGateTellsAVerbMadeSealFromAHandSeal(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	// The verb-made file: the tree's own seal path, the fake sops that refuses what real sops
	// refuses, handed a value that is already an ENC[ line so the fake's output is judged the
	// way real sops output is.
	storeDir := t.TempDir()
	rule := gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey) + "    unencrypted_regex: ^NOVA_SECRETS_WRITTEN_BY$\n"
	mustWrite(t, filepath.Join(storeDir, ".sops.yaml"), gateSops(rule), 0644)
	sopsPath := filepath.Join(t.TempDir(), "sops")
	require.NoError(t, os.Symlink(sharedFakeSops, sopsPath))
	sealed, _, err := sealEncrypt(realExecCommand, sopsPath, "", storeDir, "rowan.yaml", "seal",
		[]byte("GH_TOKEN: ENC[AES256_GCM,data:xyz,iv:abc,tag:def,type:str]\n"))
	require.NoError(t, err, "the seal path refused the fixture")
	verbMade := string(sealed)
	require.Contains(t, verbMade, "NOVA_SECRETS_WRITTEN_BY: seal ", "the seal path left no mark:\n%s", verbMade)

	// The hand seal: the same bytes without the mark.
	var hand []string
	for _, l := range strings.Split(verbMade, "\n") {
		if !strings.HasPrefix(l, "NOVA_SECRETS_WRITTEN_BY:") {
			hand = append(hand, l)
		}
	}
	handMade := strings.Join(hand, "\n")
	require.NotContains(t, handMade, "NOVA_SECRETS_WRITTEN_BY")

	gate := func(t *testing.T, content string) (string, int) {
		t.Helper()
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		head := gateCommit(t, dir, map[string]string{
			".sops.yaml": gateSops(rule),
			"rowan.yaml": content,
		})
		return RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	}

	line, code := gate(t, verbMade)
	assert.Equal(t, 0, code, "the verb-made file was refused: %s", line)
	assert.Equal(t, "GATE APPROVE files=2 machines=-", line)

	line, code = gate(t, handMade)
	assert.Equal(t, 1, code, "the hand-made file was approved: %s", line)
	assert.Equal(t, "GATE FAILED rule=1 check=2 file=rowan.yaml: the seat file was not written by a nova-secrets verb; seal it with nova-secrets seal or seat add, never by hand", line)
}
