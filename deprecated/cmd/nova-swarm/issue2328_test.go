package main

import (
	"strings"
	"testing"
)

// TestIssue2328 asserts that OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER survives
// nativeChildEnv's allowlist, so a darwin launcher can turn off opencode's FSEvents
// watcher before the wall denies the harness its startup read of /var/db/xcode_select_link
// (nova-tools #2328). The control is keepNativeEnv: without OPENCODE_*, the variable is
// dropped.
func TestIssue2328(t *testing.T) {
	const name = "OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER"
	t.Setenv(name, "1")

	env := nativeChildEnv("data", "job", "tmp", "", "", "", "")
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			return
		}
	}
	t.Fatalf("%s was dropped from the child's environment", name)
}
