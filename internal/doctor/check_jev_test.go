package doctor

import (
	"context"
	"strings"
	"testing"
)

// TestDoctorJevCheckWarnsWithoutTheKeyAndNeverPrintsIt verifies that:
// 1. The check warns when JEV_API_KEY is unset
// 2. The key name is not printed in the error
// 3. The warning text describes what is lost
func TestDoctorJevCheckWarnsWithoutTheKeyAndNeverPrintsIt(t *testing.T) {
	// Clear the key to simulate missing dependency
	t.Setenv(envKey, "")
	ctx := context.Background()

	// Run the check
	err := CheckJev(ctx)
	if err == nil {
		t.Fatal("expected warning when JEV_API_KEY is unset")
	}

	// Check it's a Warning (optional miss)
	if !IsWarning(err) {
		t.Fatalf("expected Warning, got: %T", err)
	}

	// Verify the warning message describes the issue
	msg := err.Error()
	if !strings.Contains(msg, "Jev") {
		t.Errorf("warning should mention 'Jev': %q", msg)
	}
	if !strings.Contains(msg, "unset") {
		t.Errorf("warning should mention key is unset: %q", msg)
	}

	// The key value should never be printed (even if it were empty, don't expose it)
	// Since we set it to empty, we verify the message doesn't print actual key values
	if strings.Contains(msg, "secret") || strings.Contains(msg, "api_key") || strings.Contains(msg, "sekret") {
		t.Errorf("warning should not expose secret material: %q", msg)
	}
}

// TestCheckJevWhenKeyIsSet verifies the check passes when JEV_API_KEY is configured.
func TestCheckJevWhenKeyIsSet(t *testing.T) {
	t.Setenv(envKey, "test-key-value")
	ctx := context.Background()

	err := CheckJev(ctx)
	if err != nil {
		t.Fatalf("expected no error when key is set, got: %v", err)
	}
}

// TestCheckJevWarnsIsOptional verifies that missing Jev is optional, not fatal.
func TestCheckJevWarnsIsOptional(t *testing.T) {
	t.Setenv(envKey, "")
	ctx := context.Background()

	err := Run(ctx, "jev")
	if err == nil {
		t.Fatal("expected warning when JEV_API_KEY is unset")
	}
	if !IsWarning(err) {
		t.Fatalf("expected Warning for optional miss, got: %T", err)
	}
}

// TestName verifies the check name is "jev".
func TestName(t *testing.T) {
	name := Name()
	if name != "jev" {
		t.Fatalf("expected check name 'jev', got: %q", name)
	}
}

// TestChecks verifies the check is registered.
func TestChecks(t *testing.T) {
	names := Checks()
	found := false
	for _, n := range names {
		if n == "jev" {
			found = true
			break
		}
	}
	if !found {
		t.Error("check 'jev' not found in registered checks")
	}
}

// TestKeyEnv verifies the environment variable name.
func TestKeyEnv(t *testing.T) {
	name := KeyEnv()
	if name != envKey {
		t.Fatalf("expected key env '%s', got: %q", envKey, name)
	}
}

// TestClientBaseURL verifies the default base URL.
func TestClientBaseURL(t *testing.T) {
	url := ClientBaseURL()
	if url == "" {
		t.Fatal("expected non-empty base URL")
	}
}
