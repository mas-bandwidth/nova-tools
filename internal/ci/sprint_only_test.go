package ci

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sprintOnlyPaths are nova-sprint's paths: the opinionated system that left this
// tree for a repository of its own (mas-bandwidth/nova-sprint, the split,
// v1.2.3), which imports this module's building blocks and is imported by none
// of them. None of them exists here; TestNovaSprintPathsAreNotInThisTree holds
// that.
var sprintOnlyPaths = []string{
	"cmd/nova-sprint", "cmd/nova-card", "cmd/nova-work",
	"internal/sprint", "internal/sprintdash", "internal/card", "internal/cardgen",
	"internal/workfile", "internal/workgh", "internal/worklang",
	"tools/sprintsize",
}

// sprintOnly is whether the repo-relative path rel is in one of sprintOnlyPaths.
func sprintOnly(rel string) bool {
	for _, p := range sprintOnlyPaths {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// sprintLeftMessage is the refusal, said once, where a person meets it.
const sprintLeftMessage = "nova-sprint lives in mas-bandwidth/nova-sprint; open this change there"

// sprintPathsIn is every sprint-only path present in the tree at root: a file or
// a directory, tracked or not, because a direct push to dev is red in CI the
// same as a pull request. It walks nothing; each path is one Lstat.
func sprintPathsIn(root string) ([]string, error) {
	var present []string
	for _, p := range sprintOnlyPaths {
		_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		switch {
		case err == nil:
			present = append(present, p)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
	}
	return present, nil
}

// TestNovaSprintPathsAreNotInThisTree is layer L6 of the split: nova-sprint,
// nova-card and nova-work and the packages only they import live in
// mas-bandwidth/nova-sprint, and a change that brings one of their paths back
// here is red, by pull request or by direct push. It supersedes the lint job's
// diff step of #5535, which read only a pull request's diff.
func TestNovaSprintPathsAreNotInThisTree(t *testing.T) {
	t.Parallel()

	present, err := sprintPathsIn(repoRoot(t))
	require.NoError(t, err)
	refuseSprintPaths(t, present)
}

// refuseSprintPaths is the guard's one assertion: no sprint-only path is present,
// else a refusal naming them and where the change belongs.
func refuseSprintPaths(t assert.TestingT, present []string) bool {
	return assert.Empty(t, present, sprintLeftMessage)
}

// TestTheSprintGuardRefusesAPlantedPath is the guard's reversed witness: a
// temporary tree holding one sprint-only path, as a file deep under it, is
// refused with the message; the same tree without it, and with the paths'
// near neighbours that stay (internal/cardhdr, cmd/nova-swarm), passes.
func TestTheSprintGuardRefusesAPlantedPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, stay := range []string{"internal/cardhdr/hdr.go", "internal/cardtree/tree.go", "cmd/nova-swarm/main.go", "internal/sprinter.go"} {
		path := filepath.Join(root, filepath.FromSlash(stay))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("package x\n"), 0o644))
	}
	present, err := sprintPathsIn(root)
	require.NoError(t, err)
	assert.Empty(t, present, "a clean tree is refused")

	planted := filepath.Join(root, "internal", "sprint", "store", "store.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(planted), 0o755))
	require.NoError(t, os.WriteFile(planted, []byte("package store\n"), 0o644))
	present, err = sprintPathsIn(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/sprint"}, present, "the planted path is not refused")

	ft := &guardT{}
	refuseSprintPaths(ft, present)
	assert.True(t, ft.failed, "the guard's assertion passes a tree holding internal/sprint")
	assert.Contains(t, ft.msg, sprintLeftMessage, "the refusal does not say where the change belongs")
}

// guardT records what an assertion says without failing the test running it.
type guardT struct {
	failed bool
	msg    string
}

func (f *guardT) Errorf(format string, args ...any) {
	f.failed = true
	f.msg = fmt.Sprintf(format, args...)
}
