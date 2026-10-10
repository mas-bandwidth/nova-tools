/*
Tests for the plan verbs: PlanCreateBox and the planBox it shares with the write plan.
A plan is WriteBox or CreateBox with nothing written, so every test here pins two
things at once: the answer (nil or the refusal) and that nothing appeared at the
path. The refusals are the ones a --dry-run of nova-fuse must print before any
write is attempted, so they are reached here without writing a byte.
*/
package fuse

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// TestFuseCoverPlanCreateBoxPlansCreationAndRefusesOccupied: PlanCreateBox plans
// nil where CreateBox would create (absent target, existing or missing parent) and
// refuses with fs.ErrExist where CreateBox would refuse, writing nothing either way.
func TestFuseCoverPlanCreateBoxPlansCreationAndRefusesOccupied(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      func(t *testing.T) string
		wantErr   error
		existedAs string
	}{
		{
			name: "absent target in an existing directory plans nil",
			path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "box.json") },
		},
		{
			name: "absent parent is judged as MkdirAll would make it",
			path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "new", "deeper", "box.json") },
		},
		{
			name: "an occupied path is refused, creation never replaces",
			path: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "box.json")
				require.NoError(t, os.WriteFile(path, []byte("occupied"), 0o644))
				return path
			},
			wantErr:   fs.ErrExist,
			existedAs: "occupied",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := tt.path(t)
			err := PlanCreateBox(path)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr, "PlanCreateBox(%s) = %v, want %v", path, err, tt.wantErr)
			} else {
				assert.NoError(t, err, "PlanCreateBox(%s) refused a creation it should plan", path)
			}
			if tt.existedAs != "" {
				kept, rerr := os.ReadFile(path)
				require.NoError(t, rerr)
				assert.Equal(t, tt.existedAs, string(kept), "PlanCreateBox replaced the box it was only asked to plan")
				return
			}
			assert.NoFileExists(t, path, "PlanCreateBox wrote at the path it was only asked to plan")
		})
	}
}

// TestFuseCoverPlanWriteBoxPlansReplacementAndRefusesSymlink: PlanWriteBox plans
// nil where WriteBox would write (absent target, replaceable regular file) and
// refuses where WriteBox refuses: a symlink target is not followed, an empty path
// is not a box location.
func TestFuseCoverPlanWriteBoxPlansReplacementAndRefusesSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege is not available on all Windows runners")
	}

	tests := []struct {
		name      string
		path      func(t *testing.T) string
		errSubstr string
		heldAs    string
	}{
		{
			name: "absent target plans nil",
			path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "box.json") },
		},
		{
			name: "an existing regular file plans nil, a write may replace it",
			path: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "box.json")
				require.NoError(t, os.WriteFile(path, []byte("old box"), 0o644))
				return path
			},
			heldAs: "old box",
		},
		{
			name: "a symlink target is refused and not followed",
			path: func(t *testing.T) string {
				root := t.TempDir()
				real := filepath.Join(root, "real.json")
				require.NoError(t, os.WriteFile(real, []byte("real box"), 0o644))
				link := filepath.Join(root, "link.json")
				require.NoError(t, os.Symlink(real, link))
				return link
			},
			errSubstr: "is a symlink",
		},
		{
			name:      "an empty path is refused",
			path:      func(t *testing.T) string { return "" },
			errSubstr: "path is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := tt.path(t)
			err := planBox(path)
			if tt.errSubstr != "" {
				assert.ErrorContains(t, err, tt.errSubstr, "planBox(%q) = %v, want a refusal naming %q", path, err, tt.errSubstr)
				return
			}
			assert.NoError(t, err, "planBox(%q) refused a write it should plan", path)
			if tt.heldAs != "" {
				kept, rerr := os.ReadFile(path)
				require.NoError(t, rerr)
				assert.Equal(t, tt.heldAs, string(kept), "PlanWriteBox replaced the box it was only asked to plan")
				return
			}
			assert.NoFileExists(t, path, "PlanWriteBox wrote at the path it was only asked to plan")
		})
	}
}

// TestFuseCoverPlanBoxRefusesForBothVerbs: planBox is the check both plan verbs
// share, so one refusal table holds for either option set: NoReplace refuses an
// occupied path and a plain plan does not, both refuse a symlink parent and a
// parent chain a file blocks, as the writes behind them would.
func TestFuseCoverPlanBoxRefusesForBothVerbs(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege is not available on all Windows runners")
	}

	root := t.TempDir()
	real := filepath.Join(root, "real")
	require.NoError(t, os.Mkdir(real, 0o755))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(real, link))
	blocked := filepath.Join(root, "blocker.json")
	require.NoError(t, os.WriteFile(blocked, []byte("a file, not a directory"), 0o644))

	tests := []struct {
		name      string
		path      string
		noReplace bool
		errSubstr string
	}{
		{name: "empty path is refused with either option set", path: "", errSubstr: "path is empty"},
		{name: "empty path is refused under NoReplace", path: "", noReplace: true, errSubstr: "path is empty"},
		{name: "symlink parent is refused and not followed", path: filepath.Join(link, "box.json"), errSubstr: "is a symlink"},
		{name: "symlink parent is refused under NoReplace", path: filepath.Join(link, "box.json"), noReplace: true, errSubstr: "is a symlink"},
		{name: "a file on the parent way is the not-a-directory refusal MkdirAll returns", path: filepath.Join(blocked, "box.json"), errSubstr: "not a directory"},
		{name: "a file on the parent way is refused under NoReplace", path: filepath.Join(blocked, "box.json"), noReplace: true, errSubstr: "not a directory"},
		{name: "an occupied path plans nil for a replacing write", path: blocked},
		{name: "an occupied path is refused for a creating write", path: blocked, noReplace: true, errSubstr: "file already exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var err error
			if tt.noReplace {
				err = planBox(tt.path, atomicfile.NoReplace())
			} else {
				err = planBox(tt.path)
			}
			if tt.errSubstr != "" {
				assert.ErrorContains(t, err, tt.errSubstr, "planBox(%q, noReplace=%t) = %v, want a refusal naming %q", tt.path, tt.noReplace, err, tt.errSubstr)
				return
			}
			assert.NoError(t, err, "planBox(%q, noReplace=%t) refused a write it should plan", tt.path, tt.noReplace)
		})
	}
	assert.DirExists(t, real, "planBox followed the symlink parent and touched the real directory")
}
