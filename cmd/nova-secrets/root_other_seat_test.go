package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRootExecRejectsModifiedOtherCommittedSeat(t *testing.T) {
	sops := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	store := filepath.Join(td, "store")
	if err := os.Mkdir(store, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, store)
	seat, recovery := genKey(t, td, "seat"), genKey(t, td, "recovery")
	if err := os.WriteFile(filepath.Join(store, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("creation_rules:\n  - path_regex: ^(rowan|other)\\.yaml$\n    age: %s,%s\n", seat.pubKey, recovery.pubKey)
	if err := os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rowan", "other"} {
		sealFileWithSops(t, sops, filepath.Join(store, name+".yaml"), []string{seat.pubKey, recovery.pubKey}, "TEST_KEY: disposable_fixture\n")
	}
	commitAndPush(t, store)
	args := []string{"exec", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops, "--only", "all", "--", "true"}
	if _, _, code := runNovaSecrets(bin, args...); code != 0 {
		t.Fatalf("clean control exit=%d", code)
	}
	sealFileWithSops(t, sops, filepath.Join(store, "other.yaml"), []string{seat.pubKey, recovery.pubKey}, "TEST_KEY: modified_disposable_fixture\n")
	runCmd(t, store, "git", "add", "other.yaml")
	_, _, checkCode := runNovaSecrets(bin, "check", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops)
	_, _, execCode := runNovaSecrets(bin, args...)
	t.Logf("modified sibling: check=%d exec=%d", checkCode, execCode)
	if checkCode != 1 || execCode != 125 {
		t.Fatalf("invariant 8 disagreement: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
	}
}

func TestRootExecRejectsDeletedOtherCommittedSeat(t *testing.T) {
	sops := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	store := filepath.Join(td, "store")
	if err := os.Mkdir(store, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, store)
	seat, recovery := genKey(t, td, "seat"), genKey(t, td, "recovery")
	if err := os.WriteFile(filepath.Join(store, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("creation_rules:\n  - path_regex: ^(rowan|other)\\.yaml$\n    age: %s,%s\n", seat.pubKey, recovery.pubKey)
	if err := os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rowan", "other"} {
		sealFileWithSops(t, sops, filepath.Join(store, name+".yaml"), []string{seat.pubKey, recovery.pubKey}, "TEST_KEY: disposable_fixture\n")
	}
	commitAndPush(t, store)
	args := []string{"exec", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops, "--only", "all", "--", "true"}

	// Delete other.yaml from disk without committing
	if err := os.Remove(filepath.Join(store, "other.yaml")); err != nil {
		t.Fatal(err)
	}
	_, _, checkCode := runNovaSecrets(bin, "check", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops)
	_, _, execCode := runNovaSecrets(bin, args...)
	t.Logf("deleted sibling: check=%d exec=%d", checkCode, execCode)
	if checkCode != 1 || execCode != 125 {
		t.Fatalf("invariant 8 disagreement on deletion: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
	}
}

func TestRootExecRejectsModifiedNestedCommittedYAML(t *testing.T) {
	sops := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	store := filepath.Join(td, "store")
	if err := os.Mkdir(store, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, store)
	seat, recovery := genKey(t, td, "seat"), genKey(t, td, "recovery")
	if err := os.WriteFile(filepath.Join(store, "recovery.pub"), []byte(recovery.pubKey+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	nestedDir := filepath.Join(store, "nested")
	if err := os.Mkdir(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("creation_rules:\n  - path_regex: ^(rowan|nested/pool)\\.yaml$\n    age: %s,%s\n", seat.pubKey, recovery.pubKey)
	if err := os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	sealFileWithSops(t, sops, filepath.Join(store, "rowan.yaml"), []string{seat.pubKey, recovery.pubKey}, "TEST_KEY: disposable_fixture\n")
	sealFileWithSops(t, sops, filepath.Join(nestedDir, "pool.yaml"), []string{seat.pubKey, recovery.pubKey}, "POOL_KEY: pool_val\n")
	commitAndPush(t, store)

	args := []string{"exec", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops, "--only", "all", "--", "true"}
	if _, _, code := runNovaSecrets(bin, args...); code != 0 {
		t.Fatalf("clean control exit=%d", code)
	}

	// Modify nested/pool.yaml and stage
	sealFileWithSops(t, sops, filepath.Join(nestedDir, "pool.yaml"), []string{seat.pubKey, recovery.pubKey}, "POOL_KEY: modified_pool_val\n")
	runCmd(t, store, "git", "add", "nested/pool.yaml")

	_, _, checkCode := runNovaSecrets(bin, "check", "--store", store, "--as", "rowan", "--key", seat.privPath, "--sops", sops)
	_, _, execCode := runNovaSecrets(bin, args...)
	t.Logf("modified nested YAML: check=%d exec=%d", checkCode, execCode)
	if checkCode != 1 || execCode != 125 {
		t.Fatalf("invariant 8 disagreement on nested yaml: want check=1 exec=125; got check=%d exec=%d", checkCode, execCode)
	}
}
