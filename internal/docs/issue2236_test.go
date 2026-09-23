package docs

import (
	"os"
	"strings"
	"testing"
)

// issue2236_test.go proves that docs/SPEC-FLEET-KUBE.md carries the text and
// the four behaviours nova-tools#2236 asks for (items 27-30 of the "Tests this
// spec demands" table):
//
//	27. TestSealedAtApplyDecryptedInCluster
//	28. TestSecretHoldsOnlyTheKeysItsKindNames
//	29. TestJobNeverUsesEnvFrom
//	30. a-job-gets-only-the-keys-its-kind-names
//
// The rule of record is the one the issue names: seal secrets per kind and
// deliver only the named keys via secretKeyRef, never envFrom. It reads the
// spec as text and runs nothing.

// TestIssue2236 is the anchor test for nova-tools#2236. It reads
// docs/SPEC-FLEET-KUBE.md and proves the rule and the four behaviours the
// issue names are present in the spec: per-kind SealedSecrets with ciphertext
// in state and git, a Secret that holds only its kind's keys, a Job that never
// uses envFrom, and a pod whose environment holds exactly the named keys via
// one valueFrom.secretKeyRef per key (an envFrom pod is the failing mutation).
func TestIssue2236(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../docs/SPEC-FLEET-KUBE.md")
	if err != nil {
		t.Fatalf("docs/SPEC-FLEET-KUBE.md: %v; this spec is not optional", err)
	}
	body := string(data)

	// The rule of record #2236 names: seal secrets per kind and deliver only
	// the named keys via secretKeyRef, never envFrom.
	if !strings.Contains(body, "Seal secrets per kind and deliver only the named keys via secretKeyRef, never envFrom") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing the rule %q; nova-tools#2236 names this as the contract the Secrets section makes — one SealedSecret per kind and only the named keys through valueFrom.secretKeyRef, never envFrom", "Seal secrets per kind and deliver only the named keys via secretKeyRef, never envFrom")
	}

	// 27. TestSealedAtApplyDecryptedInCluster — at apply the plaintext is
	// produced from the store and immediately sealed into a SealedSecret, so
	// state and git hold only the ciphertext, and the sealed-secrets
	// controller decrypts it in-cluster into an ordinary Secret.
	if !strings.Contains(body, "TestSealedAtApplyDecryptedInCluster") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md: the Tests this spec demands section does not list `TestSealedAtApplyDecryptedInCluster` (item 27); the table must enumerate the test so a reader can verify it exists")
	}
	if !strings.Contains(body, "SealedSecret") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing `SealedSecret`; at apply the plaintext must be sealed into a SealedSecret so state and git hold only ciphertext — item 27")
	}
	if !strings.Contains(body, "hold only the ciphertext") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing `hold only the ciphertext`; state and git must hold only the ciphertext a SealedSecret carries, never the plaintext — item 27")
	}
	if !strings.Contains(body, "sealed-secrets controller decrypts it in-cluster") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing `sealed-secrets controller decrypts it in-cluster`; the decryption happens in-cluster into an ordinary Secret, never on the machine that applies — item 27")
	}

	// 28. TestSecretHoldsOnlyTheKeysItsKindNames — the Secret holds only the
	// keys one worker kind is entitled to and no other key exists in it,
	// generated per kind (nova-secrets-<kind>), not one fleet-wide Secret.
	if !strings.Contains(body, "TestSecretHoldsOnlyTheKeysItsKindNames") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md: the Tests this spec demands section does not list `TestSecretHoldsOnlyTheKeysItsKindNames` (item 28)")
	}
	if !strings.Contains(body, "nova-secrets-<kind>") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing `nova-secrets-<kind>`; the Secret is generated per kind, not one fleet-wide Secret a container could draw from — item 28")
	}
	if !strings.Contains(body, "That Secret holds only the keys one worker kind is entitled to") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing the sentence that says That Secret holds only the keys one worker kind is entitled to; this is the behaviour item 28 proves — without it the spec does not name the subset rule")
	}

	// 29. TestJobNeverUsesEnvFrom — a Job never uses envFrom, because envFrom
	// projects every key a Secret holds and cannot promise the subset.
	if !strings.Contains(body, "TestJobNeverUsesEnvFrom") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md: the Tests this spec demands section does not list `TestJobNeverUsesEnvFrom` (item 29)")
	}
	if !strings.Contains(body, "A Job never uses `envFrom`") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing `A Job never uses \\`envFrom\\``; envFrom projects every key a Secret holds and cannot promise the subset this section makes — item 29")
	}

	// 30. a-job-gets-only-the-keys-its-kind-names — each container names the
	// exact keys it gets, one valueFrom.secretKeyRef per key (go gets
	// DEEPSEEK_API_KEY and GH_TOKEN; lisp, docs and schema-leg get
	// ANTHROPIC_API_KEY and GH_TOKEN), and the pod's environment holds
	// exactly the named keys and nothing else; a pod built with envFrom
	// projects an extra key and is the mutation.
	if !strings.Contains(body, "a-job-gets-only-the-keys-its-kind-names") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing `a-job-gets-only-the-keys-its-kind-names`; each container must name the exact keys it gets and the pod's environment must hold exactly those — item 30")
	}
	if !strings.Contains(body, "one `valueFrom.secretKeyRef` per key") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing `one \\`valueFrom.secretKeyRef\\` per key`; the delivery is one named key at a time, never a projection of the whole Secret — item 30")
	}
	for _, key := range []string{"DEEPSEEK_API_KEY", "ANTHROPIC_API_KEY", "GH_TOKEN"} {
		if !strings.Contains(body, key) {
			t.Errorf("docs/SPEC-FLEET-KUBE.md missing %s; a go worker is named DEEPSEEK_API_KEY and GH_TOKEN, and lisp/docs/schema-leg are named ANTHROPIC_API_KEY and GH_TOKEN — item 30", key)
		}
	}
	if !strings.Contains(body, "a pod built with `envFrom`") {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing `a pod built with \\`envFrom\\``; an envFrom pod projects an extra key and is the failing mutation item 30 is proven able to catch")
	}
}
