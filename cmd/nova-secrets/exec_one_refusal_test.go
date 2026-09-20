package main

import (
	"strings"
	"testing"
)

// TestExecRefusalNamesEveryMissingRequiredFlagTogether runs exec with no flags and
// asserts that the single refusal names --store, --as, --key and --sops together, plus a
// pasteable example invocation, rather than revealing the required flags one refusal at a
// time (nova-tools#1477).
func TestExecRefusalNamesEveryMissingRequiredFlagTogether(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)

	out, errOut, code := runNovaSecrets(bin, "exec", "--", "true")
	if code != 125 {
		t.Fatalf("expected exit 125, got %d (stdout=%q)", code, out)
	}

	lines := strings.Split(strings.TrimSpace(errOut), "\n")
	if len(lines) != 1 {
		t.Errorf("expected a single refusal line, got %d: %s", len(lines), errOut)
	}

	for _, flag := range []string{"--store", "--as", "--key", "--sops"} {
		if !strings.Contains(errOut, flag) {
			t.Errorf("refusal must name %s together, got: %s", flag, errOut)
		}
	}

	if !strings.Contains(errOut, "example:") || !strings.Contains(errOut, "nova-secrets exec") {
		t.Errorf("refusal must carry a pasteable example invocation, got: %s", errOut)
	}
}
