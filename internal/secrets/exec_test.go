package secrets

import (
	"os"
	"strings"
	"testing"
)

// TestRunExecRefusalNamesEveryMissingRequiredFlagTogether pins 6dbfe26e (#1477):
// RunExec names every missing required flag in one refusal, plus a pasteable
// example, rather than returning on the first empty string. Reverting exec.go
// left ./internal/secrets green because nothing in the package called RunExec.
func TestRunExecRefusalNamesEveryMissingRequiredFlagTogether(t *testing.T) {
	code, err := RunExec("", "", "", "", "", nil, []string{os.Args[0]})
	if code != 125 {
		t.Fatalf("expected exit 125, got %d err=%v", code, err)
	}
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	msg := err.Error()
	for _, flag := range []string{"--store", "--as", "--key", "--sops", "--only"} {
		if !strings.Contains(msg, flag) {
			t.Errorf("refusal must name %s together, got: %s", flag, msg)
		}
	}
	if !strings.Contains(msg, "example:") || !strings.Contains(msg, "nova-secrets exec") {
		t.Errorf("refusal must carry a pasteable example invocation, got: %s", msg)
	}
}
