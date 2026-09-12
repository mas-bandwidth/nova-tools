package main

import (
	"runtime/debug"
	"strings"
)

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is the same shape cmd/nova-bus uses.
var version string

const unknownVersion = "devel"

// shortRevisionLen is 12 hex digits, the length a Go pseudo-version uses: a 7-digit
// prefix already collides in repositories this size.
const shortRevisionLen = 12

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(version, info, ok)
}

// resolveVersion takes the stamp and the build information as ARGUMENTS rather than
// reading them, so that the order is testable.
func resolveVersion(stamped string, info *debug.BuildInfo, ok bool) string {
	if v := strings.TrimSpace(stamped); v != "" {
		return v
	}
	if !ok || info == nil {
		return unknownVersion
	}
	if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" && v != unknownVersion {
		return v
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			if len(s.Value) > shortRevisionLen {
				return s.Value[:shortRevisionLen]
			}
			return s.Value
		}
	}
	return unknownVersion
}
