package sandbox

import (
	"testing"
)

func TestParseGPUModeRejectsBlanketAccess(t *testing.T) {
	t.Parallel()

	if _, err := ParseGPUMode("all"); err == nil {
		t.Fatal("ParseGPUMode(\"all\") accepted a blanket GPU grant; only none|metal are explicit capabilities")
	}
	m, err := ParseGPUMode("metal")
	if err != nil || m != GPUMetal {
		t.Fatalf("ParseGPUMode(metal) = %q, %v; want metal", m, err)
	}
}
