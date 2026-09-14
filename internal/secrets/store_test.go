package secrets

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestUnquoteYAMLDecodesAllValidYAMLEscapesAndRejectsUnknown(t *testing.T) {
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{`"hello\nworld"`, "hello\nworld", false},
		{`"bell\a and \b"`, "bell\a and \b", false},
		{`"tab\t and \v and \f"`, "tab\t and \v and \f", false},
		{`"cr\r and \e"`, "cr\r and \x1b", false},
		{`"space\  and quote\" and slash\/ and backslash\\"`, "space  and quote\" and slash/ and backslash\\", false},
		{`"hex \x41\x42"`, "hex AB", false},
		{`"unicode \u0041\u0042"`, "unicode AB", false},
		{`"wide \U00000041"`, "wide A", false},
		{`"nel \N and nbsp \_ and ls \L and ps \P"`, "nel \u0085 and nbsp \u00a0 and ls \u2028 and ps \u2029", false},
		{`"null \0"`, "null \x00", false},
		{`'single ''quoted'' string'`, "single 'quoted' string", false},
		{`plain_value`, "plain_value", false},
		// Error cases
		{`"invalid \c"`, "", true},
		{`"truncated \x4"`, "", true},
		{`"truncated \u004"`, "", true},
		{`"truncated \U0000004"`, "", true},
		{`"invalid hex \xZZ"`, "", true},
		{`"invalid surrogate \UD8000000"`, "", true},
		{`"unterminated backslash \"`, "", true},
	}

	for _, tc := range cases {
		got, err := unquoteYAML(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("unquoteYAML(%q) expected error, got nil (val=%q)", tc.input, got)
			}
		} else {
			if err != nil {
				t.Errorf("unquoteYAML(%q) unexpected error: %v", tc.input, err)
			} else if got != tc.want {
				t.Errorf("unquoteYAML(%q) = %q, want %q", tc.input, got, tc.want)
			}
		}
	}
}

func TestParseDecryptedSecretsLargeLineBuffer(t *testing.T) {
	largeVal := strings.Repeat("A", 128*1024)
	input := fmt.Sprintf("LARGE_KEY: %s\n", largeVal)
	sec, keys, err := ParseDecryptedSecrets([]byte(input))
	if err != nil {
		t.Fatalf("ParseDecryptedSecrets failed on 128KB line: %v", err)
	}
	if len(keys) != 1 || keys[0] != "LARGE_KEY" {
		t.Fatalf("expected 1 key LARGE_KEY, got %v", keys)
	}
	_ = sec["LARGE_KEY"].Use(func(v string) error {
		if len(v) != 128*1024 {
			t.Errorf("expected len %d, got %d", 128*1024, len(v))
		}
		return nil
	})
}

func TestReadGitIndexExtendedFlags(t *testing.T) {
	td := t.TempDir()
	gitDir := filepath.Join(td, ".git")
	_ = os.MkdirAll(gitDir, 0755)
	indexPath := filepath.Join(gitDir, "index")

	var buf bytes.Buffer
	// Header: DIRC, version 3, 1 entry
	buf.WriteString("DIRC")
	_ = binary.Write(&buf, binary.BigEndian, uint32(3))
	_ = binary.Write(&buf, binary.BigEndian, uint32(1))

	// Entry:
	// 40 bytes stat dummy
	buf.Write(make([]byte, 40))
	// 20 bytes sha1
	sha1Bytes := []byte("01234567890123456789")
	buf.Write(sha1Bytes)
	// 2 bytes flags: 0x4000 | 8
	_ = binary.Write(&buf, binary.BigEndian, uint16(0x4000|8))
	// 2 bytes extended flags
	_ = binary.Write(&buf, binary.BigEndian, uint16(0x0000))
	// 8 bytes path: test.txt
	buf.WriteString("test.txt")
	// 8 NUL bytes padding (headerLen 64 + 8 path + 8 padding = 80 bytes)
	buf.Write(make([]byte, 8))

	// Trailing 20 bytes checksum
	buf.Write(make([]byte, 20))

	if err := os.WriteFile(indexPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	idx, err := ReadGitIndex(td)
	if err != nil {
		t.Fatalf("ReadGitIndex with extended flags failed: %v", err)
	}
	if len(idx.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(idx.Entries))
	}
	entry, ok := idx.Entries["test.txt"]
	if !ok {
		t.Fatalf("expected test.txt entry")
	}
	if string(entry.BlobSHA1[:]) != string(sha1Bytes) {
		t.Errorf("blob SHA1 mismatch")
	}
}
