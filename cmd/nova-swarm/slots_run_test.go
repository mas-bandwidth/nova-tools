package main

import (
	"os"
	"path/filepath"

	"testing"

	"github.com/stretchr/testify/require"
)

// The retired bench slot store's shape, for the tests that still hand native the
// --slots-store and --owner it accepts and reads nothing of: a directory holding
// shares.tsv and slots/, one directory per lease. The slots verb and the library that
// kept the store are gone; native never touched the store after nova-tools#3877.

// slotShares writes a shares.tsv for these tests and returns the store directory.
func slotShares(t *testing.T, body string) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "slots-store")
	require.NoError(t, os.MkdirAll(store, 0o755))
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

// nativeStore is the one-seat store the native tests pass as `--slots-store nativeStore(t)
// --owner fake-1`, flags native accepts and reads nothing of since nova-tools#3877. It is a
// function rather than a fixture so that each test gets a store of its own under its own
// t.TempDir().
func nativeStore(t *testing.T) string {
	t.Helper()
	return slotShares(t, "capacity\t1\nreserve\t0\nfake-1\t1\n")
}
