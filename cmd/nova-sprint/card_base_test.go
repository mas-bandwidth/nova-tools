package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCardBaseReplacesBaseForMergingCardAndRefusesNotOnOrigin(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	briefs := t.TempDir()
	brief := func(id, base string) string {
		path := filepath.Join(briefs, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+base+"\n\nWrite "+id+".txt.")), 0o600))
		return path
	}
	r.ok("add --stream s1 --one --brief-file " + brief("s1-1", "old-branch"))

	// Unstarted card: not merging
	code, _, errs := r.do("card base s1-1 main")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "card s1-1 is not merging")

	heads := map[string]string{
		"s1-1": r.head("s1-1", "main", "s1-1.txt", "one\n"),
	}
	r.queued(heads, "s1-1")

	// Merging card, but target branch is not on origin
	code, _, errs = r.do("card base s1-1 branch-does-not-exist")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "the branch branch-does-not-exist is not on origin")

	// Merging card, target branch main is on origin
	out := r.ok("card base s1-1 main")
	assert.Contains(t, out, "card s1-1 base -> main")

	// Log records the move
	log := r.ok("log --card s1-1")
	assert.Contains(t, log, "card s1-1 base -> main")

	// The work and reads are kept
	places := r.places("s1-1")
	assert.Equal(t, "merging/queued", places["s1-1"])
}
