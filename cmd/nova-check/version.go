package main

// version is empty in every ordinary build and is the one override: a release
// stamps it with -ldflags "-X main.version=<tag>". It is a var rather than a
// const because -X can only write a string var, and it is package-level and
// unexported for the same reason. The version verb is the skeleton's, which
// reads this as Tool.Stamp through internal/buildinfo.
var version string
