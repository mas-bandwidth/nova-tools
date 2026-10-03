// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var. The version verb itself is the skeleton's: it prints
// buildinfo.Line and takes no arguments of its own.
package main

// version is the build stamp the skeleton's version verb prints.
var version string
