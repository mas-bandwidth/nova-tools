package main

import (
	"os"
	"path/filepath"

	"testing"
)

// RED TESTS FOR #880 ITEM 18: `nova-swarm run` HOLDS A BENCH SLOT LEASE PER TASK.
//
// The slots verbs already exist (docs/SPEC-SWARM.md, "Bench slot leases"): a store
// with shares.tsv, take/release/list, expiry fenced by live pids. What is missing is
// the launcher's side of the contract -- `run --slots-store <dir> --owner <name>`
// takes one lease before each task starts and releases it when the task ends. A
// refused take is a wait, never a launch past the share. These tests drive the real
// binary against the fake harness on a temp store; nothing reaches the network.

// slotShares writes a shares.tsv for these tests and returns the store directory.
func slotShares(t *testing.T, body string) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "slots-store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(store, "shares.tsv"), body)
	return store
}

// slotLeaseCount counts the lease directories the store holds right now.
func slotLeaseCount(t *testing.T, store string) int {
	t.Helper()
	return slotLeaseCountNow(store)
}

func slotLeaseCountNow(store string) int {
	entries, err := os.ReadDir(filepath.Join(store, "slots"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// nativeStore is the one-seat bench slot store a `native` launch now needs: since
// nova-tools#1546 a launch without a lease is REFUSED, so every test in this package that
// runs native passes `--slots-store nativeStore(t) --owner fake-1`. It is a function
// rather than a fixture so that each test gets a store of its own under its own
// t.TempDir(), and two tests running in parallel never contend for the same seat.
func nativeStore(t *testing.T) string {
	t.Helper()
	return slotShares(t, "capacity\t1\nreserve\t0\nfake-1\t1\n")
}
