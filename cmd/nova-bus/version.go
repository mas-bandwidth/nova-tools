// nova-bus version: which build is at the table.
//
// A table is several lines running this one tool over one repository, and every failure it
// exists to have closed is a failure of AGREEMENT -- an id scheme, a header, a push
// protocol that two senders have to implement identically. So the first question after a
// table misbehaves is which build each line is running, and until this verb existed the
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
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var, and it is package-level and unexported for the same reason.
var version string

// unknownVersion is what a build with no stamp and no vcs information reports. It is the
// same spelling the Go toolchain uses for an uninstalled build, so it reads as familiar
// rather than as a bug in this tool.
const unknownVersion = "devel"

// shortRevisionLen is 12 hex digits, the length a Go pseudo-version uses, rather than
// git's default 7: a 7-digit prefix already collides in repositories this size, and a
// version that two commits can share is not an answer to which build is running.
const shortRevisionLen = 12

// buildVersion is resolveVersion over this binary's own build information.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(version, info, ok)
}

// resolveVersion takes the ldflags stamp and the build information as ARGUMENTS rather
// than reading them, so that the order above is testable: a test cannot install itself
// from a module proxy or rebuild itself from a dirty tree, and an order asserted only by
// the build it happens to run under is asserted by one case out of four.
func resolveVersion(stamped string, info *debug.BuildInfo, ok bool) string {
	if v := strings.TrimSpace(stamped); v != "" {
		return v
	}
	if !ok || info == nil {
		return unknownVersion
	}
	// "(devel)" is what the toolchain records for a build that is not an installed
	// module version, and it carries no more information than the floor does -- so it
	// falls through to the vcs stamp below, which carries a great deal more.
	if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" && v != unknownVersion {
		return v
	}
	var revision, stamp string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			stamp = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return unknownVersion
	}
	if len(revision) > shortRevisionLen {
		revision = revision[:shortRevisionLen]
	}
	v := revision
	// Time first, revision second, the way a Go pseudo-version orders them: two builds
	// of the same tree sort by when they were built, which is the comparison a person
	// holding two of these lines actually makes.
	if t, err := time.Parse(time.RFC3339, stamp); err == nil {
		v = t.UTC().Format("20060102150405") + "-" + revision
	}
	if modified {
		// A build from an edited tree is NOT the commit it names, and the whole point of
		// this verb is that two lines can compare what they are running.
		v += "-dirty"
	}
	return v
}

// cmdVersion prints the one line. It takes no flags and no arguments: there is no --short,
// no --json and no --long, because a second output shape is a second thing to agree about
// and this verb exists to end an argument rather than to start one.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "nova-bus version: takes no flags and no arguments, got %d\n", len(args))
		return 2
	}
	fmt.Fprintf(stdout, "nova-bus %s %s/%s %s\n",
		oneline.Field(buildVersion()),
		oneline.Field(runtime.GOOS),
		oneline.Field(runtime.GOARCH),
		oneline.Field(runtime.Version()))
	return 0
}
