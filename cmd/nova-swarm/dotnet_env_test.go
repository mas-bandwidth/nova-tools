package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// TestTheJobEnvGivesDotnetItsHomeInsideTheWall: dotnet's CLI home (its first-use sentinels)
// is the job's data home, inside the wall's write set, never a DOTNET_CLI_HOME the bench
// exported outside it, and the first-run experience, logo and telemetry are off. Each name
// appears exactly once.
func TestTheJobEnvGivesDotnetItsHomeInsideTheWall(t *testing.T) {
	t.Parallel()
	caller := []string{"PATH=/usr/bin", "DOTNET_CLI_HOME=/home/nova/sdk/dotnet-home", "DOTNET_NOLOGO=0"}
	env := nativeChildEnvFrom(caller, "/s/data", "/s/jobs/l", "/s/tmp/l", "", "", "", "", nil)
	want := map[string]string{
		"DOTNET_CLI_HOME":                   "/s/data",
		"DOTNET_SKIP_FIRST_TIME_EXPERIENCE": "1",
		"DOTNET_NOLOGO":                     "1",
		"DOTNET_CLI_TELEMETRY_OPTOUT":       "1",
		"HOME":                              "/s/data",
		"TMPDIR":                            "/s/tmp/l",
	}
	for name, value := range want {
		n := 0
		for _, kv := range env {
			if k, v, _ := strings.Cut(kv, "="); k == name {
				n++
				assert.Equal(t, value, v, "%s", name)
			}
		}
		assert.Equal(t, 1, n, "%s appears %d times in the child env", name, n)
	}
}

// TestTheWallGrantsDotnetsSharedDirAndNoMoreOfTmp: the .NET runtime on Linux takes its named
// mutexes under /tmp/.dotnet/shm whatever TMPDIR says, so the job's wall names that one
// directory as a --write (the dotnet restore died EACCES without it), never /tmp itself; the
// darwin list names nothing; and the job's own dirs stay the first three writes.
func TestTheWallGrantsDotnetsSharedDirAndNoMoreOfTmp(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"/tmp/.dotnet"}, swarm.RuntimeSharedDirs("linux"))
	assert.Empty(t, swarm.RuntimeSharedDirs("darwin"))

	root, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "dn")
	cfg := nativeRunConfig{slotDir: slot, root: root, benchHome: t.TempDir(), benchOS: "linux", runtimeShared: swarm.RuntimeSharedDirs("linux")}
	argv := nativeSandboxArgv([]string{"/bin/true"}, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "dn"))
	flags := argv[:slices.Index(argv, "--")]
	var writes []string
	for i, a := range flags {
		if a == "--write" {
			require.Less(t, i+1, len(flags))
			writes = append(writes, flags[i+1])
		}
	}
	assert.Contains(t, writes, "/tmp/.dotnet", "the wall's writes: %v", writes)
	assert.NotContains(t, writes, "/tmp", "the wall must never grant all of /tmp: %v", writes)
	assert.Equal(t, []string{jobDir, filepath.Join(slot, "data"), filepath.Join(slot, "tmp", "dn")}, writes[:3])

	cfg.runtimeShared = nil
	argv = nativeSandboxArgv([]string{"/bin/true"}, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "dn"))
	assert.NotContains(t, argv, "/tmp/.dotnet", "a bench without the directory names no grant the wall would refuse")
}
