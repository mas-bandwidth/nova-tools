package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreReadsIgnoreTheRepositoryTheCallerNames pins that nova-secrets reads the store
// at --store when it is started by a git that exported its own repository (a git runs a
// credential helper with GIT_DIR set, and the helper runs `nova-secrets exec`). The store
// is packed so the pure-Go loose read fails and git ls-tree answers; with GIT_DIR left in
// that git's environment the answer came from the other repository and exec refused
// "failed to read HEAD tree". Each row exports one repository-location variable pointing
// at a repository that is not the store.
func TestStoreReadsIgnoreTheRepositoryTheCallerNames(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)
	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	sopsCfg := fmt.Sprintf("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: %s,%s\n", keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_testtoken123\n")
	commitAndPush(t, storeDir)
	runCmd(t, storeDir, "git", "repack", "-a", "-d", "-q")
	runCmd(t, storeDir, "git", "prune-packed", "-q")

	decoy := filepath.Join(td, "decoy.git")
	runCmd(t, "", "git", "init", "--bare", "-q", "-b", "main", decoy)
	decoyTree := filepath.Join(td, "decoy-tree")
	_ = os.MkdirAll(decoyTree, 0755)

	rows := []struct {
		name string
		env  []string
	}{
		{"GIT_DIR", []string{"GIT_DIR=" + decoy}},
		{"GIT_DIR and GIT_WORK_TREE", []string{"GIT_DIR=" + decoy, "GIT_WORK_TREE=" + decoyTree}},
		{"GIT_OBJECT_DIRECTORY", []string{"GIT_OBJECT_DIRECTORY=" + filepath.Join(decoy, "objects")}},
		{"GIT_WORK_TREE", []string{"GIT_WORK_TREE=" + decoyTree}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			run := func(args ...string) (string, string, error) {
				cmd := exec.Command(bin, args...)
				cmd.Env = append(os.Environ(), r.env...)
				var o, e bytes.Buffer
				cmd.Stdout, cmd.Stderr = &o, &e
				err := cmd.Run()
				return o.String(), e.String(), err
			}
			_, errOut, err := run("names", "--store", storeDir, "--as", "rowan")
			require.NoError(t, err, "names with %s exported: %s", r.name, errOut)
			out, errOut, err := run("exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
				"--only", "GH_TOKEN", "--", "env")
			require.NoError(t, err, "exec with %s exported: %s", r.name, errOut)
			assert.Contains(t, out, "GH_TOKEN=ghp_testtoken123", "exec with %s exported must hand the store's token to the command", r.name)
		})
	}
}
