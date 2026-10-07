//go:build !darwin && !linux

package main

import (
	"fmt"
	"runtime"
)

// probeNonceOnFD has no body where the sandbox body has none: Run REFUSES on every such
// platform (internal/sandbox/wrap_other.go), so no probe there has a child and the only
// caller of the internal verb is someone typing it — whose refusal this is.
func probeNonceOnFD() ([probeNonceLen]byte, error) {
	var got [probeNonceLen]byte
	return got, fmt.Errorf("this build inherits no probe descriptor on %s", runtime.GOOS)
}
