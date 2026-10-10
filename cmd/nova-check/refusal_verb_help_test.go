package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryRefusalNamesTheVerbsOwnHelp pins the card's step 3: a refusal the
// verb itself builds names that verb's own help, `nova-check help <verb>`, so
// the reader who mis-invoked one verb pastes the one page with its flags and
// effect instead of the whole banner (docs/STANDARD.md section 3, recovery
// takes one turn). One row per verb: the command that makes it refuse, and the
// verb the door must name.
func TestEveryRefusalNamesTheVerbsOwnHelp(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "nope")
	cases := []struct {
		name string
		args []string
		verb string
	}{
		{"links without flags", []string{"links"}, "links"},
		{"links with a directory that is not there", []string{"links", "--dir", missing}, "links"},
		{"spelling without flags", []string{"spelling"}, "spelling"},
		{"spelling with an ignore file that is not there", []string{"spelling", "--dir", exampleSelf, "--ignore", "@no-such-ignore-file"}, "spelling"},
		{"quickstart without flags", []string{"quickstart"}, "quickstart"},
		{"attest without flags", []string{"attest"}, "attest"},
		{"attest with a manifest that is not there", []string{"attest", "--home", exampleSelf, "--manifest", missing}, "attest"},
		{"kernel without flags", []string{"kernel"}, "kernel"},
		{"kernel without budget", []string{"kernel", "--file", "k.md"}, "kernel"},
		{"nocode without flags", []string{"nocode"}, "nocode"},
		{"nocode with a directory that is not there", []string{"nocode", "--dir", missing}, "nocode"},
		{"floors without flags", []string{"floors"}, "floors"},
		{"corpus without flags", []string{"corpus"}, "corpus"},
		{"corpus with a ledger that is not there", []string{"corpus", "--ledger", missing, "--root", ".", "--min-anchors", "1"}, "corpus"},
		{"hygiene without flags", []string{"hygiene"}, "hygiene"},
		{"hygiene with missing repo", []string{"hygiene", "--base", "main", "--head", "head", "--identity", "Ada <ada@example.com>"}, "hygiene"},
		{"hygiene with invalid identity", []string{"hygiene", "--repo", ".", "--base", "main", "--head", "head", "--identity", "Ada <<ada@example.com>>"}, "hygiene"},
		{"hygiene with invalid kind", []string{"hygiene", "--repo", ".", "--base", "main", "--head", "head", "--identity", "Ada <ada@example.com>", "--kind", "fix-with-red-test"}, "hygiene"},
		{"hygiene with invalid timeout", []string{"hygiene", "--repo", ".", "--base", "main", "--head", "head", "--identity", "Ada <ada@example.com>", "--timeout", "0"}, "hygiene"},
		{"dogfood ledger without flags", []string{"dogfood", "ledger"}, "dogfood ledger"},
		{"dogfood ledger with no verb list", []string{"dogfood", "ledger", "--receipts", t.TempDir()}, "dogfood ledger"},
		{"dogfood record without flags", []string{"dogfood", "record"}, "dogfood record"},
		{"dogfood record with a cli that is not there", []string{"dogfood", "record", "--cli", missing, "--tool", "nova-example", "--verb", "links", "--by", "Stella", "--notes", "work", "--ok", "--receipts", t.TempDir()}, "dogfood record"},
		{"dogfood gate without flags", []string{"dogfood", "gate"}, "dogfood gate"},
		{"dogfood gate with a cli that is not there", []string{"dogfood", "gate", "--cli", missing, "--receipts", t.TempDir()}, "dogfood gate"},
		{"convergence without flags", []string{"convergence"}, "convergence"},
		{"convergence with a since that is neither instant nor duration", []string{"convergence", "--repo", ".", "--ledger", "l", "--receipts", "r", "--retired", "t", "--since", "not-a-time"}, "convergence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exit, stdout, stderr := runCheck(t, tc.args...)
			require.EqualValues(t, 2, exit, "exit %d, want 2; stderr: %s", exit, stderr)
			require.EqualValues(t, "", stdout, "a refusal wrote to stdout: %q", stdout)
			lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
			require.NotEmpty(t, lines, "no refusal lines on stderr")
			for _, line := range lines {
				if line == "" || strings.HasPrefix(line, "  ") {
					continue
				}
				assert.True(t, strings.HasSuffix(line, "; run: nova-check help "+tc.verb),
					"the refusal line %q does not end with the %s page", line, tc.verb)
			}
		})
	}
}
