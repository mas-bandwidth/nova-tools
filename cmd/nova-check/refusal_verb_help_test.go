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
		{"links without a directory", []string{"links"}, "links"},
		{"links with a directory that is not there", []string{"links", "--dir", missing}, "links"},
		{"spelling without a selector", []string{"spelling"}, "spelling"},
		{"spelling with an ignore file that is not there", []string{"spelling", "--dir", exampleSelf, "--ignore", "@no-such-ignore-file"}, "spelling"},
		{"attest with a manifest that is not there", []string{"attest", "--home", exampleSelf, "--manifest", missing}, "attest"},
		{"nocode with a directory that is not there", []string{"nocode", "--dir", missing}, "nocode"},
		{"corpus with a ledger that is not there", []string{"corpus", "--ledger", missing, "--root", ".", "--min-anchors", "1"}, "corpus"},
		{"dogfood ledger with no verb list", []string{"dogfood", "ledger", "--receipts", t.TempDir()}, "dogfood ledger"},
		{"dogfood record with a cli that is not there", []string{"dogfood", "record", "--cli", missing, "--tool", "nova-example", "--verb", "links", "--by", "Stella", "--notes", "work", "--ok", "--receipts", t.TempDir()}, "dogfood record"},
		{"dogfood gate with a cli that is not there", []string{"dogfood", "gate", "--cli", missing, "--receipts", t.TempDir()}, "dogfood gate"},
		{"convergence with a since that is neither instant nor duration", []string{"convergence", "--repo", ".", "--ledger", "l", "--receipts", "r", "--retired", "t", "--since", "not-a-time"}, "convergence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exit, stdout, stderr := runCheck(t, tc.args...)
			require.EqualValues(t, 2, exit, "exit %d, want 2; stderr: %s", exit, stderr)
			require.EqualValues(t, "", stdout, "a refusal wrote to stdout: %q", stdout)
			line, _, _ := strings.Cut(strings.TrimRight(stderr, "\n"), "\n")
			assert.True(t, strings.HasSuffix(line, "; run: nova-check help "+tc.verb),
				"the refusal names %q, want the %s page: %q", line, tc.verb, line)
		})
	}
}
