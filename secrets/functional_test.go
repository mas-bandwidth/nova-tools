//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestARealStore runs the built binary against real sops, age-keygen and git, on a
// store and keys made in the test's own directory: seat add, a first set, exec with
// its exit status passed through, and the refusals.
func TestARealStore(t *testing.T) {
	t.Parallel()
	sops, err := exec.LookPath("sops")
	if err != nil {
		t.Skip("sops not on PATH")
	}
	ageKeygen, err := exec.LookPath("age-keygen")
	if err != nil {
		t.Skip("age-keygen not on PATH")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "nova-secrets")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	keys, store := filepath.Join(dir, "keys"), filepath.Join(dir, "store")
	require.NoError(t, os.Mkdir(keys, 0o700))
	require.NoError(t, os.Mkdir(store, 0o755))
	env := append(os.Environ(), "HOME="+dir, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(stdin string, want int, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env, cmd.Stdin = env, strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			require.NoError(t, err)
		}
		require.Equal(t, want, code, "%v: %s", args, out)
		return string(out)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", store}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	keygen := func(name string) string {
		key := filepath.Join(keys, name+".key")
		out, err := exec.Command(ageKeygen, "-o", key).CombinedOutput()
		require.NoError(t, err, "age-keygen -o: %s", out)
		pub, err := exec.Command(ageKeygen, "-y", key).Output()
		require.NoError(t, err)
		return strings.TrimSpace(string(pub))
	}
	keygen("recovery")
	recPub, err := exec.Command(ageKeygen, "-y", filepath.Join(keys, "recovery.key")).Output()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(store, "recovery.pub"), recPub, 0o644))
	ada := filepath.Join(keys, "ada.key")
	adaPub := keygen("ada")
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules:\n"), 0o644))
	run("", 0, "seat", "add", "ada", adaPub, "--store", store)
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-qm", "a new store")

	seat := []string{"--store", store, "--as", "ada", "--key", ada, "--sops", sops}
	run("value-one-xyz\n", 0, append([]string{"set", "GH_TOKEN"}, seat...)...)
	run("value-two-xyz\n", 0, append([]string{"set", "API_KEY"}, seat...)...)
	sealedFile, err := os.ReadFile(filepath.Join(store, "ada.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(sealedFile), "value-one-xyz")
	assert.Contains(t, string(sealedFile), "ENC[")

	got := run("", 0, append(append([]string{"exec"}, seat...), "--only", "GH_TOKEN", "--require=GH_TOKEN", "--",
		"sh", "-c", `test "$GH_TOKEN" = value-one-xyz && test -z "$API_KEY" && echo delivered`)...)
	assert.Contains(t, got, "delivered")
	assert.NotContains(t, got, "value-one-xyz")
	run("", 7, append(append([]string{"exec"}, seat...), "--only", "API_KEY", "--", "sh", "-c", "exit 7")...)
	assert.Contains(t, run("", 125, append(append([]string{"exec"}, seat...), "--only", "NOPE", "--", "true")...), "unknown secret name")

	require.NoError(t, os.Chmod(ada, 0o644))
	run("", 125, append(append([]string{"exec"}, seat...), "--only", "API_KEY", "--", "true")...)
}
