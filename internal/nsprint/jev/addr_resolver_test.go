package jev

import "testing"

// TestJevAddrReadsTheResolver (#4399 round 4, the reader's reproduction made
// parallel-safe: the resolver is injected through the env, never by swapping
// the package global): with no --redis the ledger dials the resolver's
// address over its environment, and --redis wins over it.
func TestJevAddrReadsTheResolver(t *testing.T) {
	t.Parallel()
	e := env{
		getenv:  func(k string) string { return map[string]string{"NOVA_REDIS": "127.0.0.1:1"}[k] },
		resolve: func(getenv func(string) string) string { return getenv("NOVA_REDIS") },
	}
	if got := e.addr(""); got != "127.0.0.1:1" {
		t.Fatalf("env.addr(\"\") = %q with the resolver set; want the one resolver's 127.0.0.1:1", got)
	}
	if got := e.addr("127.0.0.1:2"); got != "127.0.0.1:2" {
		t.Fatalf("env.addr(--redis) = %q; want --redis first", got)
	}
	if realEnv.resolve == nil {
		t.Fatal("the real env has no resolver: the ledger would read no environment at all")
	}
}
