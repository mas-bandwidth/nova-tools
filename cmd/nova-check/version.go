// nova-check version: which build is running. A refusal, a green or a pasted line
// is evidence about a build, so the binary says which one it is. The version is read
// from the build itself by pkg/buildinfo, which holds the resolution order and
// the shape of the line for every tool in this repository, so each answers in one
// spelling; it is never a constant maintained by hand, which would be wrong at the
// first commit after a release.
package main

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var, and it is package-level and unexported for the same reason.
var version string
