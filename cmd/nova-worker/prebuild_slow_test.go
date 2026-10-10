//go:build slow || functional

package main

// prebuild compiles the binaries the slow and functional tiers run, once, before any test,
// so the compile is charged to the package and never to whichever test asks first.
func prebuild() error { return buildShared() }
