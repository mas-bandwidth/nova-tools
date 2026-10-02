package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

// A dry run is the real run's own plan: on a parent that is a file, a parent
// this process cannot write, a parent that is a link, and a target already
// there, open and append refuse with --dry-run exactly where they do without,
// and the dry run changes no byte. The parent is the directory the verb writes
// its file into: sessions/ for open, entries/<session>/ for append.
func TestADryRunRefusesWhereTheWriteWould(t *testing.T) {
	t.Parallel()
	// The store each verb starts from: open's is there and empty; append's holds
	// session s with an entry and session t with none, the one it appends to.
	stores := map[string]func(t *testing.T, store string){
		"open": func(t *testing.T, store string) { require.NoError(t, os.MkdirAll(store, 0o755)) },
		"append": func(t *testing.T, store string) {
			cli.OK(t, "open", "--store", store, "--session", "s", "--publish", "manual")
			cli.OK(t, "append", "--store", store, "--session", "s", "--entry", "first", "--text", "w")
			cli.OK(t, "open", "--store", store, "--session", "t", "--publish", "manual")
		},
	}
	verbs := []struct {
		name   string
		parent func(store string) string
		args   func(store string) (verb, rest []string)
		target string
	}{
		{"open", func(store string) string { return filepath.Join(store, "sessions") },
			func(store string) ([]string, []string) {
				return []string{"open"}, []string{"--store", store, "--session", "new", "--publish", "manual"}
			}, "new.md"},
		{"append", func(store string) string { return filepath.Join(store, "entries", "t") },
			func(store string) ([]string, []string) {
				return []string{"append"}, []string{"--store", store, "--session", "t", "--entry", "e", "--text", "w"}
			}, "e.json"},
	}
	for _, v := range verbs {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			at := func(change func(t *testing.T, store, parent string)) func(*testing.T, string) ([]string, []string) {
				return func(t *testing.T, root string) ([]string, []string) {
					store := filepath.Join(root, "store")
					stores[v.name](t, store)
					change(t, store, v.parent(store))
					return v.args(store)
				}
			}
			testkit.DryRunAgrees(t, cli.Run, []testkit.DryCase{
				{Name: "parent is a file", Setup: at(func(t *testing.T, _, parent string) {
					testkit.WriteFile(t, parent, "not a directory")
				})},
				{Name: "parent not writable", Unwrites: true, Setup: at(func(t *testing.T, _, parent string) {
					require.NoError(t, os.MkdirAll(parent, 0o755))
					testkit.Unwritable(t, parent)
				})},
				{Name: "parent is a link", Setup: at(func(t *testing.T, store, parent string) {
					real := filepath.Join(store, "..", "real")
					require.NoError(t, os.MkdirAll(real, 0o755))
					require.NoError(t, os.Symlink(real, parent))
				})},
				{Name: "target is a directory", Setup: at(func(t *testing.T, _, parent string) {
					require.NoError(t, os.MkdirAll(filepath.Join(parent, v.target), 0o755))
				})},
				{Name: "store is a file", Setup: func(t *testing.T, root string) ([]string, []string) {
					store := filepath.Join(root, "store")
					testkit.WriteFile(t, store, "not a directory")
					return v.args(store)
				}},
				{Name: "store not there", Setup: func(t *testing.T, root string) ([]string, []string) {
					return v.args(filepath.Join(root, "store"))
				}},
			})
		})
	}
}
