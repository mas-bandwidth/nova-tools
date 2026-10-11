package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSeatAddRefusesAnotherSeatsPublicKey is the wall against a --pub that is already
// a seat's key. Without it `seat add --as bo --pub <ada's key>` writes a bo.yaml that
// ada's key opens, with bo's first values re-sealed to ada: a seat whose credentials a
// different seat reads (SPEC-SECRETS "seat add"; tla/SecretsSeat.tla on
// sprint/md-secrets-h.w1.g1.e15, the SeatAdd action, whose guards on --pub name the
// recovery key and the private half but not a key another rule already carries).
// The refusal comes before any write, names the seat that owns the key and the rule
// that names it, and never the key's value beyond what the rule file already shows.
func TestSeatAddRefusesAnotherSeatsPublicKey(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	dir := t.TempDir()
	storeDir := filepath.Join(dir, "store")
	mustMkdir(t, storeDir, 0755)
	mustMkdir(t, filepath.Join(storeDir, ".git"), 0755)

	// The store: one seat, ada, under rule 1 with its own key and the recovery key,
	// and this machine holds the key that opens ada.yaml.
	pubAda := testPub('a')
	mustWrite(t, filepath.Join(storeDir, ".sops.yaml"),
		"creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: "+pubAda+","+pubRecovery+"\n", 0644)
	mustWrite(t, filepath.Join(storeDir, "recovery.pub"), pubRecovery+"\n", 0644)
	mustWrite(t, filepath.Join(storeDir, "ada.yaml"),
		fakeCipher([]string{pubAda, pubRecovery}, "GH_TOKEN: ghp_ada\n"), 0644)

	keyDir := filepath.Join(dir, "keys")
	mustMkdir(t, keyDir, 0700)
	adaKey := filepath.Join(keyDir, "ada.key")
	mustWrite(t, adaKey, "AGE-SECRET-KEY-1ADA\n# public key: "+pubAda+"\n", 0600)

	sopsPath := filepath.Join(dir, "sops")
	require.NoError(t, os.Symlink(sharedFakeSops, sopsPath))

	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(storeDir, rel))
		if os.IsNotExist(err) {
			return ""
		}
		require.NoError(t, err, "reading %s", rel)
		return string(b)
	}
	configBefore, adaBefore := read(".sops.yaml"), read("ada.yaml")

	// The operator of ada adds bo under ada's own key.
	lines, err := RunSeatAdd(SeatAddOptions{
		StoreDir: storeDir,
		AsName:   "bo",
		Pub:      pubAda,
		From:     "ada",
		Only:     "GH_TOKEN",
		KeyPath:  adaKey,
		SopsPath: sopsPath,
	})
	require.Error(t, err, "seat add took another seat's key as --pub and printed %v", lines)

	assert.Contains(t, err.Error(),
		"--pub is already the key of seat ada (rule 1 of .sops.yaml); a seat is one seat key, and a new seat's key comes from its own keygen receipt",
		"the refusal does not name the owning seat and its rule: %v", err)
	assert.NotContains(t, err.Error(), pubAda, "the refusal carries the key's value: %v", err)

	assert.Equal(t, configBefore, read(".sops.yaml"), ".sops.yaml changed under a refusal")
	assert.Equal(t, adaBefore, read("ada.yaml"), "the source seat's file changed under a refusal")
	assert.Empty(t, read("bo.yaml"), "a refused run left a seat file behind")
}
