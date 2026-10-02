package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlacementReceiptNamesTheBytesActuallyDecrypted pins SPEC-SECRETS "Nothing
// derived from a value": the sealed-file identity names the ciphertext from which
// the delivered value came, even when another writer replaces the live store file.
func TestPlacementReceiptNamesTheBytesActuallyDecrypted(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)
	original := filepath.Join(f.store, "rowan.yaml")
	replacement := filepath.Join(t.TempDir(), "replacement.yaml")
	captured := filepath.Join(t.TempDir(), "decrypted-input.yaml")
	require.NoError(t, os.WriteFile(replacement, []byte("DEEPSEEK_API_KEY: ENC[FAKE-REPLACEMENT]\n"), 0o644))
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	// The fake reads its actual input (which may be a future immutable snapshot).
	// A separate writer then replaces the original store path, never that input path.
	writeFakeExe(t, f.sops, "#!/bin/sh\n"+
		"case \"$1\" in\n"+
		"--version) echo 'sops 3.13.3'; exit 0 ;;\n"+
		"-d) input=$(cat \"$2\") || exit 1\n"+
		"if test -f "+quote(replacement)+"; then\n"+
		"printf '%s\\n' \"$input\" > "+quote(captured)+" || exit 1\n"+
		"mv "+quote(replacement)+" "+quote(original)+" || exit 1\n"+
		"fi\n"+
		"case \"$input\" in\n"+
		"*FAKE-REPLACEMENT*) printf 'DEEPSEEK_API_KEY: replacement-dummy-value\\n' ;;\n"+
		"*) printf 'DEEPSEEK_API_KEY: %s\\n' "+quote(f.value)+" ;;\n"+
		"esac\nexit 0 ;;\n"+
		"esac\nexit 1\n")

	stdout, stderr, code := runNovaSecrets(f.bin, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)...)
	require.FileExists(t, captured, "the fake must reach decryption before any accepted refusal")
	require.NoFileExists(t, replacement, "the external replacement must have happened")
	receiptPath := filepath.Join(f.receipts, "mini.receipt")
	if code != 0 {
		assert.NotEmpty(t, stderr, "a refused changing input must explain the refusal")
		assert.NotContains(t, stdout, "SECRETS PLACE OK")
		_, err := os.Stat(f.sshStdinFile)
		assert.True(t, os.IsNotExist(err), "a refused changing input must not deliver: %v; stderr=%s", err, stderr)
		_, err = os.Stat(receiptPath)
		assert.True(t, os.IsNotExist(err), "a refused changing input must not record placement: %v", err)
		return
	}

	copied, err := os.ReadFile(f.sshStdinFile)
	require.NoError(t, err)
	assert.Contains(t, string(copied), f.value, "the fake decrypted the original ciphertext")
	decryptedBlob := blobOf(t, captured)
	assert.NotEqual(t, decryptedBlob, blobOf(t, original), "the replacement must change ciphertext identity")
	assert.Contains(t, stdout, "blob="+decryptedBlob, "success must identify the bytes actually decrypted")
	receipt, err := os.ReadFile(receiptPath)
	require.NoError(t, err)
	fields := strings.Split(strings.TrimSuffix(string(receipt), "\n"), "\t")
	require.Len(t, fields, 6)
	assert.Equal(t, decryptedBlob, fields[4], "the receipt must identify the delivered value's ciphertext")

	plan, stderr, code := runNovaSecrets(f.bin, append(f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath), "--dry-run")...)
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, plan, "action=replace", "the live ciphertext changed after the delivered value was decrypted")
}
