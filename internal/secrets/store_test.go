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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreParsers(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	// 1. recovery.pub tests
	recPath := filepath.Join(tmp, "recovery.pub")
	_, err := ReadRecoveryPub(tmp)
	require.Error(t, err, "expected error on absent recovery.pub")

	// Empty
	require.NoError(t, os.WriteFile(recPath, []byte(""), 0600))
	_, err = ReadRecoveryPub(tmp)
	require.Error(t, err, "expected error on empty recovery.pub")

	// Invalid key
	require.NoError(t, os.WriteFile(recPath, []byte("not-a-key\n"), 0600))
	_, err = ReadRecoveryPub(tmp)
	require.Error(t, err, "expected error on invalid key in recovery.pub")

	// Multiple lines
	validKey := "age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata"
	require.NoError(t, os.WriteFile(recPath, []byte(validKey+"\n"+validKey+"\n"), 0600))
	_, err = ReadRecoveryPub(tmp)
	require.Error(t, err, "expected error on multi-line recovery.pub")

	// Valid single line
	require.NoError(t, os.WriteFile(recPath, []byte(validKey+"\n"), 0600))
	k, err := ReadRecoveryPub(tmp)
	require.NoError(t, err, "unexpected error: %v", err)
	require.Equal(t, validKey, k, "got %q, want %q", k, validKey)

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
	require.NoError(t, os.WriteFile(sopsPath, []byte(sopsContent), 0600))
	cfg, err := ParseSopsConfig(tmp)
	require.NoError(t, err, "failed to parse sops config: %v", err)
	require.Len(t, cfg.CreationRules, 2, "expected 2 rules, got %d", len(cfg.CreationRules))
	rule1 := cfg.CreationRules[0]
	assert.Equal(t, `^rowan\.yaml$`, rule1.PathRegex, "rule 0 path regex: %s", rule1.PathRegex)
	assert.Equal(t, `^(SPACE_USER|SPACE_HOST)$`, rule1.UnencryptedRegex, "rule 0 unencrypted regex: %s", rule1.UnencryptedRegex)
	require.Len(t, rule1.Recipients, 2, "expected 2 recipients in rule 0, got %d", len(rule1.Recipients))

	// 3. Match rule
	matched, err := FindMatchingRule(cfg, "rowan.yaml")
	require.NoError(t, err, "failed to match rowan.yaml: %v", err)
	assert.Equal(t, `^rowan\.yaml$`, matched.PathRegex, "matched unexpected rule: %s", matched.PathRegex)

	// 4. Decrypted YAML secrets parsing
	decrypted := []byte(`
GH_TOKEN: ghp_testsecret123
SPACE_USER: rowan
VALID_KEY: "quotes_stripped"
`)
	sec, keys, err := ParseDecryptedSecrets(decrypted)
	require.NoError(t, err, "ParseDecryptedSecrets failed: %v", err)
	require.Len(t, sec, 3, "expected 3 secrets, got %d", len(sec))
	require.Len(t, keys, 3, "expected 3 secrets, got %d", len(sec))
	_ = sec["GH_TOKEN"].Use(func(v string) error {
		assert.Equal(t, "ghp_testsecret123", v, "GH_TOKEN value got %s", v)
		return nil
	})
	_ = sec["VALID_KEY"].Use(func(v string) error {
		assert.Equal(t, "quotes_stripped", v, "VALID_KEY value got %s", v)
		return nil
	})

	// 5. Decrypted secrets rejection of multi-line
	multiDecrypted := []byte(`
GH_TOKEN: ghp_testsecret123
MULTILINE: |
  line1
  line2
`)
	_, _, err = ParseDecryptedSecrets(multiDecrypted)
	require.Error(t, err, "expected error on multi-line secret")
}

func TestGitIndexParser(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	// Run git init, create a file, git add
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %v, out: %s", args, err, string(out))
	}
	run("init")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.com")

	f1 := filepath.Join(tmp, "tracked.txt")
	require.NoError(t, os.WriteFile(f1, []byte("hello"), 0644))
	run("add", "tracked.txt")
	run("commit", "-m", "initial")

	idx, err := ReadGitIndex(tmp)
	require.NoError(t, err, "ReadGitIndex failed: %v", err)
	assert.Contains(t, idx.Entries, "tracked.txt", "expected tracked.txt to be in index")
	assert.NotContains(t, idx.Entries, "untracked.txt", "untracked.txt should not be in index")

	// 2. Index Version 4 with prefix compression
	f2 := filepath.Join(tmp, "tracked2.txt")
	require.NoError(t, os.WriteFile(f2, []byte("world"), 0644))
	run("add", "tracked2.txt")
	run("update-index", "--index-version", "4")

	idxData, err := ReadGitIndex(tmp)
	require.NoError(t, err, "ReadGitIndex v4 failed: %v", err)
	require.Len(t, idxData.Entries, 2, "expected 2 entries in v4 index, got %d", len(idxData.Entries))
	_, ok := idxData.Entries["tracked.txt"]
	assert.True(t, ok, "tracked.txt missing from v4 index")
	{
		_, ok := idxData.Entries["tracked2.txt"]
		assert.True(t, ok, "tracked2.txt missing from v4 index")
	}
	// The index's blob ids are the working files' own.
	for _, f := range []string{f1, f2} {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.Equal(t, GitBlobSHA1(data), idxData.Entries[filepath.Base(f)].BlobSHA1, "%s: index blob differs from the file", f)
	}
}

func TestUnquoteYAMLPreservesTrailingQuotes(t *testing.T) {
	t.Parallel()

	data := []byte(`
V_TRAILQ: p@ss"
V_PAD: "  padded  "
V_SINGLE: 'it''s'
V_NORMAL: hello
`)
	secrets, keys, err := ParseDecryptedSecrets(data)
	require.NoError(t, err, "ParseDecryptedSecrets failed: %v", err)
	require.Len(t, keys, 4, "expected 4 keys, got %d", len(keys))
	_ = secrets["V_TRAILQ"].Use(func(v string) error {
		assert.Equal(t, `p@ss"`, v, "V_TRAILQ got %q, want %q", v, `p@ss"`)
		return nil
	})
	_ = secrets["V_PAD"].Use(func(v string) error {
		assert.Equal(t, "  padded  ", v, "V_PAD got %q, want %q", v, "  padded  ")
		return nil
	})
	_ = secrets["V_SINGLE"].Use(func(v string) error {
		assert.Equal(t, "it's", v, "V_SINGLE got %q, want %q", v, "it's")
		return nil
	})
	_ = secrets["V_NORMAL"].Use(func(v string) error {
		assert.Equal(t, "hello", v, "V_NORMAL got %q, want %q", v, "hello")
		return nil
	})
}

func TestPostSopsKeysDetected(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	content := `
sops:
    age:
        - recipient: age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata
LEAKED_TOKEN: cleartext_after_sops
`
	filePath := filepath.Join(tmp, "test.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0644))
	keys, recipients, hasSops, err := ParseStoreFileWithoutDecrypting(filePath)
	require.NoError(t, err, "ParseStoreFileWithoutDecrypting failed: %v", err)
	assert.True(t, hasSops, "expected hasSops=true")
	assert.Len(t, recipients, 1, "expected 1 recipient, got %d", len(recipients))
	found := false
	for _, k := range keys {
		if k.Name == "LEAKED_TOKEN" {
			found = true
			assert.True(t, k.Clear, "expected LEAKED_TOKEN to be marked clear")
		}
	}
	assert.True(t, found, "LEAKED_TOKEN after sops block was not detected")
}

func TestIsValidAsName(t *testing.T) {
	t.Parallel()

	valid := []string{"rowan", "emma-1", "seat_2", "A", "0"}
	for _, v := range valid {
		assert.True(t, IsValidAsName(v), "expected %q to be valid", v)
	}
	invalid := []string{"", "../outside", "a/b", "a\nb", "foo bar", "a*b"}
	for _, inv := range invalid {
		assert.False(t, IsValidAsName(inv), "expected %q to be invalid", inv)
	}
}

func TestUnquoteYAMLDecodesAllValidYAMLEscapesAndRejectsUnknown(t *testing.T) {
	t.Parallel()

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
			assert.Error(t, err, "unquoteYAML(%q) expected error, got nil (val=%q)", tc.input, got)
		} else {
			if assert.NoError(t, err, "unquoteYAML(%q) unexpected error: %v", tc.input, err) {
				assert.Equal(t, tc.want, got, "unquoteYAML(%q) = %q, want %q", tc.input, got, tc.want)
			}
		}
	}
}

func TestParseDecryptedSecretsLargeLineBuffer(t *testing.T) {
	t.Parallel()

	largeVal := strings.Repeat("A", 128*1024)
	input := fmt.Sprintf("LARGE_KEY: %s\n", largeVal)
	sec, keys, err := ParseDecryptedSecrets([]byte(input))
	require.NoError(t, err, "ParseDecryptedSecrets failed on 128KB line: %v", err)
	require.Len(t, keys, 1, "expected 1 key LARGE_KEY, got %v", keys)
	require.Equal(t, "LARGE_KEY", keys[0], "expected 1 key LARGE_KEY, got %v", keys)
	_ = sec["LARGE_KEY"].Use(func(v string) error {
		assert.Len(t, v, 128*1024, "expected len %d, got %d", 128*1024, len(v))
		return nil
	})
}

func TestReadGitIndexExtendedFlags(t *testing.T) {
	t.Parallel()

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

	require.NoError(t, os.WriteFile(indexPath, buf.Bytes(), 0644))

	idx, err := ReadGitIndex(td)
	require.NoError(t, err, "ReadGitIndex with extended flags failed: %v", err)
	require.Len(t, idx.Entries, 1, "expected 1 entry, got %d", len(idx.Entries))
	entry, ok := idx.Entries["test.txt"]
	require.True(t, ok, "expected test.txt entry")
	assert.Equal(t, string(sha1Bytes), string(entry.BlobSHA1[:]), "blob SHA1 mismatch")
}
