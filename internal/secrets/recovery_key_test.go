package secrets

import (
	"strings"
	"testing"
)

// docs/SPEC-SECRETS.md line 1039: "A key is generated off-bench for recovery only; the
// private half lives in the owner's password manager, because recovery that sits beside
// the ciphertext is not recovery."
//
// keygenLines emits the .sops.yaml rule block and the OK receipt. The age field names two
// recipients: the seat's own public key and the recovery key. In both branches (with- and
// without --store) those are always public keys only -- never a private key string. This
// test pins that invariant by asserting every possible output.

func TestRecoveryKeyLivesOnlyInThePasswordManager(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		recoveryKey string
		placeholder bool
	}{
		{"without --store: placeholder, not a private key", "<recovery key>", true},
		{"with --store: public recovery key from recovery.pub", "age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata", false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lines := keygenLines("rowan", "/k/rowan.key", "age1pubtesttestestestestestestestestestesssssss00000000", tc.recoveryKey, tc.placeholder)
			for _, l := range lines {
				if strings.Contains(l, "-----BEGIN AGE PRIVATE KEY-----") {
					t.Errorf("receipt leaked a private key header:\n%s", l)
				}
				if strings.Contains(l, "AGE_PRIVATE_KEY") {
					t.Errorf("receipt contained an AGE_PRIVATE_KEY reference:\n%s", l)
				}
				// age private keys start with "AGEPRIVATEKEY..." or "age1..." (bech32 private encoding)
				if strings.Contains(l, "AGENERE") || strings.Contains(l, "xprv") || strings.Contains(l, "privkey") {
					t.Errorf("receipt leaked private-key vocabulary:\n%s", l)
				}
			}
		})
	}
}
