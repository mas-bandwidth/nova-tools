package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusGrammar runs each verb of this tool that prints a status word to one
// OK, one REFUSED and one FAILED outcome through run, and holds the two halves of
// the grammar together: the first word after the verb's token is OK, REFUSED or
// FAILED (docs/STANDARD.md section 2), and the exit under it is 0, 2 or 1 in that
// order. A word that moved without its exit, or an exit that moved without its
// word, fails here first.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		token string // the token the verb's lines lead with
		word  string // the first word after the token: OK, REFUSED or FAILED
		exit  int
		args  func(t *testing.T) []string
	}{
		{"quickstart closes OK on a clean tree", "QUICKSTART", "OK", 0, func(t *testing.T) []string {
			dir := t.TempDir()
			mustWrite(t, dir, "a.md", "no links here\n")
			return []string{"quickstart", "--dir", dir}
		}},
		{"quickstart closes FAILED on a broken link", "QUICKSTART", "FAILED", 1, func(t *testing.T) []string {
			dir := t.TempDir()
			mustWrite(t, dir, "a.md", "[gone](nowhere.md)\n")
			return []string{"quickstart", "--dir", dir}
		}},
		{"quickstart refuses a missing --dir", "QUICKSTART", "REFUSED", 2, nil},
		{"attest passes a good manifest", "ATTEST", "OK", 0, func(t *testing.T) []string {
			home := t.TempDir()
			mustWrite(t, home, "KERNEL.md", "the kernel\n")
			manifest := filepath.Join(t.TempDir(), "manifest.txt")
			require.NoError(t, os.WriteFile(manifest, []byte("KERNEL.md\n"), 0o644))
			return []string{"attest", "--home", home, "--manifest", manifest}
		}},
		{"attest fails on a path the manifest names and the tree lacks", "ATTEST", "FAILED", 1, func(t *testing.T) []string {
			home := t.TempDir()
			manifest := filepath.Join(t.TempDir(), "manifest.txt")
			require.NoError(t, os.WriteFile(manifest, []byte("missing.md\n"), 0o644))
			return []string{"attest", "--home", home, "--manifest", manifest}
		}},
		{"attest refuses a missing --manifest", "ATTEST", "REFUSED", 2, nil},
		{"links passes a resolving tree", "LINKS", "OK", 0, func(t *testing.T) []string {
			dir := t.TempDir()
			mustWrite(t, dir, "a.md", "[b](b.md)\n")
			mustWrite(t, dir, "b.md", "# b\n")
			return []string{"links", "--dir", dir}
		}},
		{"links fails on a broken link", "LINKS", "FAILED", 1, func(t *testing.T) []string {
			dir := t.TempDir()
			mustWrite(t, dir, "a.md", "[x](missing.md)\n")
			return []string{"links", "--dir", dir}
		}},
		{"links refuses a missing --dir", "LINKS", "REFUSED", 2, nil},
		{"kernel passes under its byte budget", "KERNEL", "OK", 0, func(t *testing.T) []string {
			file := filepath.Join(t.TempDir(), "kernel.md")
			require.NoError(t, os.WriteFile(file, []byte("# Kernel\n"), 0o644))
			return []string{"kernel", "--file", file, "--max-bytes", "4000"}
		}},
		{"kernel fails over its byte budget", "KERNEL", "FAILED", 1, func(t *testing.T) []string {
			file := filepath.Join(t.TempDir(), "kernel.md")
			require.NoError(t, os.WriteFile(file, []byte("# Kernel\n"), 0o644))
			return []string{"kernel", "--file", file, "--max-bytes", "1"}
		}},
		{"kernel refuses a missing --file", "KERNEL", "REFUSED", 2, nil},
		{"nocode passes a prose tree", "NOCODE", "OK", 0, func(t *testing.T) []string {
			dir := t.TempDir()
			mustWrite(t, dir, "a.md", "prose\n")
			return []string{"nocode", "--dir", dir}
		}},
		{"nocode fails on a script", "NOCODE", "FAILED", 1, func(t *testing.T) []string {
			dir := t.TempDir()
			mustWrite(t, dir, "run.sh", "#!/bin/sh\n")
			return []string{"nocode", "--dir", dir}
		}},
		{"nocode refuses a missing --dir", "NOCODE", "REFUSED", 2, nil},
		{"floors passes the seed pair", "FLOORS", "OK", 0, func(t *testing.T) []string {
			return []string{"floors",
				"--core", "../../internal/check/testdata/seed-core-floors.md",
				"--source", "../../internal/check/testdata/seed-floors.md"}
		}},
		{"floors fails on a swapped pair", "FLOORS", "FAILED", 1, func(t *testing.T) []string {
			return []string{"floors",
				"--core", "../../internal/check/testdata/seed-floors.md",
				"--source", "../../internal/check/testdata/seed-core-floors.md"}
		}},
		{"floors refuses a missing --core", "FLOORS", "REFUSED", 2, nil},
		{"corpus passes the example ledger at its floor", "CORPUS", "OK", 0, func(t *testing.T) []string {
			return []string{"corpus", "--ledger", exampleSelf + "/corpus/anchors.md",
				"--root", exampleSelf, "--min-anchors", "2"}
		}},
		{"corpus fails under a row floor the ledger cannot meet", "CORPUS", "FAILED", 1, func(t *testing.T) []string {
			return []string{"corpus", "--ledger", exampleSelf + "/corpus/anchors.md",
				"--root", exampleSelf, "--min-anchors", "99"}
		}},
		{"corpus refuses an unnamed ledger", "CORPUS", "REFUSED", 2, nil},
		{"spelling passes clean prose", "SPELLING", "OK", 0, func(t *testing.T) []string {
			dir := t.TempDir()
			mustWrite(t, dir, "a.md", "the receive step\n")
			return []string{"spelling", "--dir", dir}
		}},
		{"spelling fails on a misspelling", "SPELLING", "FAILED", 1, func(t *testing.T) []string {
			dir := t.TempDir()
			mustWrite(t, dir, "a.md", "the recieve step\n")
			return []string{"spelling", "--dir", dir}
		}},
		{"spelling refuses to guess where to read", "SPELLING", "REFUSED", 2, nil},
		{"hygiene passes a clean branch", "HYGIENE", "OK", 0, func(t *testing.T) []string {
			return []string{"hygiene", "--repo", hygLab(t), "--base", "main", "--head", "HEAD",
				"--identity", "Rowan <rowan@example.com>"}
		}},
		{"hygiene fails on a change outside the declared paths", "HYGIENE", "FAILED", 1, func(t *testing.T) []string {
			lab := hygLab(t)
			hygWrite(t, lab, "elsewhere/x.go", "package elsewhere\n")
			hygGit(t, lab, "add", "-A")
			hygGit(t, lab, "commit", "-q", "-m", "out of path")
			return []string{"hygiene", "--repo", lab, "--base", "main", "--head", "HEAD",
				"--identity", "Rowan <rowan@example.com>", "--paths", "sign/**"}
		}},
		{"hygiene refuses a range checked against nobody", "HYGIENE", "REFUSED", 2, func(t *testing.T) []string {
			return []string{"hygiene", "--repo", hygLab(t), "--base", "main", "--head", "HEAD"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var args []string
			if tc.args != nil {
				args = tc.args(t)
			} else {
				args = []string{strings.ToLower(tc.token)}
			}
			exit, stdout, stderr := runCheck(t, args...)
			assert.Equal(t, tc.exit, exit, "%s: exit = %d, want %d; stdout: %s stderr: %s", tc.name, exit, tc.exit, stdout, stderr)
			line := lastLineLeadingWith(t, stdout+"\n"+stderr, tc.token)
			rest, ok := strings.CutPrefix(line, tc.token+" ")
			require.True(t, ok, "%s: line %q does not lead with the token", tc.name, line)
			// A refusal spells its word with the colon the grammar puts after it.
			word := strings.TrimSuffix(strings.Fields(rest)[0], ":")
			assert.Equal(t, tc.word, word, "%s: the first word after the token, on line %q", tc.name, line)
		})
	}

	t.Run("the alias --fail-max is --max", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustWrite(t, dir, "a.md", "[a](1.md)\n[b](2.md)\n[c](3.md)\n")
		alias, _, stderr := runCheck(t, "links", "--dir", dir, "--fail-max", "1")
		assert.Equal(t, 1, alias, "exit = %d, want 1; stderr: %s", alias, stderr)
		assert.Contains(t, stderr, "NOTE --fail-max is --max", "the old spelling said nothing:\n%s", stderr)
		assert.Contains(t, stderr, "LINKS MORE kind=broken shown=1 total=3 "+tool.MaxRemedy, "the alias set no ceiling:\n%s", stderr)
		spelled, _, stderr := runCheck(t, "links", "--dir", dir, "--max", "1")
		assert.Equal(t, alias, spelled, "the two spellings answered different exits")
		assert.NotContains(t, stderr, "NOTE --fail-max is --max", "the new spelling apologises for nothing:\n%s", stderr)
	})
}

// lastLineLeadingWith returns the last line of the stream that begins with the
// token and a status word (OK, FAILED or REFUSED, a refusal's with its colon), so
// a verb's closing line is read and not its opening RUN line or a finding under it.
func lastLineLeadingWith(t *testing.T, stream, token string) string {
	t.Helper()
	line := ""
	for _, candidate := range strings.Split(stream, "\n") {
		if rest, ok := strings.CutPrefix(candidate, token+" "); ok {
			switch strings.TrimSuffix(strings.Fields(rest + " ")[0], ":") {
			case "OK", "FAILED", "REFUSED":
				line = candidate
			}
		}
	}
	require.NotEmpty(t, line, "no line leads with %q in:\n%s", token, stream)
	return line
}
