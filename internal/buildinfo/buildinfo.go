// Package buildinfo answers one question in one place: WHICH BUILD IS THIS.
//
// Every binary in this repo can be running a different build at the same moment -- a
// release binary somebody downloaded, a `go install` from a tag, a working tree with
// three uncommitted edits -- and the first question after any of them misbehaves is
// which. Five binaries answered it with five copies of the same forty lines, and a
// copied answer is an answer that drifts: cmd/nova-wake's copy had already lost the
// build time and the dirty marker, so two lines comparing a `nova-bus version` against a
// `nova-wake version` were comparing two different spellings of the same fact.
//
// So the resolution order lives here, once, and the one line every `version` verb prints
// is built here too. The order:
//
//	-ldflags "-X main.version=..."  what the release workflow stamps: the tag, exactly
//	the module version              what `go install ...@v1.2.3` records for itself
//	the vcs stamp                   <utc build time>-<12 hex of the revision>[-dirty]
//	devel                           a build with none of the above, SAYING it has none
//
// The middle two come from debug.ReadBuildInfo, which the toolchain fills in with no help
// from any file here: there is nothing to remember to update and therefore nothing to
// forget. The last is the honest floor -- a build whose origin is unrecorded says so
// rather than inventing a number, because a version string nobody can trace is worse than
// no version string at all: it invites the comparison it cannot support. NOTHING HERE
// EVER INVENTS A DOTTED NUMBER.
//
// ONE LINE, FOUR TOKENS. Every field of Line goes through oneline.Field, so what is
// printed is four whitespace-separated tokens whatever the -X held -- and the -X value is
// the one field in the whole line that comes from outside the toolchain.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Unknown is what a build with no stamp and no vcs information reports. It is the same
// spelling the Go toolchain uses for an uninstalled build, so it reads as familiar rather
// than as a bug in the tool printing it.
const Unknown = "devel"

// shortRevisionLen is 12 hex digits, the length a Go pseudo-version uses, rather than
// git's default 7: a 7-digit prefix already collides in repositories this size, and a
// version that two commits can share is not an answer to which build is running.
const shortRevisionLen = 12

// Version is Resolve over the calling binary's own build information. The stamped
// argument is the caller's `var version string`, which only a release's
// -ldflags "-X main.version=<tag>" ever writes: -X can only reach a string var in the
// main package, so the var stays there and the reading of it happens here.
func Version(stamped string) string {
	info, ok := debug.ReadBuildInfo()
	return Resolve(stamped, info, ok)
}

// Resolve takes the stamp and the build information as ARGUMENTS rather than reading
// them, so that the order above is testable: a test cannot install itself from a module
// proxy or rebuild itself from a dirty tree, and an order asserted only by the build it
// happens to run under is asserted by one case out of four.
func Resolve(stamped string, info *debug.BuildInfo, ok bool) string {
	if v := strings.TrimSpace(stamped); v != "" {
		return v
	}
	if !ok || info == nil {
		return Unknown
	}
	// "(devel)" is what the toolchain records for a build that is not an installed
	// module version, and it carries no more information than the floor does -- so it
	// falls through to the vcs stamp below, which carries a great deal more.
	if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" && v != Unknown {
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
		return Unknown
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
		// A build from an edited tree is NOT the commit it names, and the whole point
		// of a version verb is that two lines can compare what they are running.
		v += "-dirty"
	}
	return v
}

// Line is the one line a `version` verb prints, without its newline:
//
//	<tool> <build identity> <goos>/<goarch> <go version>
//
// Four tokens, in this order, from every binary in the set -- so a release assertion, or
// a person holding two pastes, reads the identity out of field two and never has to know
// which tool wrote the line. Every field is rendered through oneline.Field, including the
// tool's own name, because a helper that escapes three of four fields is a helper whose
// guarantee has to be re-checked at every call site.
func Line(tool, stamped string) string {
	return fmt.Sprintf("%s %s %s/%s %s",
		oneline.Field(tool),
		oneline.Field(Version(stamped)),
		oneline.Field(runtime.GOOS),
		oneline.Field(runtime.GOARCH),
		oneline.Field(runtime.Version()))
}
