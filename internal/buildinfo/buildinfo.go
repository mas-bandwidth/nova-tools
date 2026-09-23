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
//	the vcs stamp                   <utc revision time>-<12 hex of the revision>[-dirty]
//	devel                           a build with none of the above, SAYING it has none
//
// The middle two come from debug.ReadBuildInfo, which the toolchain fills in with no help
// from any file here: there is nothing to remember to update and therefore nothing to
// forget. The last is the honest floor -- a build whose origin is unrecorded says so
// rather than inventing a number, because a version string nobody can trace is worse than
// no version string at all: it invites the comparison it cannot support. NOTHING HERE
// EVER INVENTS A DOTTED NUMBER.
//
// ONE LINE, FOUR TOKENS AND THEN NAMED EXTRAS. Every field of Line goes through
// oneline.Field, so what is printed is whitespace-separated tokens whatever the -X held
// -- and the -X value is the one field in the whole line that comes from outside the
// toolchain. A tool with more to say says it as `key=value` after the fourth token, and
// Parse, here, is the one reader of the whole shape: writer and reader are one pair, so a
// tool that adds a fact cannot break a consumer that has never heard of it (#1297).
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
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
	// of different revisions carry the commit timestamp. Rebuilding one revision
	// does not change vcs.time; this is not the compilation time.
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
//
// A tool with one more true thing to say about itself says it as an EXTRA: a `key=value`
// token after the fourth -- `build=<12 hex>` from nova-merge, `backend=` and `platform=`
// from nova-sandbox. Extras are part of the grammar rather than exceptions to it. On
// 2026-09-18 nova-merge's hand-rolled fifth token made `nova-version snapshot` refuse an
// entire install (#1297), because each reader had been written against the four tokens it
// happened to know. So the writer takes extras HERE, where Parse is guaranteed to read
// them back, and REFUSES one that is not key=value: a writer looser than its reader is a
// refusal deferred to whoever runs the snapshot.
func Line(tool, stamped string, extras ...string) string {
	line := fmt.Sprintf("%s %s %s/%s %s",
		oneline.Field(tool),
		oneline.Field(Version(stamped)),
		oneline.Field(runtime.GOOS),
		oneline.Field(runtime.GOARCH),
		oneline.Field(runtime.Version()))
	for _, e := range extras {
		key, value, found := strings.Cut(e, "=")
		if !found || key == "" || value == "" {
			panic("buildinfo.Line: extra " + strconv.Quote(e) + " is not key=value; the extras of a version line are named facts, never loose tokens")
		}
		line += " " + oneline.Field(key) + "=" + oneline.Field(value)
	}
	return line
}

// Fields is one version line taken apart. It is what every READER of a version line in
// this tree gets, so that "what a version line is" is answered in one place rather than
// once per consumer: nova-version's snapshot and its report both ask Parse, and a tool
// that adds an extra tomorrow is already legible to both.
type Fields struct {
	Tool      string   // field one: the binary's own name for itself
	Version   string   // field two: the build identity, the one field a comparison reads
	Platform  string   // field three: <goos>/<goarch>
	GoVersion string   // field four: the toolchain that built it
	Extras    []string // every key=value token after the fourth, in the order printed
}

// Extra is the value of one named extra. A reader asks for the fact it wants BY NAME and
// never holds a position, so a tool that adds a second extra cannot move the first.
func (f Fields) Extra(key string) (string, bool) {
	for _, e := range f.Extras {
		if k, v, found := strings.Cut(e, "="); found && k == key {
			return v, true
		}
	}
	return "", false
}

// Parse reads the first line of what a `version` verb printed and reports whether it is a
// version line at all. The grammar, stated once for the whole tree:
//
//	<tool> <build identity> <goos>/<goarch> <go version> [key=value ...]
//
// It is deliberately strict about the four mandatory tokens -- a caller uses ok to tell
// "this binary is broken" from "this binary is old", and a parser that accepts a usage
// refusal can tell neither -- and deliberately open about what follows, because the hurt
// it exists to end was a reader taking one tool's extra fact for a broken build.
func Parse(s string) (Fields, bool) {
	line, _, _ := strings.Cut(s, "\n")
	tokens := strings.Fields(strings.TrimSuffix(line, "\r"))
	if len(tokens) < 4 {
		return Fields{}, false
	}
	goos, goarch, found := strings.Cut(tokens[2], "/")
	if !found || goos == "" || goarch == "" {
		return Fields{}, false
	}
	f := Fields{Tool: tokens[0], Version: tokens[1], Platform: tokens[2], GoVersion: tokens[3]}
	for _, e := range tokens[4:] {
		key, value, found := strings.Cut(e, "=")
		if !found || key == "" || value == "" {
			return Fields{}, false
		}
		f.Extras = append(f.Extras, e)
	}
	return f, true
}

// Source is the structured view of WHERE a binary was built from. The version line has
// always carried an unstructured "the stamp this build reports"; Source is what the stamp
// is verified against: every stamp read at the gate -- apply --sha's postflight, the
// snapshot, `moved`'s per-revision readback -- also reads this four-field shape and
// refuses a binary that names a different checkout, a different revision, a dirty tree,
// or a different build host than the manifest recorded. A build from the wrong
// repository that happens to carry the requested linker stamp cannot pass (#2291,
// SPEC-VERSION item 6).
//
// All four fields are always written together, so the round-trip is unambiguous: a Source
// the reader can extract is one the writer wrote whole. A version line that carries some
// but not all of the four is read as "no source": a half-present source is a source the
// reader cannot verify, and a silent disagreement is worse than a refusal.
type Source struct {
	// Repository is the checkout the build came from, e.g. "github.com/owner/repo". A
	// build from a different repository cannot pass even if the linker stamp matches.
	Repository string
	// Revision is the full SHA the build was cut at, or its 12-hex prefix. Two builds
	// of two adjacent commits already disagree on this field, and a binary that names
	// the wrong revision is a different build.
	Revision string
	// Dirty is true when the source tree carried uncommitted changes at build time. A
	// release binary whose tree was dirty at the build is not the commit it names, and
	// this field is what a reader verifies it against.
	Dirty bool
	// BuildHost is the hostname the build ran on. Two hosts cutting the same commit
	// produce the same stamp but different artifacts, and this field is what tells
	// them apart.
	BuildHost string
}

// Extras returns the Source as a slice of key=value tokens, the shape Line takes as its
// variadic extras. The four tokens appear in a fixed order -- repo, revision, dirty,
// build_host -- so the writer and the reader cannot disagree about which field is
// which. Dirty is ALWAYS emitted (true OR false), because a Source the reader can
// extract is one the writer wrote whole, and a missing dirty field is a Source the
// reader is forced to refuse (#2291).
func (s Source) Extras() []string {
	return []string{
		"repo=" + s.Repository,
		"revision=" + s.Revision,
		"dirty=" + strconv.FormatBool(s.Dirty),
		"build_host=" + s.BuildHost,
	}
}

// LineWithSource is the version line the build stamps into a binary, with Source
// metadata attached. It is the writer `apply --sha` uses on every binary it builds, so
// the postflight can read source back with FindSource and verify it against the manifest
// the build recorded.
//
// The four mandatory tokens come first, the four source tokens follow in Extras() order,
// and any extras the caller wants to add (a file digest, a backend label) come after.
// Writer and reader are the one pair this package has always been, so a Source round-
// trips through Line and Parse into itself: a tool that adds a fact cannot break a
// consumer that has never heard of it, and a reader that has never seen the source
// metadata reads the four tokens it knows and ignores the rest (#1297).
func LineWithSource(tool, stamped string, src Source, extras ...string) string {
	all := append(src.Extras(), extras...)
	return Line(tool, stamped, all...)
}

// FindSource returns the Source the fields carry, or false. The four source keys are
// the only ones FindSource reads: anything else in the extras -- nova-merge's
// `build=<hex>`, nova-sandbox's `backend=` -- is ignored. A version line that carries
// none of the four is reported with ok=false: old binaries, foreign tools, and a `go
// install` from a tag never had this, and "no Source" is the honest answer. A version
// line that carries SOME but not ALL of the four is ALSO reported with ok=false: a
// partial source is a source the reader cannot verify, and the gate must refuse it
// rather than guess at the missing field (#2291, SPEC-VERSION item 6).
func (f Fields) FindSource() (Source, bool) {
	repo, hasRepo := f.Extra("repo")
	rev, hasRev := f.Extra("revision")
	dirty, hasDirty := f.Extra("dirty")
	host, hasHost := f.Extra("build_host")
	if !hasRepo && !hasRev && !hasDirty && !hasHost {
		return Source{}, false
	}
	if !hasRepo || !hasRev || !hasDirty || !hasHost {
		return Source{}, false
	}
	return Source{
		Repository: repo,
		Revision:   rev,
		Dirty:      dirty == "true",
		BuildHost:  host,
	}, true
}
