package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

// fuseRun runs the tool with the tests' fixed clock.
func fuseRun(args ...string) testkit.Result {
	return fuseFixed.Run(args...)
}

// A dry run is the real run's own plan: on a parent that is a file, a parent
// this process cannot write, a parent that is a link, and a target already
// there, every verb that writes refuses or fails with --dry-run exactly where
// it does without, and the dry run changes no byte.
func TestADryRunRefusesWhereTheWriteWould(t *testing.T) {
	t.Parallel()
	const quarantined = `{"lockdown":null,"quarantine":{"s":{"at":"2026-09-09T18:27:40Z","reason":"r"}}}`
	writers := []struct {
		verb []string
		pos  []string
		box  bool // the verb needs a box already there
	}{
		{[]string{"init"}, nil, false},
		{[]string{"lockdown"}, []string{"why"}, true},
		{[]string{"quarantine"}, []string{"t", "why"}, true},
		{[]string{"lift", "quarantine"}, []string{"s"}, true},
	}
	for _, w := range writers {
		layout := func(t *testing.T, root string, parent func(dir string)) (verb, rest []string) {
			dir := filepath.Join(root, "p")
			parent(dir)
			return w.verb, append([]string{"--box", filepath.Join(dir, "box.json")}, w.pos...)
		}
		withBox := func(t *testing.T, dir string) {
			require.NoError(t, os.MkdirAll(dir, 0o755))
			if w.box {
				testkit.WriteFile(t, filepath.Join(dir, "box.json"), quarantined)
			}
		}
		t.Run(strings.Join(w.verb, " "), func(t *testing.T) {
			t.Parallel()
			testkit.DryRunAgrees(t, fuseRun, []testkit.DryCase{
				{Name: "parent is a file", Setup: func(t *testing.T, root string) ([]string, []string) {
					return layout(t, root, func(dir string) { testkit.WriteFile(t, dir, "not a directory") })
				}},
				{Name: "parent not writable", Unwrites: true, Setup: func(t *testing.T, root string) ([]string, []string) {
					return layout(t, root, func(dir string) { withBox(t, dir); testkit.Unwritable(t, dir) })
				}},
				{Name: "parent is a link", Setup: func(t *testing.T, root string) ([]string, []string) {
					return layout(t, root, func(dir string) {
						real := filepath.Join(root, "real")
						withBox(t, real)
						require.NoError(t, os.Symlink(real, dir))
					})
				}},
				{Name: "target already there", Setup: func(t *testing.T, root string) ([]string, []string) {
					return layout(t, root, func(dir string) {
						withBox(t, dir)
						if !w.box {
							testkit.WriteFile(t, filepath.Join(dir, "box.json"), quarantined)
						}
					})
				}},
				{Name: "target is a directory", Setup: func(t *testing.T, root string) ([]string, []string) {
					return layout(t, root, func(dir string) { require.NoError(t, os.MkdirAll(filepath.Join(dir, "box.json"), 0o755)) })
				}},
				{Name: "parent missing", Setup: func(t *testing.T, root string) ([]string, []string) {
					return layout(t, root, func(string) {})
				}},
			})
		})
	}
}
