package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
)

// gitFails runs git in dir and returns its error: a git the test expects to fail.
func gitFails(r *landRig, dir string, args ...string) error {
	_, err := gitrun.Run(context.Background(), gitrun.Options{C: dir, Env: r.env, OwnRepo: true}, args...)
	return err
}

// The lander owns the clone it keeps under its root: a pass cut short there (a modified
// file, a merge stopped on a conflict, a stray file) is restored to the fetched base before
// the batch, said on one LAND CLEANED line naming the files, and the batch lands. A clone
// the caller gives with --repo-dir is the caller's: a dirty one is refused as it was and
// left as it is (docs/SPEC-SPRINT.md, section 7, land-clone-self-heals-r.w1).
func TestLanderRestoresItsOwnDirtyCacheClone(t *testing.T) {
	t.Parallel()
	t.Run("its own cache clone is restored and the batch lands", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		kept := filepath.Join(r.dir, "land", repoDirName(r.remote))
		require.NoError(t, os.MkdirAll(filepath.Dir(kept), 0o755))
		r.git("", "clone", "-q", r.remote, kept)
		// a pass cut short: a local commit, a half merge stopped on README with side.txt
		// staged, and a stray file
		r.git(kept, "switch", "-q", "-c", "side")
		require.NoError(t, os.WriteFile(filepath.Join(kept, "README"), []byte("side\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(kept, "side.txt"), []byte("side\n"), 0o600))
		r.git(kept, "add", "README", "side.txt")
		r.git(kept, "commit", "-q", "-m", "side")
		r.git(kept, "switch", "-q", "main")
		require.NoError(t, os.WriteFile(filepath.Join(kept, "README"), []byte("mine\n"), 0o600))
		r.git(kept, "commit", "-q", "-am", "mine")
		require.Error(t, gitFails(r, kept, "merge", "--no-edit", "side"), "the merge stops on README")
		require.NoError(t, os.WriteFile(filepath.Join(kept, "stray.txt"), []byte("stray\n"), 0o600))
		r.git(kept, "rev-parse", "--verify", "MERGE_HEAD")

		briefs := t.TempDir()
		path := filepath.Join(briefs, "a.md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite a.txt.")), 0o600))
		r.promotionStream("s1") // the card is cut on main, which the promotion stream alone takes
		r.ok("add --stream s1 a --one --brief-file " + path)
		r.queued(map[string]string{"a": r.head("a", "main", "a.txt", "a\n")}, "a")

		out := r.ok("land")
		assert.Contains(t, out, "LAND CLEANED stream=s1 dir="+kept+" files=3 paths=README,side.txt,stray.txt")
		assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
		assert.Less(t, strings.Index(out, "LAND CLEANED"), strings.Index(out, "LAND OK"), "the clean comes before the batch")
		assert.Equal(t, []string{"land a (sprint stream s1)", "base"}, r.mainLog())
		assert.Equal(t, map[string]string{"a": "landed/merged"}, r.places("a"))
		assert.Error(t, gitFails(r, kept, "rev-parse", "--verify", "-q", "MERGE_HEAD"), "the merge in progress was aborted")
		assert.NoFileExists(t, filepath.Join(kept, "stray.txt"))
		assert.Empty(t, r.git(kept, "status", "--porcelain"))
	})
	t.Run("a dirty --repo-dir clone is refused and left as it is", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		r.ok("add --stream s1 --count 1 --one")
		r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
		require.NoError(t, os.WriteFile(filepath.Join(r.clone, "README"), []byte("mine\n"), 0o600))
		before := r.git(r.remote, "rev-parse", "main")
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		assert.Equal(t, 1, code)
		assert.Contains(t, errs, "the clone "+r.clone+" is not clean")
		assert.NotContains(t, out+errs, "LAND CLEANED")
		assert.Equal(t, "M README", r.git(r.clone, "status", "--porcelain"))
		assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
		assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
	})
}

// The lander leaves its clone clean after every outcome: a head that does not merge is
// aborted, no MERGE_HEAD and no change left, and the next batch in that clone lands. One
// land at a time works in a clone: a land that finds another holding the clone's land lock
// (two landers, the server's loop and a hand land, once shared a clone, and the second's
// merge found the first's MERGE_HEAD and failed in git, 2026-10-06 18:14 ET) refuses
// before any git touches it, and the cards stay queued for the next land
// (docs/SPEC-SPRINT.md section 7, the land clone).
func TestTheLandCloneIsCleanAfterAFailedMerge(t *testing.T) {
	t.Parallel()
	t.Run("a conflicting merge leaves the clone clean and the next batch lands", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		r.ok("add --stream s1 --count 2")
		heads := map[string]string{
			"s1-1": r.head("s1-1", "main", "a.txt", "one\n"),
			"s1-2": r.head("s1-2", "main", "a.txt", "two\n"),
		}
		r.queued(heads, "s1-1", "s1-2")
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		assert.Equal(t, 1, code, out+errs)
		assert.Contains(t, errs, "of s1-2 does not merge")
		assert.Error(t, gitFails(r, r.clone, "rev-parse", "--verify", "-q", "MERGE_HEAD"), "no merge is left in progress")
		assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "no change is left in the clone")

		// s1-2 is reworked at the tip, ready: off the table, so the deal below takes s2-1
		r.ok("drop s1-2 --reason 'not this test'")
		r.ok("add --stream s2 --count 1 --one")
		r.queued(map[string]string{"s2-1": r.head("s2-1", "main", "b.txt", "b\n")}, "s2-1")
		out = r.ok("land --repo-dir " + r.clone + " --base main --stream s2")
		assert.Contains(t, out, "LAND OK stream=s2 cards=1 base=main")
		assert.Equal(t, map[string]string{"s2-1": "landed/merged"}, r.places("s2-1"))
	})
	t.Run("a land beside another in the same clone touches nothing", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		r.ok("add --stream s1 --count 1 --one")
		r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
		other, err := filelock.TryLock(filepath.Join(r.clone, ".git", landLockName), "the other land")
		require.NoError(t, err)
		headBefore, base := r.git(r.clone, "rev-parse", "HEAD"), r.git(r.remote, "rev-parse", "main")
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		assert.Equal(t, 1, code, out+errs)
		assert.Contains(t, errs, "the clone "+r.clone+" is in use by another land")
		assert.Contains(t, errs, "the other land")
		assert.Equal(t, headBefore, r.git(r.clone, "rev-parse", "HEAD"), "the clone is not touched")
		assert.Equal(t, "main", r.git(r.clone, "branch", "--show-current"), "no batch branch is cut")
		assert.Equal(t, base, r.git(r.remote, "rev-parse", "main"))
		assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))

		require.NoError(t, other.Unlock())
		out = r.ok("land --repo-dir " + r.clone + " --base main")
		assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
	})
}
