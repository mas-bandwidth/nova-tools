package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
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
