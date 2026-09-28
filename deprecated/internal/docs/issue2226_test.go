package docs

import (
	"os"
	"strings"
	"testing"
)

// issue2226_test.go proves nova-tools#2226: the spec demands that a card naming
// a secret path is refused, and secrets are delivered environment-only.
// Behaviours 31–33 of SPEC-FLEET-KUBE.md's "Tests this spec demands" section.
func TestIssue2226(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-FLEET-KUBE.md")
	if err != nil {
		t.Fatalf("read docs/SPEC-FLEET-KUBE.md: %v", err)
	}
	text := string(spec)

	// Behaviour 31: TestExecOnlyRequireRefusesAShortSecret
	// The container's command is nova-secrets exec --only ... --require ...,
	// so --require refuses before the harness starts if the Secret is short.
	for _, want := range []string{
		"nova-secrets exec --only",
		"--require",
		"TestExecOnlyRequireRefusesAShortSecret",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/SPEC-FLEET-KUBE.md missing %q (behaviour 31: exec --only/--require refuses when the Secret is short)", want)
		}
	}

	// Behaviour 32: TestSecretDeliveryIsEnvironmentOnly
	// Environment only, never a volumeMount, never an image layer, never a build argument.
	for _, want := range []string{
		"Environment only",
		"never a `volumeMount`",
		"never an image layer",
		"never a build argument",
		"TestSecretDeliveryIsEnvironmentOnly",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/SPEC-FLEET-KUBE.md missing %q (behaviour 32: secrets environment-only, never volumeMount/image-layer/build-arg)", want)
		}
	}

	// Behaviour 33: a-card-that-names-a-secret-path-is-refused
	// A card mentioning .key, the store path, auth.json, or a secretRef mounted
	// as a file is refused by the puller before a Job is created.
	for _, want := range []string{
		"a-card-that-names-a-secret-path-is-refused",
		"refused by the puller",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/SPEC-FLEET-KUBE.md missing %q (behaviour 33: card naming secret path refused by puller)", want)
		}
	}
}
