package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAbsentSeatFixture is newSealFixture with the counterexample's shape: a merged
// rule for new.yaml and no new.yaml in the store. The fixture key (its public half
// age1test) is not a recipient of that rule, so the decrypt that proves the caller's
// key opens the target has nothing to prove it with.
func newAbsentSeatFixture(t *testing.T) *sealFixture {
	t.Helper()
	f := newSealFixture(t, "")
	require.NoError(t, os.WriteFile(filepath.Join(f.storeDir, ".sops.yaml"),
		[]byte("creation_rules:\n  - path_regex: ^new\\.yaml$\n    age: age1abc\n"), 0644))
	require.NoError(t, os.Remove(filepath.Join(f.storeDir, "rowan.yaml")))
	return f
}

// TestSealIntoAnAbsentSeatFileNeedsTheTargetKey pins SPEC-SECRETS rule 12, which the
// tla/SecretsSeat.tla model of this package states in its Seal action: seal decrypts
// before it writes, and only the target seat's own key opens the target seat's file,
// so a seat file the store does not hold is never written by seal -- a rule for the
// name alone, held by any store key, must not be enough. The refusal is the verb's
// exit-2 sentence (`SECRETS SEAL REFUSED: <seat>.yaml does not exist in the store; a
// new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule
// 12); run: nova-secrets seal -h`, the one refusal grammar the CLI renders for every
// RunSeal error), typed as errSeatFileAbsent, and it leaves the store byte for byte:
// no value read into a write, no sops, no git, no gh.
func TestSealIntoAnAbsentSeatFileNeedsTheTargetKey(t *testing.T) {
	t.Parallel()

	skipPOSIXFakesOnWindows(t)
	f := newAbsentSeatFixture(t)
	before := treeOf(t, f.storeDir)

	opts := SealOptions{
		StoreDir:        f.storeDir,
		AsName:          "new",
		KeyPath:         f.keyPath,
		SopsPath:        f.sopsPath,
		Name:            "GH_TOKEN",
		GitPath:         f.gitPath,
		GHPath:          f.ghPath,
		NoPR:            true,
		Stdin:           strings.NewReader("leakedvalue\n"),
		StdinIsTerminal: false,
		Now:             func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) },
		Check:           func(storeDir, asName, keyPath, sopsPath string) error { return nil },
	}
	line, err := RunSeal(opts)
	require.Error(t, err, "seal into an absent seat file with a key the rule does not name must refuse, not write the file")
	assert.Empty(t, line, "a refused seal prints no OK line: %s", line)
	assert.ErrorIs(t, err, errSeatFileAbsent, "the refusal is the typed not-exist refusal: %v", err)
	assert.Contains(t, err.Error(), "new.yaml does not exist in the store", "the refusal does not name the absent seat file: %v", err)
	assert.Contains(t, err.Error(), "a new seat is given its first values by seat add, never by seal", "the refusal does not name the verb that does this job: %v", err)
	assert.Contains(t, err.Error(), "(SPEC-SECRETS rule 12)", "the refusal does not name the rule it keeps: %v", err)
	assert.NotContains(t, err.Error(), "leakedvalue", "the refusal carries the value: %v", err)
	assert.NoFileExists(t, filepath.Join(f.storeDir, "new.yaml"), "the absent seat file was written by a refused seal")
	sameTree(t, "a refused seal into an absent seat file", before, treeOf(t, f.storeDir))
	assert.Equal(t, "", strings.TrimSpace(readMaybe(t, f.sopsArgs)), "a refused seal ran sops past the version probe:\n%s", readMaybe(t, f.sopsArgs))
	assert.Empty(t, readMaybe(t, f.gitArgs), "a refused seal ran git:\n%s", readMaybe(t, f.gitArgs))
	assert.Empty(t, readMaybe(t, f.ghArgs), "a refused seal called gh:\n%s", readMaybe(t, f.ghArgs))

	t.Run("the dry run refuses the same write", func(t *testing.T) {
		t.Parallel()

		f := newAbsentSeatFixture(t)
		before := treeOf(t, f.storeDir)
		dry := SealOptions{
			StoreDir:        f.storeDir,
			AsName:          "new",
			KeyPath:         f.keyPath,
			SopsPath:        f.sopsPath,
			Name:            "GH_TOKEN",
			GitPath:         f.gitPath,
			GHPath:          f.ghPath,
			NoPR:            true,
			DryRun:          true,
			Stdin:           untouchable{t},
			StdinIsTerminal: false,
			Now:             func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) },
			Check:           func(storeDir, asName, keyPath, sopsPath string) error { return nil },
		}
		plan, err := RunSeal(dry)
		require.Error(t, err, "the dry run planned the write the real run refuses")
		assert.Empty(t, plan, "a refused dry run prints no plan: %s", plan)
		assert.ErrorIs(t, err, errSeatFileAbsent, "the dry run's refusal is the typed not-exist refusal: %v", err)
		assert.Contains(t, err.Error(), "new.yaml does not exist in the store", "the dry run's refusal does not name the absent seat file: %v", err)
		assert.NoFileExists(t, filepath.Join(f.storeDir, "new.yaml"), "the dry run wrote the absent seat file")
		sameTree(t, "a refused seal --dry-run into an absent seat file", before, treeOf(t, f.storeDir))
		assert.Equal(t, "", strings.TrimSpace(readMaybe(t, f.sopsArgs)), "the refused dry run encrypted:\n%s", readMaybe(t, f.sopsArgs))
		git := readMaybe(t, f.gitArgs)
		for _, write := range []string{"checkout", "add", "commit", "push", "pull"} {
			assert.NotContains(t, git, write, "the refused dry run ran git %s:\n%s", write, git)
		}
		assert.Empty(t, readMaybe(t, f.ghArgs), "the refused dry run called gh:\n%s", readMaybe(t, f.ghArgs))
	})
}
