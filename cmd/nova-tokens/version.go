// nova-tokens version identifies the build used in day-file and note stamps.
// Resolution order is the release's -ldflags stamp, the module version recorded
// by go install, the VCS build stamp, then devel when no identity was recorded.
// The shared buildinfo renderer escapes fields to keep the result on one line.
package main

import "github.com/mas-bandwidth/nova-tools/internal/buildinfo"

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var, and it is package-level and unexported for the same reason.
// The verb itself is the skeleton's (internal/tool): buildinfo.Line over this stamp.
var version string

// buildVersion is this binary's build identity, the build= on a day file and a summary
// line: internal/buildinfo's one resolution order over the stamp.
func buildVersion() string { return buildinfo.Version(version) }
