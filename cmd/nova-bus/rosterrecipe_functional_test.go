//go:build functional

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE BANNER'S FIRST-SEND RECIPE RUNS AS PRINTED. `nova-bus help` carries a recipe from
// nothing to a first send (the ROSTER AND LANES paragraph). A recipe that is prose rots
// the day a rule changes, and the two that rotted first were a draft written inside the
// checkout (an untracked file, so send refused) and an inbox read as a sender-less name.
// This test reads the recipe out of the banner, line by line, and RUNS it in a temp
// directory: git lines through git, nova-bus lines through the tool, the roster the
// paragraph prints saved as participants.json where the recipe says, the draft's
// placeholder body replaced as the recipe says. Every command must exit 0.
//
// It needs git and nothing else; nothing reaches a network (--no-push), everything is
// under t.TempDir().
func TestTheBannersFirstSendRecipeRunsAsPrinted(t *testing.T) {
	t.Parallel()
	lines := strings.Split(invoke(t, "", "help").stdout, "\n")
	var roster string
	var recipe []string
	for i := 0; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if strings.HasPrefix(text, "first send, from nothing") {
			// the recipe's own sentence runs on to its first indented command
			for i++; i < len(lines) && !strings.HasPrefix(lines[i], "  git "); i++ {
			}
			for ; i < len(lines) && strings.HasPrefix(lines[i], "  ") && strings.TrimSpace(lines[i]) != ""; i++ {
				recipe = append(recipe, strings.TrimSpace(lines[i]))
			}
		}
	}
	require.Falsef(t, len(recipe) < 4, "the banner's recipe has %d lines, want its five commands: %q", len(recipe), recipe)

	root := t.TempDir()
	cwd := root
	// abs resolves a path the recipe writes against the directory the recipe is in, as the
	// shell would: nothing the recipe names is ever relative to this package's directory.
	abs := func(arg string) string {
		if filepath.IsAbs(arg) {
			return arg
		}
		return filepath.Join(cwd, arg)
	}
	ident := append(os.Environ(), "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=ada@example.com", "GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=ada@example.com")
	var sawSend, sawInbox bool
	for _, line := range recipe {
		if i := strings.Index(line, "  ("); i >= 0 {
			line = strings.TrimSpace(line[:i]) // a trailing parenthesis is the recipe's comment
		}
		for _, part := range strings.Split(line, " && ") {
			fields := strings.Fields(part)
			out := ""
			for i, f := range fields {
				if f == ">" && i+1 < len(fields) {
					out = abs(fields[i+1])
					fields = fields[:i]
					break
				}
			}
			switch fields[0] {
			case "cd":
				cwd = abs(filepath.Join(cwd, fields[1]))
			case "git":
				cmd := exec.Command("git", fields[1:]...)
				cmd.Dir, cmd.Env = cwd, ident
				{
					b, err := cmd.CombinedOutput()
					require.NoErrorf(t, err, "%q: %v\n%s", part, err, b)
				}
			case "printf":
				// printf '%s\n' '<roster>' > bus/participants.json: the recipe's roster
				at := strings.Index(part, `{"participants":[`)
				require.GreaterOrEqual(t, at, 0, "the recipe's printf writes no roster: %q", part)
				roster = strings.TrimSuffix(strings.TrimSpace(part[at:strings.LastIndex(part, ">")]), "'")
				require.NoError(t, os.WriteFile(out, []byte(roster+"\n"), 0o600))
			case "nova-bus":
				args := append([]string(nil), fields[1:]...)
				for i := 0; i+1 < len(args); i++ {
					if args[i] == "--bus" || args[i] == "--file" || args[i] == "--out" {
						args[i+1] = abs(args[i+1])
					}
				}
				var stdout, stderr bytes.Buffer
				{
					code := run(args, strings.NewReader(""), &stdout, &stderr, now())
					require.Equalf(t, 0, code, "%q: exit %d\nstdout: %s\nstderr: %s", part, code, stdout.String(), stderr.String())
				}
				if out != "" {
					body := strings.ReplaceAll(stdout.String(), "<the note goes here>", "Hello Bo, the recipe runs.")
					require.NoError(t, os.WriteFile(out, []byte(body), 0o600))
				}
				sawSend = sawSend || strings.Contains(stdout.String(), "SEND OK")
				sawInbox = sawInbox || strings.Contains(stdout.String(), "INBOX OK")
			default:
				require.FailNowf(t, "assertion failed", "the recipe runs %q, which this test does not know how to run", part)
			}
		}
	}
	assert.Falsef(t, !sawSend || !sawInbox, "the recipe ran without printing SEND OK (%v) and INBOX OK (%v)", sawSend, sawInbox)
}
