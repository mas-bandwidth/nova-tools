package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// TestLandDutyRegisteredAndOffWithoutHosts: the reconciler carries the land
// duty (nova-tools #3898), switched off under NOVA_TEST_NO_HOST=1, and built
// with every production seam otherwise.
func TestLandDutyRegisteredAndOffWithoutHosts(t *testing.T) {
	var found *reconcileDutyBuilder
	for i := range reconcileDuties {
		if reconcileDuties[i].Name == "land" {
			found = &reconcileDuties[i]
		}
	}
	if found == nil {
		t.Fatal("no land duty registered")
	}
	t.Setenv("NOVA_TEST_NO_HOST", "1")
	testguard.Reload()
	t.Cleanup(testguard.Reload)
	if d, err := found.Build(store.New(nil)); err != nil || d != nil {
		t.Fatalf("under NOVA_TEST_NO_HOST=1: duty=%v err=%v, want off", d, err)
	}
	d := landDuty(store.New(nil))
	if d.GitHub == nil || d.Request == nil || d.Mirror == nil || d.Out == nil {
		t.Fatalf("seams missing: %+v", d)
	}
}
