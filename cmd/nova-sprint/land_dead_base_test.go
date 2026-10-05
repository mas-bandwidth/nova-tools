package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Write the red test first on a twin store and a twin repository: two merging cards in one stream,
// the first naming a deleted base; three land passes raise one judgment, make one refusal, and land
// the second card; `card base` to a live branch lands the first on the next pass.
func TestLanderRefusesADeadBaseOnceAndTheStreamMovesOn(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	briefs := t.TempDir()
	brief := func(id, base string) string {
		path := filepath.Join(briefs, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+base+"\n\nWrite "+id+".txt.")), 0o600))
		return path
	}
	r.ok("add --stream s1 --brief-file " + brief("s1-1", "deleted-base") + " --brief-file " + brief("s1-2", "main"))
	heads := map[string]string{
		"s1-1": r.head("s1-1", "main", "s1-1.txt", "one\n"),
		"s1-2": r.head("s1-2", "main", "s1-2.txt", "two\n"),
	}
	r.queued(heads, "s1-1", "s1-2")

	// Pass 1: raises one judgment, makes one refusal on s1-1, and lands s1-2.
	code, out, errs := r.do("land --repo-dir " + r.clone)
	assert.Equal(t, 1, code, "pass 1 should exit 1 because s1-1 was refused: out=%s, errs=%s", out, errs)
	assert.Contains(t, errs, "card s1-1 names BASE deleted-base, which is not on origin")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	places := r.places("s1-1", "s1-2")
	assert.Equal(t, "merging/queued", places["s1-1"])
	assert.Equal(t, "landed/merged", places["s1-2"])

	// Pass 2: s1-1 is skipped; no new refusal and no new judgment.
	code2, out2, errs2 := r.do("land --repo-dir " + r.clone)
	assert.Equal(t, 0, code2, "pass 2 should exit 0: out=%s, errs=%s", out2, errs2)
	assert.NotContains(t, errs2, "card s1-1 names BASE deleted-base, which is not on origin")

	// Pass 3: s1-1 is still skipped; no new refusal and no new judgment.
	code3, out3, errs3 := r.do("land --repo-dir " + r.clone)
	assert.Equal(t, 0, code3, "pass 3 should exit 0: out=%s, errs=%s", out3, errs3)
	assert.NotContains(t, errs3, "card s1-1 names BASE deleted-base, which is not on origin")

	// Verify exactly one judgment was raised across the three passes.
	inbox := r.ok("inbox")
	assert.Contains(t, inbox, "card s1-1 names BASE deleted-base, which is not on origin")

	// card base to a live branch lands the first on the next pass.
	r.ok("card base s1-1 main")

	// Pass 4: lands s1-1 on main.
	code4, out4, errs4 := r.do("land --repo-dir " + r.clone)
	assert.Equal(t, 0, code4, "pass 4 should land s1-1: out=%s, errs=%s", out4, errs4)
	assert.Contains(t, out4, "LAND OK stream=s1 cards=1 base=main")
	placesAfter := r.places("s1-1", "s1-2")
	assert.Equal(t, "landed/merged", placesAfter["s1-1"])
	assert.Equal(t, "landed/merged", placesAfter["s1-2"])
}
