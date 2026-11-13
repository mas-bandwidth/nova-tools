//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sdk_toolchain_link_functional_test.go runs the two scripts of sdk-toolchain's link.yml that decide whether the
// login's own SDK may go (github_runner_shared_test.go is the structural half): what it holds that the shared one lacks
// (SHARE-HELD), and whether a process runs from it (in-use.yml, SDK-IN-USE). Each runs under /bin/sh on temp trees, with
// the role's variables put in as the play quotes them.

func sdkToolchainScript(t *testing.T, file, task string, vars map[string]string) string {
	t.Helper()
	sh := asText(runnerTask(t, taskFileOf(t, sdkToolchainRole+"/tasks/"+file), task)["ansible.builtin.shell"])
	for k, v := range vars {
		sh = strings.ReplaceAll(sh, k, "'"+v+"'")
	}
	require.NotContains(t, sh, "{{", "every variable of the script is given")
	return sh
}

func runScript(t *testing.T, sh string) []string {
	t.Helper()
	out, err := exec.Command("/bin/sh", "-c", sh).CombinedOutput()
	require.NoError(t, err, "%s", out)
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func makeTree(t *testing.T, root string, dirs, files []string, links map[string]string) {
	t.Helper()
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	for _, f := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, f), nil, 0o644))
	}
	for name, target := range links {
		require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
	}
}

func TestFunctionalSDKShareHeldNamesWhatOnlyTheOldSDKHolds(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	shared, own := filepath.Join(tmp, "shared"), filepath.Join(tmp, "own")
	makeTree(t, shared,
		[]string{"go-1.23/bin", "rustup-1/toolchains/1.80", "rustup-1/cargo/bin", "opam/default/lib/zarith", "opam/default/bin", "dl"},
		[]string{"go-1.23/bin/go", "rustup-1/cargo/bin/cargo"},
		map[string]string{"go": "go-1.23", "lost": "nowhere"})
	makeTree(t, own,
		[]string{"go-1.22/bin", "go-1.23/bin", "rustup-1/toolchains/1.80", "rustup-1/toolchains/stable/lib", "rustup-1/cargo/bin",
			"opam/default/lib/zarith", "opam/default/lib/extra", "opam/default/bin", "dl/old", "handmade/sub", "lost/deeper"},
		[]string{"go-1.22/bin/go", "go-1.22/bin/gofmt", "go-1.23/bin/go", "go-1.23/vet", "rustup-1/cargo/bin/cargo",
			"rustup-1/cargo/bin/cargo-nextest", "opam/default/bin/why3", ".hidden"},
		map[string]string{"go": "go-1.22"})
	sh := sdkToolchainScript(t, "link.yml", "what the login's own SDK holds that the shared one lacks", map[string]string{
		"{{ (nova_shared_dir ~ '/sdk') | quote }}": shared,
		"{{ nova_sdk_dir | quote }}":               own,
	})
	got := runScript(t, sh)
	// each once, at its highest missing level; nothing under dl/, nothing again under the go link; a name the shared SDK
	// holds only as a dangling link is climbed past, so what is below it is named
	assert.ElementsMatch(t, []string{
		"go-1.22", "go-1.23/vet", "handmade", ".hidden", "rustup-1/toolchains/stable",
		"rustup-1/cargo/bin/cargo-nextest", "opam/default/lib/extra", "opam/default/bin/why3", "lost/deeper",
	}, got)

	// an old SDK the shared one covers whole names nothing, and the script ends well with every glob unmatched
	empty := filepath.Join(tmp, "empty")
	require.NoError(t, os.Mkdir(empty, 0o755))
	sh = sdkToolchainScript(t, "link.yml", "what the login's own SDK holds that the shared one lacks", map[string]string{
		"{{ (nova_shared_dir ~ '/sdk') | quote }}": shared,
		"{{ nova_sdk_dir | quote }}":               empty,
	})
	assert.Empty(t, runScript(t, sh))
}

func TestFunctionalSDKInUseFindsAProcessRunningFromIt(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the scan reads /proc; the shared SDK is Linux only")
	}
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	require.NoError(t, os.MkdirAll(filepath.Join(real, "home", "sdk", "bin"), 0o755))
	// the home is reached through a link, as under a linked /home: the kernel names the resolved path
	require.NoError(t, os.Symlink(filepath.Join(real, "home"), filepath.Join(tmp, "home")))
	dir := filepath.Join(tmp, "home", "sdk")
	scan := func() []string {
		return runScript(t, sdkToolchainScript(t, "in-use.yml", "no process runs from {{ sdk_in_use_dir }}",
			map[string]string{"{{ sdk_in_use_dir | quote }}": dir}))
	}
	assert.Empty(t, scan(), "nothing runs from it yet")

	cmd := exec.Command("sleep", "60")
	cmd.Dir = filepath.Join(dir, "bin")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	got := scan()
	require.Len(t, got, 1, "%v", got)
	assert.True(t, strings.HasPrefix(got[0], strconv.Itoa(cmd.Process.Pid)+":sleep 60"), got[0])

	// a link in place of the directory is not the SDK: nothing to find
	assert.Empty(t, runScript(t, sdkToolchainScript(t, "in-use.yml", "no process runs from {{ sdk_in_use_dir }}",
		map[string]string{"{{ sdk_in_use_dir | quote }}": filepath.Join(tmp, "home")})))
}
