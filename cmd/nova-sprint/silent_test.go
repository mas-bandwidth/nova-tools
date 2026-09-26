package main

import (
	"testing"
)

// The controls of the no-silent-failure sweep (Glenn 2026-09-26 11:30 AM ET:
// "every verb in nova tools related to current work should not fail
// silently"): each was a place a failure vanished, and each now prints one
// typed line and exits non-zero, or reaches the duty's DUTY line.

// TestCapacityHookRefusesAMalformedBudget: NOVA_CI_CPU_MILLI=abc used to
// become the 2000 default in silence. It is an error that names the
// variable (the verb prints it as its refusal, exit 2, nothing taken).
func TestCapacityHookRefusesAMalformedBudget(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_CI_CPU_MILLI": "abc"}
	getenv := func(k string) string { return env[k] }
	if _, _, err := hookBudget(getenv, 0, 0); err == nil || err.Error() != "NOVA_CI_CPU_MILLI=abc wants an integer" {
		t.Fatalf("hookBudget = %v; want the variable named", err)
	}
	env = map[string]string{"NOVA_CI_CPU_MILLI": "1500", "NOVA_CI_MEM_MB": "lots"}
	if _, _, err := hookBudget(getenv, 0, 0); err == nil || err.Error() != "NOVA_CI_MEM_MB=lots wants an integer" {
		t.Fatalf("hookBudget = %v; want the variable named", err)
	}
	env = map[string]string{"NOVA_CI_CPU_MILLI": "1500"}
	if cpu, mem, err := hookBudget(getenv, 0, 0); err != nil || cpu != 1500 || mem != 4096 {
		t.Fatalf("hookBudget = %d %d %v; want 1500 and the 4096 default", cpu, mem, err)
	}
	if cpu, mem, err := hookBudget(getenv, 3000, 8192); err != nil || cpu != 3000 || mem != 8192 {
		t.Fatalf("hookBudget = %d %d %v; want the flags", cpu, mem, err)
	}
}
