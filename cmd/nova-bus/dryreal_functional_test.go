//go:build functional

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dry run is the real run's own path with the writes left out: for every verb that takes
// --dry-run, the dry form and the real form run on identical copies of one bus, and they
// refuse alike (the same exit, the same lines), and a dry run changes nothing on disk --
// the checkout, its .git (the checkout lock, which the real run leaves behind by design,
// included), the remote and the scratch directory are mode for mode and byte for byte
// what they were. The real form is what the dry one predicts.
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
	// The shared form (testkit.DryRunAgrees): identical fresh roots, the same exit and
	// status words, and testkit.Snapshot of the whole root unchanged by the dry run --
	// every path's mode and bytes, the checkout's .git included (its nova-bus.lock above
	// all), the remote and the scratch directory. Nothing is excluded: a read by git
	// under this tool (GIT_OPTIONAL_LOCKS=0) changes nothing in .git, and the cases
	// below are the measurement.
	var dry []testkit.DryCase
	for _, c := range cases {
		dry = append(dry, testkit.DryCase{Name: c.name, Setup: func(t *testing.T, root string) ([]string, []string) {
			busInto(t, root)
			require.NoError(t, os.MkdirAll(scratch(root), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(scratch(root), "d.md"), []byte("From: Ada\nTo: Bo\nSubject: the gate\n\nIt is green.\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(scratch(root), "r.md"), []byte("Yes, on the merge queue too.\n"), 0o644))
			c.setup(t, root)
			args := c.args(root)
			return args[:1], args[1:]
		}})
	}
	testkit.DryRunAgrees(t, func(args ...string) testkit.Result {
		r := invoke(t, "", args...)
		return testkit.Result{Code: r.code, Stdout: r.stdout, Stderr: r.stderr}
	}, dry)
	// And a dry run refuses in the real run's own words, not only its status word.
	for _, c := range cases {
		t.Run(c.name+"/the same refusal", func(t *testing.T) {
			dryRoot, realRoot := t.TempDir(), t.TempDir()
			for _, root := range []string{dryRoot, realRoot} {
				busInto(t, root)
				require.NoError(t, os.MkdirAll(scratch(root), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(scratch(root), "d.md"), []byte("From: Ada\nTo: Bo\nSubject: the gate\n\nIt is green.\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(scratch(root), "r.md"), []byte("Yes, on the merge queue too.\n"), 0o644))
				c.setup(t, root)
			}
			dry := invoke(t, "", append(c.args(dryRoot), "--dry-run")...)
			real := invoke(t, "", c.args(realRoot)...)
			if real.code != 0 {
				assert.Equal(t, normalizeRoot(real.stderr, realRoot), normalizeRoot(dry.stderr, dryRoot), "dry and real refuse differently")
			}
		})
	}
}

// busInto lays the shared bus fixture out under root, as busDir does under its own
// TempDir: root/checkout and root/bus.git, origin pointing at this root's bare copy.
func busInto(t *testing.T, root string) {
	t.Helper()
	busFixtureOnce.Do(func() { busFixtureDir, busFixtureErr = buildBusFixture() })
	require.NoError(t, busFixtureErr)
	require.NoError(t, os.CopyFS(root, os.DirFS(busFixtureDir)))
	gitIn(t, filepath.Join(root, "checkout"), "remote", "set-url", "origin", filepath.Join(root, "bus.git"))
}

// dryRealVerbs is every verb TestEveryDryRunIsTheRealRunWithoutItsWrites holds: every verb
// of this tool that takes --dry-run but wait, which has none.
var dryRealVerbs = []string{"inbox --advance", "draft --out", "receipt", "send", "reply", "close", "check --rebuild-index"}

// normalizeRoot replaces a copy's root, as given and as resolved, with one placeholder.
func normalizeRoot(s, root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		s = strings.ReplaceAll(s, resolved, "<root>")
	}
	return strings.ReplaceAll(s, root, "<root>")
}

// Every verb whose -h lists --dry-run is in dryRealVerbs: a dry run added later is held to
// the same rule the day it lands.
func TestEveryDryRunVerbIsHeldToTheRealRun(t *testing.T) {
	t.Parallel()
	for _, verb := range verbs {
		help := invoke(t, "", verb, "-h").mustCode(t, 0).stdout
		if !strings.Contains(help, "\n  --dry-run  ") {
			continue
		}
		found := false
		for _, v := range dryRealVerbs {
			found = found || strings.Fields(v)[0] == verb
		}
		assert.True(t, found, "%s takes --dry-run and TestEveryDryRunIsTheRealRunWithoutItsWrites does not hold it", verb)
	}
}
