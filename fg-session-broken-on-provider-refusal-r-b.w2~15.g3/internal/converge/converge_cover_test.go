// Unit coverage for the PlanSave path the per-function coverage table showed
// at zero: the plan of the state write, which makes every check the write
// makes and writes nothing. Everything runs in-process: the main path is a
// path in the test's own temporary directory, and the refusals are a parent
// that is not there and a target that is a directory, both of which the file
// system refuses before anything could be written. No sleeps, no real time,
// no network, no subprocess, no Redis or Postgres.
package converge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvergeCoverPlanSavePlansAWriteItCouldMake(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rows := []struct {
		name string
		path string
	}{
		{name: "an empty path plans nothing", path: ""},
		{name: "a blank path plans nothing", path: "   "},
		{name: "a path in an existing directory is planned", path: filepath.Join(dir, "state.json")},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			err := PlanSave(row.path)
			assert.NoError(t, err, "a plan over a writable parent must answer nil, got %v", err)
			assert.NoFileExists(t, row.path, "a plan writes nothing, yet something stands at %q", row.path)
		})
	}
}

func TestConvergeCoverPlanSaveRefusesWhereTheWriteWouldRefuse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "target-dir"), 0o755), "the refusal fixture needs a directory to plan at")

	rows := []struct {
		name string
		path string
		want string
	}{
		{
			name: "a parent that is not there is refused before anything could be written",
			path: filepath.Join(dir, "no-such-dir", "state.json"),
			want: "atomicfile: parent directory for",
		},
		{
			name: "a target that is a directory is refused",
			path: filepath.Join(dir, "target-dir"),
			want: "atomicfile: target",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			err := PlanSave(row.path)
			require.Error(t, err, "a plan must refuse where the write would")
			assert.Contains(t, err.Error(), row.want, "the refusal names what it refused")
			assert.Contains(t, err.Error(), row.path, "the refusal names the path it judged")
		})
	}
}
