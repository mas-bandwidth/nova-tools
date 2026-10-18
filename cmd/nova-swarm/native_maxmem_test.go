package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNativeSandboxArgvPassesAMemberMemoryCap: a member that names --max-mem hands it
// to the wall as the wall's own flag, so the child's tree is capped where the member
// said rather than at the wall's sized default.
func TestNativeSandboxArgvPassesAMemberMemoryCap(t *testing.T) {
	t.Parallel()

	cfg := nativeRunConfig{slotDir: "/s", benchHome: t.TempDir(), benchOS: "linux", noSharedCaches: true, maxMem: "12g"}
	argv := nativeSandboxArgv([]string{"/bin/true"}, cfg, "/s/data", "/s/jobs/l", "/s/tmp/l")
	assert.True(t, hasFlagPair(argv, "--max-mem", "12g"), "the wall argv does not carry --max-mem 12g:\n%s", strings.Join(argv, " "))
}

// TestNativeSandboxArgvPassesNoMemoryCapByDefault: absent the flag, nothing is passed and
// the wall's sized default stands -- the argv is what it was before the flag existed.
func TestNativeSandboxArgvPassesNoMemoryCapByDefault(t *testing.T) {
	t.Parallel()

	cfg := nativeRunConfig{slotDir: "/s", benchHome: t.TempDir(), benchOS: "linux", noSharedCaches: true}
	argv := nativeSandboxArgv([]string{"/bin/true"}, cfg, "/s/data", "/s/jobs/l", "/s/tmp/l")
	for _, a := range argv {
		assert.NotEqual(t, "--max-mem", a, "a run that named no memory cap passes the wall a --max-mem anyway:\n%s", strings.Join(argv, " "))
	}
}

// TestNativeRefusesABadMaxMem: a value the wall would refuse is refused by native before
// the child starts, one REFUSED line naming the flag.
func TestNativeRefusesABadMaxMem(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0", "0.1", "999999999999999999999999t", "twelve", "12q"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{maxMem: value}, &errOut)
			require.Equal(t, 2, code, "a bad --max-mem exits 2, got %d:\n%s", code, errOut.String())
			got := strings.TrimSpace(errOut.String())
			require.Contains(t, got, "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", got)
			require.Contains(t, got, "--max-mem", "the refusal names the flag, got:\n%s", got)
			require.Equal(t, 1, strings.Count(got, "\n")+1, "exactly one REFUSED line, got:\n%s", got)
		})
	}
}
