// Unit coverage for the two ExecSource methods the per-function coverage table
// showed at zero: Changed and Packages. Both are the exec seam itself, each one
// child (git, go) run through readChild; the package's injectable seam for their
// callers is Source, and the fake that fills it is already tested. Everything
// here runs in-process. The refusal is reached through a context whose deadline
// is a fixed instant already behind it, so exec.Cmd.Start returns before it
// forks and no child starts. The success paths need a real checkout and a real
// go toolchain and belong to the functional tier
// (TestATransitiveChangeRebuildsTheTool); they are not reached here: no sleeps,
// no real time, no network, no subprocess, no Redis or Postgres.
package release

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// incrementalCoverPastDeadline is a context whose deadline is a fixed instant
// already behind it, so a child it would start is refused before it forks and
// the refusal is the deadline's, with no clock read and nothing to sleep on.
func incrementalCoverPastDeadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0).UTC())
	t.Cleanup(cancel)
	return ctx
}

func TestIncrementalCoverChangedRefusesWhenTheDeadlineHasPassed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rows := []struct {
		name, base, head string
	}{
		{name: "a range of two recorded commits", base: "c1", head: "c2"},
		{name: "an empty base", base: "", head: "HEAD"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got, err := ExecSource{}.Changed(incrementalCoverPastDeadline(t), dir, row.base, row.head)
			assert.Nil(t, got, "a refusal carries no paths, got %v", got)
			require.Error(t, err, "Changed must refuse a deadline already passed")
			assert.Contains(t, err.Error(), "git -C "+dir+" diff --name-only --no-renames",
				"the refusal names the tree diff and the checkout it ran in")
			assert.Contains(t, err.Error(), "context deadline exceeded",
				"the refusal carries the child's own reason")
		})
	}
}

func TestIncrementalCoverPackagesRefusesWhenTheDeadlineHasPassed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rows := []struct {
		name string
		pkgs []string
	}{
		{name: "one tool", pkgs: []string{"./cmd/nova-bus"}},
		{name: "two tools", pkgs: []string{"./cmd/nova-bus", "./cmd/nova-update"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got, err := ExecSource{}.Packages(incrementalCoverPastDeadline(t), dir, "linux", "amd64", row.pkgs)
			assert.Nil(t, got, "a refusal carries no packages, got %v", got)
			require.Error(t, err, "Packages must refuse a deadline already passed")
			assert.Contains(t, err.Error(), "go list -deps",
				"the refusal names the go list ask")
			assert.Contains(t, err.Error(), "context deadline exceeded",
				"the refusal carries the child's own reason")
		})
	}
}
