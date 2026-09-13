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
}
