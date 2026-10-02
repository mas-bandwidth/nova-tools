// nova-tokens version: which build wrote a day file.
//
// A day file carries the build id of the fold that wrote it (the tool stamps, never a
// person), so the first question after two day files disagree is which build wrote each.
// The same holds for a bus note, whose subject carries the build id of the report that
// wrote it: "we are all on the same version" is a reading, never a belief.
//
// The version is not a constant maintained by hand. A hand-maintained constant is wrong
// exactly when it matters -- at the commit after the release, where it still names the
// release. It is read from the build itself, in this order:
//
//	-ldflags "-X main.version=..."  what the release workflow stamps: the tag, exactly
//	the module version              what `go install ...@v1.2.3` records for itself
//	the vcs stamp                   <utc build time>-<12 hex of the revision>[-dirty]
//	devel                           a build with none of the above, SAYING it has none
//
// The middle two come from debug.ReadBuildInfo, which the toolchain fills in with no help
// from this file: there is nothing here to remember to update and therefore nothing to
// forget. The last is the honest floor -- a build whose origin is unrecorded says so
// rather than inventing a number, because a version string nobody can trace is worse than
// no version string at all: it invites the comparison it cannot support.
//
// One line, four tokens. Every field goes through oneline.Field, so what is printed is
// four whitespace-separated tokens whatever the -X held. A version stamped with a newline
// or a space in it would otherwise make the one line that says which build is running say
// two things, or say a build time as if it were an architecture -- and the -X value is the
// one field here that comes from outside the toolchain.
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
