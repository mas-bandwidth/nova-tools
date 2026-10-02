package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
// there, each verb that writes (dogfood record, spelling --write, convergence
// --state) refuses with --dry-run exactly where it does without, and the dry
// run changes no byte. A receipt's name is new for every receipt (its stamp
// and its sum), so dogfood record has no target already there to meet.
func TestADryRunRefusesWhereTheWriteWould(t *testing.T) {
	t.Parallel()
	link := func(t *testing.T, root, at string) string {
		real := filepath.Join(root, "real")
		require.NoError(t, os.MkdirAll(real, 0o755))
		require.NoError(t, os.Symlink(real, at))
		return real
	}

	t.Run("dogfood record", func(t *testing.T) {
		t.Parallel()
		record := func(t *testing.T, root string, receipts func(string)) ([]string, []string) {
			dir := filepath.Join(root, "receipts")
			receipts(dir)
			return []string{"dogfood", "record"}, []string{"--cli", writeCLI(t, root), "--tool", "nova-example", "--verb", "links",
				"--by", "Ada", "--ok", "--notes", "ran it on real work", "--receipts", dir}
		}
		testkit.DryRunAgrees(t, checkRun, []testkit.DryCase{
			{Name: "parent is a file", Setup: func(t *testing.T, root string) ([]string, []string) {
				return record(t, root, func(dir string) { testkit.WriteFile(t, dir, "not a directory") })
			}},
			{Name: "parent not writable", Unwrites: true, Setup: func(t *testing.T, root string) ([]string, []string) {
				return record(t, root, func(dir string) { require.NoError(t, os.MkdirAll(dir, 0o755)); testkit.Unwritable(t, dir) })
			}},
			{Name: "parent is a link", Setup: func(t *testing.T, root string) ([]string, []string) {
				return record(t, root, func(dir string) { link(t, root, dir) })
			}},
			{Name: "parent not there", Setup: func(t *testing.T, root string) ([]string, []string) {
				return record(t, root, func(string) {})
			}},
		})
	})

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

	t.Run("convergence --state", func(t *testing.T) {
		t.Parallel()
		f := newConvFixture(t)
		state := func(at func(root string) string) func(*testing.T, string) ([]string, []string) {
			return func(t *testing.T, root string) ([]string, []string) {
				return f.args[:1], append(append([]string{}, f.args[1:]...), "--state", at(root))
			}
		}
		testkit.DryRunAgrees(t, checkRun, []testkit.DryCase{
			{Name: "parent is a file", Setup: state(func(root string) string {
				testkit.WriteFile(t, filepath.Join(root, "p"), "not a directory")
				return filepath.Join(root, "p", "state.json")
			})},
			{Name: "parent not writable", Unwrites: true, Setup: state(func(root string) string {
				dir := filepath.Join(root, "p")
				require.NoError(t, os.MkdirAll(dir, 0o755))
				testkit.Unwritable(t, dir)
				return filepath.Join(dir, "state.json")
			})},
			{Name: "parent is a link", Setup: state(func(root string) string {
				at := filepath.Join(root, "p")
				link(t, root, at)
				return filepath.Join(at, "state.json")
			})},
			{Name: "target is a directory", Setup: state(func(root string) string {
				p := filepath.Join(root, "state.json")
				require.NoError(t, os.MkdirAll(p, 0o755))
				return p
			})},
			{Name: "parent not there", Setup: state(func(root string) string { return filepath.Join(root, "absent", "state.json") })},
			{Name: "name too long", Setup: state(func(root string) string { return filepath.Join(root, strings.Repeat("s", 242)) })},
			{Name: "state is a dangling link", Setup: state(func(root string) string {
				p := filepath.Join(root, "state.json")
				require.NoError(t, os.Symlink(filepath.Join(root, "nowhere", "state.json"), p))
				return p
			})},
		})
	})
}
