package sprint

import (
	"os"
	"strings"
	"testing"
)

// TestLocalOnlyModeRefusesEveryNonLoopbackAddressAndNeedsNoTailnet tests that:
// - In local-only mode, only loopback addresses are accepted
// - In local-only mode, tailnet addresses are refused
// - Outside local-only mode, tailnet addresses are accepted
func TestLocalOnlyModeRefusesEveryNonLoopbackAddressAndNeedsNoTailnet(t *testing.T) {
	// Test 1: In local-only mode, loopback is accepted
	os.Setenv("NOVA_SPRINT_LOCAL", "1")
	if reason := AddrOK("127.0.0.1:7395"); reason != "" {
		t.Errorf("local-only mode should accept loopback: %s", reason)
	}

	// Test 2: In local-only mode, tailnet is refused
	if reason := AddrOK("100.64.0.1:7395"); reason == "" {
		t.Error("local-only mode should refuse tailnet addresses")
	} else if !strings.Contains(reason, "local-only mode") {
		t.Errorf("local-only mode refusal should mention 'local-only mode': %s", reason)
	}

	// Test 3: In local-only mode, private is refused
	if reason := AddrOK("10.0.0.1:7395"); reason == "" {
		t.Error("local-only mode should refuse private addresses")
	} else if !strings.Contains(reason, "local-only mode") {
		t.Errorf("local-only mode refusal should mention 'local-only mode': %s", reason)
	}

	// Test 4: In local-only mode, public is refused
	if reason := AddrOK("8.8.8.8:7395"); reason == "" {
		t.Error("local-only mode should refuse public addresses")
	} else if !strings.Contains(reason, "local-only mode") {
		t.Errorf("local-only mode refusal should mention 'local-only mode': %s", reason)
	}

	// Test 5: Outside local-only mode, tailnet is accepted
	os.Unsetenv("NOVA_SPRINT_LOCAL")
	if reason := AddrOK("100.64.0.1:7395"); reason != "" {
		t.Errorf("normal mode should accept tailnet: %s", reason)
	}

	// Test 6: Outside local-only mode, private is accepted
	if reason := AddrOK("10.0.0.1:7395"); reason != "" {
		t.Errorf("normal mode should accept private: %s", reason)
	}

	// Test 7: Outside local-only mode, loopback is accepted
	if reason := AddrOK("127.0.0.1:7395"); reason != "" {
		t.Errorf("normal mode should accept loopback: %s", reason)
	}
}
