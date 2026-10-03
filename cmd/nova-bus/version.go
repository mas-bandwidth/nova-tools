// nova-bus version: which build is on the bus.
//
// A bus is several lines running this one tool over one repository, and every failure it
// exists to have closed is a failure of AGREEMENT -- an id scheme, a header, a push
// protocol that two senders have to implement identically. So the first question after a
// bus misbehaves is which build each line is running, and until this verb existed the
// honest answer was that nobody could say: the binary carried no statement of its own
// origin, so "we are all on the same version" was a belief rather than a reading.
//
// The version is NOT a constant maintained by hand. A hand-maintained constant is wrong
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
// ONE LINE, FOUR TOKENS. Every field goes through oneline.Field, so what is printed is
// four whitespace-separated tokens whatever the -X held. A version stamped with a newline
// or a space in it would otherwise make the one line that says which build is running say
// two things, or say a build time as if it were an architecture -- and the -X value is the
// one field here that comes from outside the toolchain.
package main

import (
	"runtime/debug"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var, and it is package-level and unexported for the same reason.
var version string

// resolveVersion is internal/buildinfo's Resolve, which is where the order above now
// lives: five binaries held five copies of it, and a copied answer drifts -- one copy had
// already lost the build time and the dirty marker, so two lines comparing their versions
// were comparing two spellings of the same fact. The order, the floor and the twelve-hex
// revision are one implementation for every binary here; this function stays as the name
// this package's own tests drive, and they now drive the shared one through it.
func resolveVersion(stamped string, info *debug.BuildInfo, ok bool) string {
	return buildinfo.Resolve(stamped, info, ok)
}
