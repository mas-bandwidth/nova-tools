package wake

import (
	"testing"
	"time"
)

func TestSlotLeaseConstants(t *testing.T) {
	t.Parallel()

	if DefaultSlotLeaseTTL != 15*time.Second {
		t.Fatalf("DefaultSlotLeaseTTL = %v; want 15s", DefaultSlotLeaseTTL)
	}
	if DefaultSlotLeaseHeartbeat != 5*time.Second {
		t.Fatalf("DefaultSlotLeaseHeartbeat = %v; want 5s", DefaultSlotLeaseHeartbeat)
	}
}
