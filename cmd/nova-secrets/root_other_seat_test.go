package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootExecRejectsModifiedOtherCommittedSeat(t *testing.T) {
	t.Parallel()
	sops := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	store := filepath.Join(td, "store")
	require.NoError(t, os.Mkdir(store, 0755))
	initGitStore(t, store)
	seat, recovery := genKey(t, td, "seat"), genKey(t, td, "recovery")
	require.NoError(t, os.WriteFile(filepath.Join(store, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644))
	config := fmt.Sprintf("creation_rules:\n  - path_regex: ^(rowan|other)\\.yaml$\n    age: %s,%s\n", seat.pubKey, recovery.pubKey)
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(config), 0644))
	for _, name := range []string{"rowan", "other"} {
		sealFileWithSops(t, sops, filepath.Join(store, name+".yaml"), []string{seat.pubKey, recovery.pubKey}, "TEST_KEY: disposable_fixture\n")
	}
	commitAndPush(t, store)
	args := []string{"exec", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops, "--only", "all", "--", "true"}
	_, _, code := runNovaSecrets(bin, args...)
	require.Equal(t, 0, code, "clean control exit=%d", code)
	sealFileWithSops(t, sops, filepath.Join(store, "other.yaml"), []string{seat.pubKey, recovery.pubKey}, "TEST_KEY: modified_disposable_fixture\n")
	runCmd(t, store, "git", "add", "other.yaml")
	_, _, checkCode := runNovaSecrets(bin, "check", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops)
	_, _, execCode := runNovaSecrets(bin, args...)
	t.Logf("modified sibling: check=%d exec=%d", checkCode, execCode)
	require.Equal(t, 1, checkCode, "invariant 8 disagreement: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
	require.Equal(t, 125, execCode, "invariant 8 disagreement: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
}

func TestRootExecRejectsDeletedOtherCommittedSeat(t *testing.T) {
	t.Parallel()
	sops := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	store := filepath.Join(td, "store")
	require.NoError(t, os.Mkdir(store, 0755))
	initGitStore(t, store)
	seat, recovery := genKey(t, td, "seat"), genKey(t, td, "recovery")
	require.NoError(t, os.WriteFile(filepath.Join(store, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644))
	config := fmt.Sprintf("creation_rules:\n  - path_regex: ^(rowan|other)\\.yaml$\n    age: %s,%s\n", seat.pubKey, recovery.pubKey)
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(config), 0644))
	for _, name := range []string{"rowan", "other"} {
		sealFileWithSops(t, sops, filepath.Join(store, name+".yaml"), []string{seat.pubKey, recovery.pubKey}, "TEST_KEY: disposable_fixture\n")
	}
	commitAndPush(t, store)
	args := []string{"exec", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops, "--only", "all", "--", "true"}

	// Delete other.yaml from disk without committing
	require.NoError(t, os.Remove(filepath.Join(store, "other.yaml")))
	_, _, checkCode := runNovaSecrets(bin, "check", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops)
	_, _, execCode := runNovaSecrets(bin, args...)
	t.Logf("deleted sibling: check=%d exec=%d", checkCode, execCode)
	require.Equal(t, 1, checkCode, "invariant 8 disagreement on deletion: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
	require.Equal(t, 125, execCode, "invariant 8 disagreement on deletion: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
}

func TestRootExecRejectsModifiedNestedCommittedYAML(t *testing.T) {
	t.Parallel()
	sops := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	store := filepath.Join(td, "store")
	require.NoError(t, os.Mkdir(store, 0755))
	initGitStore(t, store)
	seat, recovery := genKey(t, td, "seat"), genKey(t, td, "recovery")
	require.NoError(t, os.WriteFile(filepath.Join(store, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644))
	nestedDir := filepath.Join(store, "nested")
	require.NoError(t, os.Mkdir(nestedDir, 0755))
	config := fmt.Sprintf("creation_rules:\n  - path_regex: ^(rowan|nested/pool)\\.yaml$\n    age: %s,%s\n", seat.pubKey, recovery.pubKey)
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(config), 0644))
	sealFileWithSops(t, sops, filepath.Join(store, "rowan.yaml"), []string{seat.pubKey, recovery.pubKey}, "TEST_KEY: disposable_fixture\n")
	sealFileWithSops(t, sops, filepath.Join(nestedDir, "pool.yaml"), []string{seat.pubKey, recovery.pubKey}, "POOL_KEY: pool_val\n")
	commitAndPush(t, store)

	args := []string{"exec", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops, "--only", "all", "--", "true"}
	_, _, code := runNovaSecrets(bin, args...)
	require.Equal(t, 0, code, "clean control exit=%d", code)

	// Modify nested/pool.yaml and stage
	sealFileWithSops(t, sops, filepath.Join(nestedDir, "pool.yaml"), []string{seat.pubKey, recovery.pubKey}, "POOL_KEY: modified_pool_val\n")
	runCmd(t, store, "git", "add", "nested/pool.yaml")

	_, _, checkCode := runNovaSecrets(bin, "check", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops)
	_, _, execCode := runNovaSecrets(bin, args...)
	t.Logf("modified nested YAML: check=%d exec=%d", checkCode, execCode)
	require.Equal(t, 1, checkCode, "invariant 8 disagreement on nested yaml: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
	require.Equal(t, 125, execCode, "invariant 8 disagreement on nested yaml: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
}

func TestRootExecRejectsDirectoryReplacingOtherCommittedSeat(t *testing.T) {
	t.Parallel()
	sops := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	store := filepath.Join(td, "store")
	require.NoError(t, os.Mkdir(store, 0755))
	initGitStore(t, store)
	seat, recovery := genKey(t, td, "seat"), genKey(t, td, "recovery")
	require.NoError(t, os.WriteFile(filepath.Join(store, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644))
	config := fmt.Sprintf("creation_rules:\n  - path_regex: ^(rowan|other)\\.yaml$\n    age: %s,%s\n", seat.pubKey, recovery.pubKey)
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(config), 0644))
	for _, name := range []string{"rowan", "other"} {
		sealFileWithSops(t, sops, filepath.Join(store, name+".yaml"), []string{seat.pubKey, recovery.pubKey}, "TEST_KEY: disposable_fixture\n")
	}
	commitAndPush(t, store)
	args := []string{"exec", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops, "--only", "all", "--", "true"}
	_, _, code := runNovaSecrets(bin, args...)
	require.Equal(t, 0, code, "clean control exit=%d", code)
	require.NoError(t, os.Remove(filepath.Join(store, "other.yaml")))
	require.NoError(t, os.Mkdir(filepath.Join(store, "other.yaml"), 0755))
	_, _, checkCode := runNovaSecrets(bin, "check", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops)
	_, _, execCode := runNovaSecrets(bin, args...)
	t.Logf("directory replacement sibling: check=%d exec=%d", checkCode, execCode)
	require.Equal(t, 1, checkCode, "invariant 8 disagreement: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
	require.Equal(t, 125, execCode, "invariant 8 disagreement: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
}
