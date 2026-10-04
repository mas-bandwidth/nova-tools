package main

import (
	"strings"
	"testing"

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
		token string // the token the verb's lines lead with; a refusal leads with "nova-dev <verb>"
		word  string // the first word after the token: OK, REFUSED or FAILED
		exit  int
		args  func(t *testing.T) []string
	}{
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
		{"hygiene refuses a range checked against nobody", "nova-dev hygiene", "REFUSED", 2, func(t *testing.T) []string {
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
				args = strings.Fields(tc.token[len("nova-dev "):])
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
}

// lastLineLeadingWith returns the last line of the stream that begins with the
// token and a space, so a verb's closing line is read and not its opening RUN
// line or a finding under it.
func lastLineLeadingWith(t *testing.T, stream, token string) string {
	t.Helper()
	line := ""
	for _, candidate := range strings.Split(stream, "\n") {
		if strings.HasPrefix(candidate, token+" ") {
			line = candidate
		}
	}
	require.NotEmpty(t, line, "no line leads with %q in:\n%s", token, stream)
	return line
}
