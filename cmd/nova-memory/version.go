// nova-memory version: which build is running.
//
// A refusal, a green or a line somebody pastes into a note is evidence about a BUILD, and
// until this verb existed this binary could not say which one it was: `nova-memory version`
// was exit 2, unknown subcommand. So "we are all running the same nova-memory" was a belief
// rather than a reading, and the release could not assert over the set what no member of
// the set would answer.
//
// The version is NOT a constant maintained by hand -- a hand-maintained constant is wrong
// exactly at the commit after the release, where it still names the release. It is read
// from the build itself by internal/buildinfo, which holds the resolution order and the
// shape of the line for every binary here, so that eleven tools answer this question in
// one spelling rather than eleven.
package main

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var, and it is package-level and unexported for the same reason.
// The verb itself is the skeleton's (internal/tool): buildinfo.Line over this stamp.
var version string
