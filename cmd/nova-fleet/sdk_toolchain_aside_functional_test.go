//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sdk_toolchain_aside_functional_test.go runs aside.yml's scripts and link.yml's SHARE-HELD move on temp trees, under
// /bin/sh, with the role's variables put in as the play quotes them or passes them as arguments.

func asideScriptVar(t *testing.T, file, task, name string) string {
	t.Helper()
	v, _ := runnerTask(t, taskFileOf(t, sdkToolchainRole+"/tasks/"+file), task)["vars"].(map[string]any)
	sh := asText(v[name])
	require.NotEmpty(t, sh)
	require.NotContains(t, sh, "{{", "the script takes its values as arguments")
	return sh
}

func runShArgs(t *testing.T, sh string, args ...string) ([]string, error) {
	t.Helper()
	out, err := exec.Command("/bin/sh", append([]string{"-c", sh, "sh"}, args...)...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, err
	}
	return strings.Split(text, "\n"), err
}

func TestFunctionalSDKAsideNamesHandEntriesAndThePlaysOwn(t *testing.T) {
	t.Parallel()
	sdk := filepath.Join(t.TempDir(), "sdk")
	makeTree(t, sdk,
		[]string{"bin", "dl", "go1.26.10/bin", "rustup-1.29.1", "cargo-fuzz-0.13.1", "playwright-1.64.1", "go1.26.6", "ruby-4.0.9.broken-20261126T021123Z"},
		[]string{"env.sh", "env.sh.123.2026-11-10@03:56:50~", "playwright.env", ".hidden"},
		map[string]string{"llvm": "llvm-22.1.8", "jazzerjs": "/home/nova/sdk/jazzerjs-4.0.0"})
	sh := sdkToolchainScript(t, "aside.yml", "what the shared SDK holds that no pin names", map[string]string{
		"{{ (' ' ~ (sdk_managed | join(' ')) ~ ' ') | quote }}": " bin dl env.sh dotnet-home go1.26.10 llvm rustup-1.29.1 cargo-fuzz-0.13.1 ",
		"{{ sdk_dir | quote }}":                                 sdk,
	})
	got := runScript(t, sh)
	assert.ElementsMatch(t, []string{
		"HAND playwright-1.64.1", "HAND go1.26.6", "HAND playwright.env", "HAND .hidden", "HAND jazzerjs",
		"OWN ruby-4.0.9.broken-20261126T021123Z", "OWN env.sh.123.2026-11-10@03:56:50~",
	}, got, "a dangling link is named; a managed short name (llvm) is not")
}

func TestFunctionalSDKAsideMovesEachEntryAndNeverOverwrites(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the script reads GNU stat; the shared SDK is Linux only")
	}
	tmp := t.TempDir()
	sdk, aside := filepath.Join(tmp, "sdk"), filepath.Join(tmp, "aside")
	makeTree(t, sdk, []string{"kani-0.63.0/bin", "go1.26.10"}, []string{"kani-0.63.0/bin/kani", "playwright.env"},
		map[string]string{"playwright": "/home/nova/sdk/playwright-1.64.1"})
	sh := asideScriptVar(t, "aside.yml", "each entry is moved aside under its own name", "sdk_aside_script")
	got, err := runShArgs(t, sh, sdk, aside, "kani-0.63.0", "playwright.env", "playwright")
	require.NoError(t, err, "%v", got)
	assert.Equal(t, []string{"MOVED kani-0.63.0", "MOVED playwright.env", "MOVED playwright"}, got)
	assert.FileExists(t, filepath.Join(aside, "kani-0.63.0/bin/kani"))
	l, err := os.Readlink(filepath.Join(aside, "playwright"))
	require.NoError(t, err)
	assert.Equal(t, "/home/nova/sdk/playwright-1.64.1", l, "a link is moved as a link")
	assert.DirExists(t, filepath.Join(sdk, "go1.26.10"), "what was not named stays")
	st, err := os.Stat(aside)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), st.Mode().Perm())

	// something already there: nothing at all is moved
	makeTree(t, sdk, []string{"kani-0.63.0", "zig-0.17.1"}, nil, nil)
	got, err = runShArgs(t, sh, sdk, aside, "zig-0.17.1", "kani-0.63.0")
	require.Error(t, err)
	assert.Contains(t, strings.Join(got, "\n"), "is in the way")
	assert.DirExists(t, filepath.Join(sdk, "zig-0.17.1"))
	assert.NoDirExists(t, filepath.Join(aside, "zig-0.17.1"))
}

func TestFunctionalSDKShareHeldMoveKeepsEachPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	own, aside := filepath.Join(tmp, "sdk"), filepath.Join(tmp, "aside")
	makeTree(t, own, []string{"rustup-1.29.1/toolchains/stable/lib", "opam/default/lib/extra", "go1.26.6/bin"}, []string{"go1.26.6/bin/go"}, nil)
	sh := asideScriptVar(t, "link.yml", "what the old SDK holds that the shared one lacks is moved aside (nova_sdk_aside_dir)", "sdk_share_aside_script")
	got, err := runShArgs(t, sh, own, aside, "rustup-1.29.1/toolchains/stable", "opam/default/lib/extra", "go1.26.6")
	require.NoError(t, err, "%v", got)
	assert.Len(t, got, 3)
	assert.DirExists(t, filepath.Join(aside, "rustup-1.29.1/toolchains/stable/lib"))
	assert.DirExists(t, filepath.Join(aside, "opam/default/lib/extra"))
	assert.FileExists(t, filepath.Join(aside, "go1.26.6/bin/go"))
	assert.DirExists(t, filepath.Join(own, "rustup-1.29.1/toolchains"), "only the named path moves")
	assert.NoDirExists(t, filepath.Join(own, "rustup-1.29.1/toolchains/stable"))
}
