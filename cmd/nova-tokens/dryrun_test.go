package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dryTree is one root a dry-against-real case runs in: the example bench copied in, a day
// already folded into out (so a dry run has a file it could replace), a session
// transcript, an earlier note, and a plain file where a directory is wanted.
type dryTree struct{ root, out, repos, tr, session, note, aFile string }

func newDryTree(t *testing.T, root string) dryTree {
	t.Helper()
	require.NoError(t, os.CopyFS(root, os.DirFS(filepath.Join("testdata", "example-bench"))))
	d := dryTree{root: root, out: testkit.Mkdir(t, filepath.Join(root, "out")),
		repos: filepath.Join(root, "repos.tsv"), tr: filepath.Join(root, "transcripts")}
	novaTokens.Do(t, "fold", "--out", d.out, "--day", "2026-09-11", "--repos", d.repos, "--claude", "bench="+d.tr).Exit(0)
	d.session = testkit.WriteFile(t, filepath.Join(root, "session.jsonl"), msg("s1", "2026-09-11T10:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 3})+"\n")
	d.note = testkit.WriteFile(t, filepath.Join(root, "notes", "note.txt"), "an earlier note\n")
	d.aFile = testkit.WriteFile(t, filepath.Join(root, "a-file"), "not a directory\n")
	return d
}

func (d dryTree) report(args ...string) []string {
	return append([]string{"report", "--who", "ada", "--day", "2026-09-11", "--repos", d.repos, "--claude", "bench=" + d.tr}, args...)
}

// dryCases is every writing verb's dry run held to its real run: the runs that write and
// the runs the real run refuses or fails (exit is the real run's).
var dryCases = []struct {
	name string
	exit int
	args func(d dryTree) []string
}{
	{"fold", 0, func(d dryTree) []string {
		return []string{"fold", "--out", d.out, "--day", "2026-09-11", "--repos", d.repos, "--claude", "bench=" + d.tr, "--allow-shrink"}
	}},
	{"fold --all", 0, func(d dryTree) []string {
		return []string{"fold", "--out", d.out, "--all", "--repos", d.repos, "--claude", "bench=" + d.tr}
	}},
	{"report --note", 0, func(d dryTree) []string { return d.report("--note", d.note) }},
	{"session --out, existing", 0, func(d dryTree) []string { return []string{"session", "--claude-session", d.session, "--out", d.out} }},
	{"session --out, new", 0, func(d dryTree) []string {
		return []string{"session", "--claude-session", d.session, "--out", filepath.Join(d.root, "new", "out")}
	}},
	{"fold with no flags", 2, func(dryTree) []string { return []string{"fold"} }},
	{"fold into a file", 2, func(d dryTree) []string {
		return []string{"fold", "--out", d.aFile, "--day", "2026-09-11", "--repos", d.repos, "--claude", "bench=" + d.tr}
	}},
	{"fold with no rules file", 2, func(d dryTree) []string {
		return []string{"fold", "--out", d.out, "--day", "2026-09-11", "--repos", filepath.Join(d.root, "none.tsv"), "--claude", "bench=" + d.tr}
	}},
	{"fold with --scratch and no --opencode", 2, func(d dryTree) []string {
		return []string{"fold", "--out", d.out, "--day", "2026-09-11", "--repos", d.repos, "--claude", "bench=" + d.tr, "--scratch", d.root}
	}},
	{"report with no --who", 2, func(d dryTree) []string {
		return []string{"report", "--day", "2026-09-11", "--repos", d.repos, "--claude", "bench=" + d.tr}
	}},
	{"report --note into a missing directory", 2, func(d dryTree) []string { return d.report("--note", filepath.Join(d.root, "missing", "note.txt")) }},
	{"report --note onto a directory", 2, func(d dryTree) []string { return d.report("--note", d.out) }},
	{"ledger with no --redis", 2, func(d dryTree) []string { return []string{"ledger", "--out", d.out, "--day", "2026-09-11"} }},
	{"ledger whose user has no password", 1, func(d dryTree) []string {
		return []string{"ledger", "--out", d.out, "--day", "2026-09-11", "--redis", "127.0.0.1:0",
			"--user", "bench", "--password-env", "NOVA_TOKENS_TEST_UNSET_PASSWORD_VARIABLE"}
	}},
	{"session with no transcript", 2, func(d dryTree) []string { return []string{"session", "--out", d.out} }},
	{"session --out a file", 2, func(d dryTree) []string { return []string{"session", "--claude-session", d.session, "--out", d.aFile} }},
	{"session --out below a file", 2, func(d dryTree) []string {
		return []string{"session", "--claude-session", d.session, "--out", filepath.Join(d.aFile, "out")}
	}},
}

// A dry run is the real run's own plan with only the writes skipped (testkit.DryRunAgrees):
// on identical fresh trees it exits as the real run does, with the same status words, and
// leaves its tree byte for byte as it was -- the sources, the output directory, the note's
// directory and a session's --out parent. Where the real run refuses, the dry run refuses
// in its words, line for line, not only its status word.
func TestADryRunIsTheRealRunWithoutItsWrites(t *testing.T) {
	t.Parallel()
	var cases []testkit.DryCase
	for _, c := range dryCases {
		cases = append(cases, testkit.DryCase{Name: c.name, Setup: func(t *testing.T, root string) ([]string, []string) {
			args := c.args(newDryTree(t, root))
			return args[:1], args[1:]
		}})
	}
	testkit.DryRunAgrees(t, novaTokens.Run, cases)

	for _, c := range dryCases {
		if c.exit == 0 {
			continue
		}
		t.Run(c.name+"/the same refusal", func(t *testing.T) {
			realRoot, dryRoot := t.TempDir(), t.TempDir()
			real := novaTokens.Do(t, c.args(newDryTree(t, realRoot))...).Exit(c.exit)
			dry := novaTokens.Do(t, append(c.args(newDryTree(t, dryRoot)), "--dry-run")...)
			assert.Equal(t, strings.ReplaceAll(real.Stderr, realRoot, "<root>"), strings.ReplaceAll(dry.Stderr, dryRoot, "<root>"))
		})
	}
}

// ledger's real run dials the store, which the unit tier has none of; its dry run reads the
// day files, dials nothing, and leaves the tree as it was.
func TestLedgersDryRunDialsNothingAndWritesNothing(t *testing.T) {
	t.Parallel()
	d := newDryTree(t, t.TempDir())
	before := testkit.Snapshot(t, d.root)
	r := novaTokens.Do(t, "ledger", "--out", d.out, "--day", "2026-09-11", "--redis", "127.0.0.1:0", "--dry-run").Exit(0)
	holds(t, r.Stdout+r.Stderr, "dry_run=true")
	assert.Equal(t, before, testkit.Snapshot(t, d.root), "--dry-run changed the tree")
}
