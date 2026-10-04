package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSealDryRunOnAnUncommittedStoreRefusesWithTheRemedy: seal's plan is read at the
// store's HEAD, so `seal -h` and the banner say a committed store is wanted, and a
// staged-but-uncommitted store (git init, the files added, no commit) is refused at
// exit 2 with the one line that names the commit starting it, before any plan and with
// nothing written.
func TestSealDryRunOnAnUncommittedStoreRefusesWithTheRemedy(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fake sops runs through /bin/sh")
	}

	// The help a caller reads before the call promises the store the plan wants.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"the banner", []string{"help"}},
		{"seal -h", []string{"seal", "-h"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr strings.Builder
			require.Equal(t, 0, run(c.args, strings.NewReader(""), &stdout, &stderr), "%v: %s", c.args, stderr.String())
			flat := spaces.ReplaceAllString(stdout.String(), " ")
			assert.Contains(t, flat, "read the store at HEAD", "%s does not say the plan reads the store at HEAD:\n%s", c.name, stdout.String())
			assert.Contains(t, flat, "committed", "%s does not say a committed store is wanted:\n%s", c.name, stdout.String())
		})
	}

	// A staged-but-uncommitted store: git init, the store's files added, no commit.
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	require.NoError(t, os.MkdirAll(store, 0o755))
	initGitStore(t, store)
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"),
		[]byte("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: "+agePub('p')+","+agePub('z')+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(store, "recovery.pub"), []byte(agePub('z')+"\n"), 0o644))
	runCmd(t, store, "git", "add", ".sops.yaml", "recovery.pub")

	keyDir := filepath.Join(dir, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0o700))
	keyPath := filepath.Join(keyDir, "ada.key")
	require.NoError(t, os.WriteFile(keyPath, []byte("AGE-SECRET-KEY-FAKE\n"), 0o600))
	sopsPath := filepath.Join(dir, "fake-sops")
	writeFakeExe(t, sopsPath, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'sops 3.13.3'; exit 0; fi\nexit 0\n")

	var stdout, stderr strings.Builder
	code := run([]string{"seal", "--store", store, "--as", "ada", "--key", keyPath, "--sops", sopsPath, "--name", "TARGET", "--dry-run"},
		strings.NewReader(""), &stdout, &stderr)
	require.Equal(t, 2, code, "seal --dry-run on an uncommitted store: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	assert.Empty(t, stdout.String(), "a refusal prints nothing on stdout")
	got := stderr.String()
	assert.Regexp(t, `^SECRETS SEAL REFUSED: `, got)
	assert.Equal(t, 1, strings.Count(got, "\n"), "one line: %q", got)
	assert.Contains(t, got, "no commit yet", "the refusal does not name the store's missing commit: %s", got)
	assert.Contains(t, got, "run: git -C "+store+" add", "the refusal does not name the commit that starts the store: %s", got)
	assert.Contains(t, got, "commit -m", "the refusal does not name the commit that starts the store: %s", got)
}
