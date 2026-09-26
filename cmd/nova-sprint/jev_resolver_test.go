package main

// The reader's reproduction (rowan-opus, cold read of #4399 at 1287e7f00):
// removing jev.go's DefaultAddr line left every test green. This holds the
// eighth door: jev's ledger reads the one resolver, so NOVA_REDIS alone (the
// resolver's third variable, which jev's own fallback never read) names its
// store. Adapted from the reproduction to an injected environment, so the
// test opens with t.Parallel (the class rule) instead of t.Setenv.

import (
	"testing"

	ledger "github.com/mas-bandwidth/nova-tools/internal/nsprint/jev"
)

func TestJevLedgerReadsTheOneResolver(t *testing.T) {
	t.Parallel()
	if ledger.DefaultAddrFrom == nil {
		t.Fatal("jev.DefaultAddrFrom is nil: the jev verb reads its own env, not the one resolver (seat.go)")
	}
	env := map[string]string{"NOVA_REDIS": "127.0.0.1:1"}
	getenv := func(k string) string { return env[k] }
	if got := ledger.DefaultAddrFrom(getenv); got != "127.0.0.1:1" {
		t.Fatalf("jev.DefaultAddrFrom() = %q with NOVA_REDIS alone; want the one resolver's 127.0.0.1:1", got)
	}
	// The ledger's own use of it, end to end (round 4): the address the
	// ledger verbs dial with no --redis, in that environment.
	if got := ledger.AddrFor("", getenv); got != "127.0.0.1:1" {
		t.Fatalf("jev.AddrFor(\"\") = %q with NOVA_REDIS alone; want the ledger to dial the one resolver's 127.0.0.1:1", got)
	}
}
