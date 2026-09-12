package main

import (
	"runtime/debug"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is the same shape cmd/nova-bus and
// cmd/nova-sandbox use, and it is what the nova-bus pin is derived from: this build's own
// version IS the nova-bus version this build is written against, because one tag ships
// both. A literal here was the 2026-09-12 lockout (see internal/wake/bus.go).
var version string

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(version, info, ok)
}

// resolveVersion delegates to internal/buildinfo.Resolve, which provides the shared
// resolution order, the devel floor and 12-hex revision across binaries in this repository.
func resolveVersion(stamped string, info *debug.BuildInfo, ok bool) string {
	return buildinfo.Resolve(stamped, info, ok)
}
