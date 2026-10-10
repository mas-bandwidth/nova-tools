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
// takes one turn). One row per refusal: the command that makes it, and the verb
// the door must name.
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
		{"hygiene with an identity whose email is doubled", []string{"hygiene", "--repo", ".", "--base", "main", "--head", "HEAD", "--identity", "Ada <<ada@example.com>>"}, "hygiene"},
		{"dogfood ledger with no source", []string{"dogfood", "ledger", "--receipts", t.TempDir()}, "dogfood ledger"},
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

// TestBareVerbsNameEveryMissingFlagAndTheirOwnHelp is the open reader finding
// of attempt 8: a bare verb has no required flag read by the skeleton (which
// would refuse with the whole-banner door), so every missing flag is read in
// the verb's own Run and every line of the refusal carries the same verb's own
// page. One run still names every independent problem (docs/STANDARD.md
// section 3, point 2).
func TestBareVerbsNameEveryMissingFlagAndTheirOwnHelp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		verb  string
		args  []string
		wants []string
	}{
		{"attest", []string{"attest"}, []string{"--home", "--manifest"}},
		{"hygiene", []string{"hygiene"}, []string{"--repo", "--base", "--head", "--identity"}},
		{"links", []string{"links"}, []string{"--dir"}},
		{"floors", []string{"floors"}, []string{"--core", "--source"}},
		{"corpus", []string{"corpus"}, []string{"--ledger", "--root", "--min-anchors"}},
		{"kernel", []string{"kernel"}, []string{"--file", "--max-bytes or --max-tokens"}},
		{"convergence", []string{"convergence"}, []string{"--repo", "--ledger", "--receipts", "--retired", "--since"}},
		{"dogfood ledger", []string{"dogfood", "ledger"}, []string{"--receipts"}},
		{"dogfood record", []string{"dogfood", "record"}, []string{"--tool", "--verb", "--by", "--notes", "--receipts", "state the verdict exactly once"}},
		{"dogfood gate", []string{"dogfood", "gate"}, []string{"--receipts"}},
		{"quickstart", []string{"quickstart"}, []string{"--dir"}},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			t.Parallel()

			exit, stdout, stderr := runCheck(t, tc.args...)
			require.EqualValues(t, 2, exit, "exit %d, want 2; stderr: %s", exit, stderr)
			require.EqualValues(t, "", stdout, "a refusal wrote to stdout: %q", stdout)
			for _, want := range tc.wants {
				assert.Contains(t, stderr, want, "%s does not name %s:\n%s", tc.verb, want, stderr)
			}
			lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
			require.NotEmpty(t, lines, "%s printed no refusal line", tc.verb)
			for _, line := range lines {
				if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "  ") {
					continue
				}
				assert.True(t, strings.HasSuffix(line, "; run: nova-check help "+tc.verb),
					"%s: refusal line does not name its own page: %q", tc.verb, line)
			}
			// The door the refusal names is a command that runs: `help <verb>`
			// answers at exit 0 (docs/STANDARD.md section 3, help is never a
			// refusal).
			helpExit, _, helpErr := runCheck(t, append([]string{"help"}, strings.Fields(tc.verb)...)...)
			assert.EqualValues(t, 0, helpExit, "nova-check help %s: exit %d; stderr: %s", tc.verb, helpExit, helpErr)
		})
	}
}
