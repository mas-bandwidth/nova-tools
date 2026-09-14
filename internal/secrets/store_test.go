package secrets

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStoreParsers(t *testing.T) {
	tmp := t.TempDir()

	// 1. recovery.pub tests
	recPath := filepath.Join(tmp, "recovery.pub")
	if _, err := ReadRecoveryPub(tmp); err == nil {
		t.Fatalf("expected error on absent recovery.pub")
	}

	// Empty
	if err := os.WriteFile(recPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecoveryPub(tmp); err == nil {
		t.Fatalf("expected error on empty recovery.pub")
	}

	// Invalid key
	if err := os.WriteFile(recPath, []byte("not-a-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecoveryPub(tmp); err == nil {
		t.Fatalf("expected error on invalid key in recovery.pub")
	}

	// Multiple lines
	validKey := "age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata"
	if err := os.WriteFile(recPath, []byte(validKey+"\n"+validKey+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecoveryPub(tmp); err == nil {
		t.Fatalf("expected error on multi-line recovery.pub")
	}

	// Valid single line
	if err := os.WriteFile(recPath, []byte(validKey+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	k, err := ReadRecoveryPub(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k != validKey {
		t.Fatalf("got %q, want %q", k, validKey)
	}

	// 2. .sops.yaml parser tests
	sopsPath := filepath.Join(tmp, ".sops.yaml")
	sopsContent := `
creation_rules:
  - path_regex: ^rowan\.yaml$
    unencrypted_regex: ^(SPACE_USER|SPACE_HOST)$
    age: >-
      age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata,age158lrf2hlptfwl6fh280y6pq58vdmumnqzhk5vd669aqf37ca3sus9mcazh
  - path_regex: ^other\.yaml$
    age: age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata
`
	if err := os.WriteFile(sopsPath, []byte(sopsContent), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseSopsConfig(tmp)
	if err != nil {
		t.Fatalf("failed to parse sops config: %v", err)
	}
	if len(cfg.CreationRules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg.CreationRules))
	}
	rule1 := cfg.CreationRules[0]
	if rule1.PathRegex != `^rowan\.yaml$` {
		t.Errorf("rule 0 path regex: %s", rule1.PathRegex)
	}
	if rule1.UnencryptedRegex != `^(SPACE_USER|SPACE_HOST)$` {
		t.Errorf("rule 0 unencrypted regex: %s", rule1.UnencryptedRegex)
	}
	if len(rule1.Recipients) != 2 {
		t.Fatalf("expected 2 recipients in rule 0, got %d", len(rule1.Recipients))
	}

	// 3. Match rule
	matched, err := FindMatchingRule(cfg, "rowan.yaml")
	if err != nil {
		t.Fatalf("failed to match rowan.yaml: %v", err)
	}
	if matched.PathRegex != `^rowan\.yaml$` {
		t.Errorf("matched unexpected rule: %s", matched.PathRegex)
	}

	// 4. Decrypted YAML secrets parsing
	decrypted := []byte(`
GH_TOKEN: ghp_testsecret123
SPACE_USER: rowan
VALID_KEY: "quotes_stripped"
`)
	sec, keys, err := ParseDecryptedSecrets(decrypted)
	if err != nil {
		t.Fatalf("ParseDecryptedSecrets failed: %v", err)
	}
	if len(sec) != 3 || len(keys) != 3 {
		t.Fatalf("expected 3 secrets, got %d", len(sec))
	}
	_ = sec["GH_TOKEN"].Use(func(v string) error {
		if v != "ghp_testsecret123" {
			t.Errorf("GH_TOKEN value got %s", v)
		}
		return nil
	})
	_ = sec["VALID_KEY"].Use(func(v string) error {
		if v != "quotes_stripped" {
			t.Errorf("VALID_KEY value got %s", v)
		}
		return nil
	})

	// 5. Decrypted secrets rejection of multi-line
	multiDecrypted := []byte(`
GH_TOKEN: ghp_testsecret123
MULTILINE: |
  line1
  line2
`)
	if _, _, err := ParseDecryptedSecrets(multiDecrypted); err == nil {
		t.Fatalf("expected error on multi-line secret")
	}
}

func TestGitIndexParser(t *testing.T) {
	tmp := t.TempDir()
	// Run git init, create a file, git add
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v, out: %s", args, err, string(out))
		}
	}
	run("init")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.com")

	f1 := filepath.Join(tmp, "tracked.txt")
	if err := os.WriteFile(f1, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-m", "initial")

	tracked, err := ReadGitIndexTrackedFiles(tmp)
	if err != nil {
		t.Fatalf("ReadGitIndexTrackedFiles failed: %v", err)
	}
	if !tracked["tracked.txt"] {
		t.Errorf("expected tracked.txt to be in index")
	}
	if tracked["untracked.txt"] {
		t.Errorf("untracked.txt should not be in index")
	}

	// 2. Index Version 4 with prefix compression
	f2 := filepath.Join(tmp, "tracked2.txt")
	if err := os.WriteFile(f2, []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked2.txt")
	run("update-index", "--index-version", "4")

	idxData, err := ReadGitIndex(tmp)
	if err != nil {
		t.Fatalf("ReadGitIndex v4 failed: %v", err)
	}
	if len(idxData.Entries) != 2 {
		t.Fatalf("expected 2 entries in v4 index, got %d", len(idxData.Entries))
	}
	if _, ok := idxData.Entries["tracked.txt"]; !ok {
		t.Errorf("tracked.txt missing from v4 index")
	}
	if _, ok := idxData.Entries["tracked2.txt"]; !ok {
		t.Errorf("tracked2.txt missing from v4 index")
	}
	// Verify blob SHA1 match
	if err := VerifyFileMatchesIndex(tmp, f1, idxData); err != nil {
		t.Errorf("VerifyFileMatchesIndex f1 failed: %v", err)
	}
	if err := VerifyFileMatchesIndex(tmp, f2, idxData); err != nil {
		t.Errorf("VerifyFileMatchesIndex f2 failed: %v", err)
	}
}

func TestUnquoteYAMLPreservesTrailingQuotes(t *testing.T) {
	data := []byte(`
V_TRAILQ: p@ss"
V_PAD: "  padded  "
V_SINGLE: 'it''s'
V_NORMAL: hello
`)
	secrets, keys, err := ParseDecryptedSecrets(data)
	if err != nil {
		t.Fatalf("ParseDecryptedSecrets failed: %v", err)
	}
	if len(keys) != 4 {
		t.Fatalf("expected 4 keys, got %d", len(keys))
	}
	_ = secrets["V_TRAILQ"].Use(func(v string) error {
		if v != `p@ss"` {
			t.Errorf("V_TRAILQ got %q, want %q", v, `p@ss"`)
		}
		return nil
	})
	_ = secrets["V_PAD"].Use(func(v string) error {
		if v != "  padded  " {
			t.Errorf("V_PAD got %q, want %q", v, "  padded  ")
		}
		return nil
	})
	_ = secrets["V_SINGLE"].Use(func(v string) error {
		if v != "it's" {
			t.Errorf("V_SINGLE got %q, want %q", v, "it's")
		}
		return nil
	})
	_ = secrets["V_NORMAL"].Use(func(v string) error {
		if v != "hello" {
			t.Errorf("V_NORMAL got %q, want %q", v, "hello")
		}
		return nil
	})
}

func TestPostSopsKeysDetected(t *testing.T) {
	tmp := t.TempDir()
	content := `
sops:
    age:
        - recipient: age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata
LEAKED_TOKEN: cleartext_after_sops
`
	filePath := filepath.Join(tmp, "test.yaml")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	keys, recipients, hasSops, err := ParseStoreFileWithoutDecrypting(filePath)
	if err != nil {
		t.Fatalf("ParseStoreFileWithoutDecrypting failed: %v", err)
	}
	if !hasSops {
		t.Errorf("expected hasSops=true")
	}
	if len(recipients) != 1 {
		t.Errorf("expected 1 recipient, got %d", len(recipients))
	}
	found := false
	for _, k := range keys {
		if k.Name == "LEAKED_TOKEN" {
			found = true
			if !k.Clear {
				t.Errorf("expected LEAKED_TOKEN to be marked clear")
			}
		}
	}
	if !found {
		t.Errorf("LEAKED_TOKEN after sops block was not detected")
	}
}

func TestIsValidAsName(t *testing.T) {
	valid := []string{"rowan", "emma-1", "seat_2", "A", "0"}
	for _, v := range valid {
		if !IsValidAsName(v) {
			t.Errorf("expected %q to be valid", v)
		}
	}
	invalid := []string{"", "../outside", "a/b", "a\nb", "foo bar", "a*b"}
	for _, inv := range invalid {
		if IsValidAsName(inv) {
			t.Errorf("expected %q to be invalid", inv)
		}
	}
}
