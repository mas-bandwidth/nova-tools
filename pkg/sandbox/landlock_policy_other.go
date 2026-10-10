//go:build !linux

// The landlock ruleset text is linux's alone: only that body builds one.
package sandbox

import "fmt"

// LandlockPolicyText never runs on this platform — the `policy` verb prints
// this platform's own generated policy instead — and it exists here so that the
// verb's backend branch compiles on every build.
func LandlockPolicyText(_ *Policy) (string, error) {
	return "", fmt.Errorf("the landlock ruleset is linux's; this platform's backend is %s", Backend)
}
