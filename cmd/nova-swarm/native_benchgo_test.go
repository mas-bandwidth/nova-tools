package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// TestNativeRunHandsTheChildTheBenchGo holds the delivery leg of PR 5093 that
// TestTheChildEnvResolvesTheBenchGo leaves open (Zhi's cold read: that test hands
// nativeChildEnv its goBin by hand, so removing the production call
// swarm.BenchGoBin(benchHome(cfg), os.Getenv("PATH")) left it green). This one runs
// nativeRun itself against a fake bench home whose sdk Go is on no PATH entry, and reads
// the PATH the child was really handed from the run's native-argv.log: the sdk's GOROOT/bin
// is on it. RED WITHOUT THE CALL: the child's PATH is the member's own and names no sdk.
func TestNativeRunHandsTheChildTheBenchGo(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the bench layout is a link into the sdk tree")
	}
	bin := nativeHarness(t)
	home := t.TempDir()
	sdkBin := filepath.Join(home, "sdk", "go1.26.6", "bin")
	require.NoError(t, os.MkdirAll(sdkBin, 0o755))
	for _, tool := range []string{"go", "gofmt"} {
		require.NoError(t, testbin.WriteExecutable(filepath.Join(sdkBin, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(home, "sdk", "bin"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(sdkBin, "go"), filepath.Join(home, "sdk", "bin", "go")))
	want, err := filepath.EvalSymlinks(sdkBin)
	require.NoError(t, err)
	require.NotContains(t, filepath.SplitList(os.Getenv("PATH")), want, "the test's own PATH must not name the fake sdk")

	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "benchgo-lbl", benchHome: home,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	raw, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
	require.NoError(t, err, "the run recorded no native-argv.log")
	path := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "env: PATH="); ok {
			path = v
		}
	}
	require.NotEmpty(t, path, "the child was handed no PATH:\n%s", raw)
	assert.Contains(t, filepath.SplitList(path), want, "the child's PATH %q does not carry the bench's sdk Go %s", path, want)
}
