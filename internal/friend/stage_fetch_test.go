package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fetchFakeGit is a git that answers the mirror layout's reads as a converted mirror does
// and fails the fetch as told: the first fetches with git's "cannot lock ref" refusal (the
// fault of 2026-10-07, two fetches of one mirror at once), the rest as fetchErr says.
type fetchFakeGit struct {
	argv     [][]string
	lockRefs int // fetches that fail with "cannot lock ref"
	fetchErr error
}

func (g *fetchFakeGit) run(_ context.Context, _ time.Duration, args ...string) (string, error) {
	g.argv = append(g.argv, args)
	switch {
	case hasWord(args, "--get") && hasWord(args, "remote.origin.url"):
		return GitHubURL("o/n"), nil
	case hasWord(args, "--get-all"):
		return strings.Join(mirrorFetch, "\n"), nil
	case hasWord(args, "fetch"):
		if g.lockRefs > 0 {
			g.lockRefs--
			return "", errors.New("git fetch: exit status 1: error: cannot lock ref 'refs/remotes/origin/sprint/x': is at 1111111 but expected 2222222")
		}
		return "", g.fetchErr
	}
	return "", nil
}

func hasWord(args []string, word string) bool {
	for _, a := range args {
		if a == word {
			return true
		}
	}
	return false
}

func (g *fetchFakeGit) fetches() int {
	n := 0
	for _, a := range g.argv {
		if hasWord(a, "fetch") {
			n++
		}
	}
	return n
}

func (g *fetchFakeGit) deleted() []string {
	var refs []string
	for _, a := range g.argv {
		if len(a) >= 5 && a[2] == "update-ref" && a[3] == "-d" {
			refs = append(refs, a[4])
		}
	}
	return refs
}

func fetchStager(t *testing.T, g *fetchFakeGit) (*Stager, string) {
	t.Helper()
	s := &Stager{Dir: t.TempDir(), Git: g.run}
	mirror := filepath.Join(s.Dir, MirrorsDir, "o", "n.git")
	require.NoError(t, os.MkdirAll(mirror, 0o755))
	return s, mirror
}

// A fetch git refuses for a ref it cannot lock has that ref deleted and is run once more:
// the mirror is fetched, the lock file beside it taken for the fetch.
func TestAFetchRefusedForARefItCannotLockDeletesTheRefAndFetchesOnceMore(t *testing.T) {
	t.Parallel()
	g := &fetchFakeGit{lockRefs: 1}
	s, mirror := fetchStager(t, g)
	got, err := s.mirror(context.Background(), "o/n")
	require.NoError(t, err)
	assert.Equal(t, mirror, got)
	assert.Equal(t, 2, g.fetches(), "the fetch, refused, and the fetch again")
	assert.Equal(t, []string{"refs/remotes/origin/sprint/x"}, g.deleted(), "the ref git could not lock, deleted between them")
	_, err = os.Stat(mirror + ".fetch.lock")
	assert.NoError(t, err, "the mirror's fetch lock is a file beside it")
}

// A fetch refused the same way twice is refused: the ref deleted once, no third fetch, and
// the stage says the repository could not be fetched.
func TestAFetchRefusedTwiceForARefIsRefused(t *testing.T) {
	t.Parallel()
	g := &fetchFakeGit{lockRefs: 2}
	s, _ := fetchStager(t, g)
	_, err := s.mirror(context.Background(), "o/n")
	var ns *NotStageable
	require.ErrorAs(t, err, &ns)
	assert.Contains(t, ns.Why, "cannot lock ref 'refs/remotes/origin/sprint/x'")
	assert.Equal(t, 2, g.fetches())
	assert.Len(t, g.deleted(), 1)
}

// Any other fetch failure is refused as before, with no ref deleted.
func TestAnotherFetchFailureIsRefusedWithNoRefDeleted(t *testing.T) {
	t.Parallel()
	g := &fetchFakeGit{fetchErr: errors.New("git fetch: exit status 128: fatal: could not read from remote repository")}
	s, _ := fetchStager(t, g)
	_, err := s.mirror(context.Background(), "o/n")
	var ns *NotStageable
	require.ErrorAs(t, err, &ns)
	assert.Contains(t, ns.Why, "could not read from remote repository")
	assert.Equal(t, 1, g.fetches())
	assert.Empty(t, g.deleted())
}
