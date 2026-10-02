package main

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// The rule (docs/SPEC-SECRETS.md, "Nothing derived from a value"): nothing nova-secrets
// prints, writes to a receipt, logs, or puts in a --json object or a dry-run plan is
// derived from a secret's plaintext in a way a reader without the key could test a guess
// against. A placement is identified by the sealed file instead (file, head, blob).

// valueDigests is every common hex digest of value, whole, in both cases: what a reader
// holding only the output would compare a guessed value's digest against.
func valueDigests(value string) []string {
	s1, s256, s512, m5 := sha1.Sum([]byte(value)), sha256.Sum256([]byte(value)), sha512.Sum512([]byte(value)), md5.Sum([]byte(value))
	var out []string
	for _, d := range []string{hex.EncodeToString(s1[:]), hex.EncodeToString(s256[:]), hex.EncodeToString(s512[:]), hex.EncodeToString(m5[:])} {
		out = append(out, d, strings.ToUpper(d))
	}
	return out
}

// assertNoValueDerivative fails when text holds the value, a fragment of it, or any hex
// digest of it whole or by a prefix of 8 or more characters (every longer prefix holds
// the 8-character one, so that one is what is searched for).
func assertNoValueDerivative(t *testing.T, what, text, value string) {
	t.Helper()
	assert.False(t, secrets.Leaks(text, secrets.NewSecret(value)), "%s carries the value or a fragment of it:\n%s", what, text)
	for _, d := range valueDigests(value) {
		assert.NotContains(t, text, d[:8], "%s carries a digest of the value (%s...):\n%s", what, d[:8], text)
	}
}

// blobOf is the git blob id of the file at path: what a receipt names a sealed file by,
// the value `git rev-parse HEAD:<file>` prints for it once it is committed.
func blobOf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	id := secrets.GitBlobSHA1(b)
	return hex.EncodeToString(id[:])
}

// TestNoOutputCarriesADigestOfTheValue runs every verb that touches a placed value --
// place --dry-run, place, placed, and the dry run again with the receipt on disk -- and
// holds every line on both streams and the receipt file to the rule.
func TestNoOutputCarriesADigestOfTheValue(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)
	dry := append(f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath), "--dry-run")
	for _, args := range [][]string{dry, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath), f.placedArgs("mini"), dry} {
		stdout, stderr, code := runNovaSecrets(f.bin, args...)
		require.Equal(t, 0, code, "%v: %s", args, stderr)
		assertNoValueDerivative(t, args[0]+" stdout", stdout, f.value)
		assertNoValueDerivative(t, args[0]+" stderr", stderr, f.value)
	}
	receipt, err := os.ReadFile(filepath.Join(f.receipts, "mini.receipt"))
	require.NoError(t, err)
	assertNoValueDerivative(t, "the receipt", string(receipt), f.value)
}

// TestAPlacementIsIdentifiedByTheSealedFile: the receipt, the OK line, the plan and
// placed all name the seat file, the store's HEAD commit and the sealed file's blob id;
// a changed sealed file is a placement to make again, never "unchanged".
func TestAPlacementIsIdentifiedByTheSealedFile(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)
	head := strings.Repeat("ab", 20)
	require.NoError(t, os.WriteFile(filepath.Join(f.store, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.store, ".git", "refs", "heads"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.store, ".git", "refs", "heads", "main"), []byte(head+"\n"), 0o644))
	identity := "file=rowan.yaml head=" + head + " blob=" + blobOf(t, filepath.Join(f.store, "rowan.yaml"))

	dry := append(f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath), "--dry-run")
	plan, _, code := runNovaSecrets(f.bin, dry...)
	require.Equal(t, 0, code)
	assert.Contains(t, plan, "mode=0600 "+identity+"\n")
	ok, stderr, code := runNovaSecrets(f.bin, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)...)
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, ok, " "+identity+" stamp=")
	listed, _, code := runNovaSecrets(f.bin, f.placedArgs("mini")...)
	require.Equal(t, 0, code)
	assert.Contains(t, listed, "SECRETS PLACED ITEM machine=mini secret=DEEPSEEK_API_KEY path="+f.remotePath+" "+identity+" stamp=")
	assert.NotContains(t, listed, "identity=unknown")
	receipt, err := os.ReadFile(filepath.Join(f.receipts, "mini.receipt"))
	require.NoError(t, err)
	assert.Equal(t, "DEEPSEEK_API_KEY\t"+f.remotePath+"\trowan.yaml\t"+head+"\t"+blobOf(t, filepath.Join(f.store, "rowan.yaml"))+"\t",
		string(receipt)[:strings.LastIndex(string(receipt), "\t")+1])

	again, _, _ := runNovaSecrets(f.bin, dry...)
	assert.Contains(t, again, "action=unchanged", "the same sealed file at the same path changes nothing")
	require.NoError(t, os.WriteFile(filepath.Join(f.store, "rowan.yaml"), []byte("DEEPSEEK_API_KEY: ENC[FAKE-RESEALED]\n"), 0o644))
	resealed, _, _ := runNovaSecrets(f.bin, dry...)
	assert.Contains(t, resealed, "action=replace", "a resealed file is placed again")
}

// TestAnOlderReceiptIsReadWithoutItsDigest: a receipt line from an older build (secret,
// path, sha256 of the value, stamp) lists as identity=unknown with the digest dropped, a
// NOTE names the count and the two commands that refresh it, the dry run never calls it
// unchanged, and the next place to the machine rewrites the file without any old digest.
func TestAnOlderReceiptIsReadWithoutItsDigest(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)
	const otherValue = "zq-other-legacy-value-7731"
	sum, other := sha256.Sum256([]byte(f.value)), sha256.Sum256([]byte(otherValue))
	require.NoError(t, os.MkdirAll(f.receipts, 0o700))
	file := filepath.Join(f.receipts, "mini.receipt")
	require.NoError(t, os.WriteFile(file, []byte(
		"DEEPSEEK_API_KEY\t"+f.remotePath+"\t"+hex.EncodeToString(sum[:])+"\t2026-09-01T00:00:00Z\n"+
			"OTHER_KEY\t/home/glenn/other.env\t"+hex.EncodeToString(other[:])+"\t2026-09-01T00:00:00Z\n"), 0o600))

	listed, stderr, code := runNovaSecrets(f.bin, f.placedArgs("mini")...)
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, listed, "SECRETS PLACED ITEM machine=mini secret=DEEPSEEK_API_KEY path="+f.remotePath+" file=- head=- blob=- stamp=2026-09-01T00:00:00Z identity=unknown")
	assert.Contains(t, listed, "SECRETS PLACED NOTE 2 receipt(s) for mini carry no sealed-file identity (identity=unknown: place again); 2 were written by an older build and still hold a hash of the value on disk")
	assert.Contains(t, listed, "run: nova-secrets place --machine mini --secret <NAME>")
	assert.Contains(t, listed, "rm "+file)
	assertNoValueDerivative(t, "placed over an older receipt", listed, f.value)
	assertNoValueDerivative(t, "placed over an older receipt", listed, otherValue)

	plan, _, code := runNovaSecrets(f.bin, append(f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath), "--dry-run")...)
	require.Equal(t, 0, code)
	assert.Contains(t, plan, "action=replace", "an older receipt's identity is unknown, never unchanged")

	_, stderr, code = runNovaSecrets(f.bin, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)...)
	require.Equal(t, 0, code, stderr)
	rewritten, err := os.ReadFile(file)
	require.NoError(t, err)
	assertNoValueDerivative(t, "the rewritten receipt", string(rewritten), f.value)
	assertNoValueDerivative(t, "the rewritten receipt", string(rewritten), otherValue)
	assert.Contains(t, string(rewritten), "OTHER_KEY\t/home/glenn/other.env\t-\t-\t-\t2026-09-01T00:00:00Z\n", "the other secret's line keeps its place and loses its digest")
	listed, _, _ = runNovaSecrets(f.bin, f.placedArgs("mini")...)
	assert.Contains(t, listed, "SECRETS PLACED NOTE 1 receipt(s) for mini carry no sealed-file identity (identity=unknown: place again); run: ")
	assert.NotContains(t, listed, "older build", "a rewritten file holds no digest, and the NOTE no longer says it does")
}
