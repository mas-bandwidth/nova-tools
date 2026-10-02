//go:build functional

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dry run is the real run's own path with the writes left out: for every verb that takes
// --dry-run, the dry form and the real form run on identical copies of one bus, and they
// refuse alike (the same exit, the same lines), and a dry run changes nothing on disk --
// the checkout, its .git (the checkout lock included), the remote and the scratch
// directory are byte for byte what they were. The real form is what the dry one predicts.
func TestEveryDryRunIsTheRealRunWithoutItsWrites(t *testing.T) {
	t.Parallel()
	wrongBranch := func(t *testing.T, root string) {
		gitIn(t, filepath.Join(root, "checkout"), "checkout", "-q", "-b", "elsewhere")
	}
	dirty := func(t *testing.T, root string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, "checkout", "stray.txt"), []byte("not this verb's\n"), 0o644))
	}
	lockSentinel := func(t *testing.T, root string) {
		gd := filepath.Join(root, "checkout", ".git")
		require.NoError(t, os.WriteFile(filepath.Join(gd, bus.LockName), []byte("sentinel holder\n"), 0o644))
	}
	clean := func(*testing.T, string) {}
	scratch := func(root string) string { return filepath.Join(root, "scratch") }
	verbs := map[string]func(root string) []string{
		"inbox --advance": func(root string) []string {
			return []string{"inbox", "--bus", filepath.Join(root, "checkout"), "--as", "Ada", "--receipt-max-words", "3", "--carry-history", "--advance", "--remote", "origin", "--branch", "main"}
		},
		"receipt": func(root string) []string {
			return []string{"receipt", "--bus", filepath.Join(root, "checkout"), "--as", "Ada", "--note", "bo-abcdef012345", "--remote", "origin", "--branch", "main"}
		},
		"close": func(root string) []string {
			return []string{"close", "--bus", filepath.Join(root, "checkout"), "--as", "Ada", "--before", "2026-12-01T00:00:00Z", "--remote", "origin", "--branch", "main"}
		},
		"send": func(root string) []string {
			return []string{"send", "--bus", filepath.Join(root, "checkout"), "--file", filepath.Join(scratch(root), "d.md"), "--as", "Ada", "--remote", "origin", "--branch", "main"}
		},
		"reply": func(root string) []string {
			return []string{"reply", "--bus", filepath.Join(root, "checkout"), "--as", "Ada", "--re", "bo-abcdef012345", "--file", filepath.Join(scratch(root), "r.md"), "--remote", "origin", "--branch", "main"}
		},
		"check --rebuild-index": func(root string) []string {
			return []string{"check", "--bus", filepath.Join(root, "checkout"), "--full", "--rebuild-index"}
		},
	}
	type tc struct {
		name  string
		setup func(*testing.T, string)
		args  func(root string) []string
	}
	var cases []tc
	for _, verb := range dryRealVerbs {
		args, ok := verbs[verb]
		if !ok {
			continue
		}
		cases = append(cases, tc{verb + "/clean", clean, args}, tc{verb + "/clean, a lock file there", lockSentinel, args})
		if verb != "check --rebuild-index" {
			cases = append(cases, tc{verb + "/wrong branch", wrongBranch, args}, tc{verb + "/a stray file", dirty, args})
		}
	}
	if slices.Contains(dryRealVerbs, "check --rebuild-index") {
		cases = append(cases, tc{"check --rebuild-index/without --full", clean, func(root string) []string {
			return []string{"check", "--bus", filepath.Join(root, "checkout"), "--rebuild-index"}
		}})
	}
	draft := func(out func(root string) string) func(root string) []string {
		return func(root string) []string {
			return []string{"draft", "--bus", filepath.Join(root, "checkout"), "--as", "Ada", "--to", "Bo", "--subject", "x", "--out", out(root)}
		}
	}
	if slices.Contains(dryRealVerbs, "draft --out") {
		cases = append(cases,
			tc{"draft --out/a new file", clean, draft(func(root string) string { return filepath.Join(scratch(root), "d.md") })},
			tc{"draft --out/a missing parent", clean, draft(func(root string) string { return filepath.Join(scratch(root), "missing", "d.md") })},
			tc{"draft --out/a parent that is a file", func(t *testing.T, root string) {
				require.NoError(t, os.WriteFile(filepath.Join(scratch(root), "f"), []byte("x"), 0o644))
			}, draft(func(root string) string { return filepath.Join(scratch(root), "f", "d.md") })},
			tc{"draft --out/a file already there", func(t *testing.T, root string) {
				require.NoError(t, os.WriteFile(filepath.Join(scratch(root), "d.md"), []byte("mine\n"), 0o644))
			}, draft(func(root string) string { return filepath.Join(scratch(root), "d.md") })},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			prepare := func() string {
				checkout, _ := busDir(t)
				root := filepath.Dir(checkout)
				require.NoError(t, os.MkdirAll(scratch(root), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(scratch(root), "d.md"), []byte("From: Ada\nTo: Bo\nSubject: the gate\n\nIt is green.\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(scratch(root), "r.md"), []byte("Yes, on the merge queue too.\n"), 0o644))
				c.setup(t, root)
				return root
			}
			dryRoot, realRoot := prepare(), prepare()
			before := treeBytes(t, dryRoot)
			dry := invoke(t, "", append(c.args(dryRoot), "--dry-run")...)
			real := invoke(t, "", c.args(realRoot)...)
			assert.Equal(t, real.code, dry.code, "dry and real exits differ\ndry stderr: %s\nreal stderr: %s", dry.stderr, real.stderr)
			if real.code != 0 {
				assert.Equal(t, normalizeRoot(real.stderr, realRoot), normalizeRoot(dry.stderr, dryRoot), "dry and real refuse differently")
			}
			assert.Equal(t, before, treeBytes(t, dryRoot), "the dry run changed something on disk\nstdout: %s\nstderr: %s", dry.stdout, dry.stderr)
		})
	}
}

// dryRealVerbs is every verb TestEveryDryRunIsTheRealRunWithoutItsWrites holds: every verb
// of this tool that takes --dry-run but wait, which has none.
var dryRealVerbs = []string{"inbox --advance", "draft --out"}

// treeBytes is every file under root, .git included, with its bytes, and every directory.
func treeBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	}))
	return out
}

// normalizeRoot replaces a copy's root, as given and as resolved, with one placeholder.
func normalizeRoot(s, root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		s = strings.ReplaceAll(s, resolved, "<root>")
	}
	return strings.ReplaceAll(s, root, "<root>")
}
