package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// A build with no stamp at all still refuses rather than accepting anything: an
// empty tool version is not a wildcard.
func TestAnUnstampedVersionIsNotAWildcard(t *testing.T) {
	t.Parallel()

	if wake.AcceptBus("", "v0.12.0") {
		t.Error("a nova-wake that cannot say what it is accepted a nova-bus anyway")
	}
	// `devel` == `devel` is a DEVELOPER'S WILDCARD, not a same-tree proof: a
	// `go run` nova-wake accepts a `go run` nova-bus from any checkout of any
	// fork, because neither half has a stamp or a module version to be equal
	// by. It is dev-only -- no released binary ever says `devel`, since the
	// release workflow stamps every binary in cmd/ -- and the alternative,
	// refusing every unstamped pair, would make `go run` unusable against a bus
	// built the same way. Blessed here so that the wildcard is written down
	// rather than found.
	if !wake.AcceptBus("devel", "devel") {
		t.Error("two halves of one unstamped tree must still be a pair")
	}
	if wake.AcceptBus("v0.12.0", "v0.12.1") {
		t.Error("a nova-bus from another release was accepted")
	}
}

// stampRelease makes this test binary answer the way a released one does: the
// release stamps -ldflags "-X main.version=<tag>" and buildVersion reads it.
func stampRelease(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}
