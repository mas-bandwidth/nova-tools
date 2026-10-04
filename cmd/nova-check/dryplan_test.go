package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

func checkRun(args ...string) testkit.Result {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return testkit.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
}

// A dry run is the real run's own plan: on a parent that is a file, a parent
// this process cannot write, a parent that is a link, and a target already
// there, spelling --write refuses with --dry-run exactly where it does without,
// and the dry run changes no byte.
func TestADryRunRefusesWhereTheWriteWould(t *testing.T) {
	t.Parallel()
	link := func(t *testing.T, root, at string) string {
		real := filepath.Join(root, "real")
		require.NoError(t, os.MkdirAll(real, 0o755))
		require.NoError(t, os.Symlink(real, at))
		return real
	}

	t.Run("spelling --write", func(t *testing.T) {
		t.Parallel()
		misspelled := func(t *testing.T, dir string) string {
			require.NoError(t, os.MkdirAll(dir, 0o755))
			p := filepath.Join(dir, "a.md")
			testkit.WriteFile(t, p, "the recieve step\n")
			return p
		}
		testkit.DryRunAgrees(t, checkRun, []testkit.DryCase{
			{Name: "parent is a file", Setup: func(t *testing.T, root string) ([]string, []string) {
				testkit.WriteFile(t, filepath.Join(root, "f"), "not a directory")
				return []string{"spelling"}, []string{"--dir", root, "--file", filepath.Join(root, "f", "a.md"), "--write"}
			}},
			{Name: "parent not writable", Unwrites: true, Setup: func(t *testing.T, root string) ([]string, []string) {
				dir := filepath.Join(root, "prose")
				misspelled(t, dir)
				testkit.Unwritable(t, dir)
				return []string{"spelling"}, []string{"--dir", dir, "--write"}
			}},
			{Name: "parent is a link", Setup: func(t *testing.T, root string) ([]string, []string) {
				at := filepath.Join(root, "prose")
				misspelled(t, link(t, root, at))
				return []string{"spelling"}, []string{"--dir", root, "--file", filepath.Join(at, "a.md"), "--write"}
			}},
			{Name: "a file to correct", Setup: func(t *testing.T, root string) ([]string, []string) {
				misspelled(t, filepath.Join(root, "prose"))
				return []string{"spelling"}, []string{"--dir", filepath.Join(root, "prose"), "--write"}
			}},
		})
	})
}
