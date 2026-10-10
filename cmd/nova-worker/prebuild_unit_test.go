//go:build !slow && !functional

package main

// prebuild is a no-op in the unit tier: no unit test starts a built binary (those tests sit
// behind `slow`, see the ledger's header in internal/ci/slow-tests_allowlist.txt), so the
// package's tests answer without the seven-binary compile.
func prebuild() error { return nil }
