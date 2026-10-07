package sprint

import (
	"testing"
)

func TestLocalOnlyModeRefusesEveryNonLoopbackAddressAndNeedsNoTailnet(t *testing.T) {
	// Test in local-only mode
	t.Run("local-only mode", func(t *testing.T) {
		t.Setenv("NOVA_SPRINT_LOCAL", "1")

		t.Run("loopback is accepted", func(t *testing.T) {
			if got := CheckAddr("127.0.0.1:6379"); got != "" {
				t.Errorf("CheckAddr(127.0.0.1:6379) = %q, want empty", got)
			}
		})

		t.Run("tailnet address is refused", func(t *testing.T) {
			if got := CheckAddr("100.64.0.1:6379"); got == "" {
				t.Errorf("CheckAddr(100.64.0.1:6379) = empty, want refusal")
			}
		})

		t.Run("public address is refused", func(t *testing.T) {
			if got := CheckAddr("8.8.8.8:6379"); got == "" {
				t.Errorf("CheckAddr(8.8.8.8:6379) = empty, want refusal")
			}
		})
	})

	// Test in normal mode (not local-only)
	t.Run("normal mode", func(t *testing.T) {
		t.Setenv("NOVA_SPRINT_LOCAL", "")

		t.Run("loopback is accepted", func(t *testing.T) {
			if got := CheckAddr("127.0.0.1:6379"); got != "" {
				t.Errorf("CheckAddr(127.0.0.1:6379) = %q, want empty", got)
			}
		})

		t.Run("tailnet address is accepted", func(t *testing.T) {
			if got := CheckAddr("100.64.0.1:6379"); got != "" {
				t.Errorf("CheckAddr(100.64.0.1:6379) = %q, want empty", got)
			}
		})

		t.Run("public address is refused", func(t *testing.T) {
			if got := CheckAddr("8.8.8.8:6379"); got == "" {
				t.Errorf("CheckAddr(8.8.8.8:6379) = empty, want refusal")
			}
		})
	})
}

func TestAcceptableAddr(t *testing.T) {
	t.Run("local-only mode", func(t *testing.T) {
		t.Setenv("NOVA_SPRINT_LOCAL", "1")

		if !AcceptableAddr("127.0.0.1:6379") {
			t.Error("AcceptableAddr(127.0.0.1:6379) in local-only mode want true, got false")
		}
		if AcceptableAddr("100.64.0.1:6379") {
			t.Error("AcceptableAddr(100.64.0.1:6379) in local-only mode want false, got true")
		}
	})

	t.Run("normal mode", func(t *testing.T) {
		t.Setenv("NOVA_SPRINT_LOCAL", "")

		if !AcceptableAddr("127.0.0.1:6379") {
			t.Error("AcceptableAddr(127.0.0.1:6379) want true, got false")
		}
		if !AcceptableAddr("100.64.0.1:6379") {
			t.Error("AcceptableAddr(100.64.0.1:6379) want true, got false")
		}
	})
}
