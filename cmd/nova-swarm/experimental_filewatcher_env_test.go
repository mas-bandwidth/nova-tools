package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIssue2328 asserts that OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER survives
// nativeChildEnv's allowlist, so a darwin launcher can turn off opencode's FSEvents
// watcher before the wall denies the harness its startup read of /var/db/xcode_select_link
// (nova-tools #2328). The control is keepNativeEnv: without OPENCODE_*, the variable is
// dropped.
func TestIssue2328(t *testing.T) {
	t.Parallel()
	const name = "OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER"
	// the per-test seam: nativeChildEnvFrom takes the environment to build from, so the
	// variable is named in the list rather than set on the whole process.
	env := nativeChildEnvFrom([]string{name + "=1"}, "data", "job", "tmp", "", "", "", "", nil)
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			return
		}
	}
	require.FailNowf(t, "", "%s was dropped from the child's environment", name)
}
