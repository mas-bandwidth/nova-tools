package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hostile are values a caller may hand nova-work that a shell would split,
// expand, chain or read as something else if a remedy printed them bare.
var hostile = []struct{ name, value string }{
	{"space", "work trees"},
	{"single quote", "it's"},
	{"double quote", `say "hi"`},
	{"command substitution", "$(x)"},
	{"semicolon", "a;b"},
	{"leading dash", "-x"},
	{"newline", "line\nbreak"},
}

// remedyArgs is the remedy of a refusal split as a POSIX shell splits it
// (onboarding.SplitShell; nothing is executed): from --json, where the
// remedy is exact, and from the text line's "; run: " tail when the text
// form can carry the value (a newline is shown escaped there, by the
// one-line rule, so that form is read only for values without one).
func remedyArgs(t *testing.T, stdout, stderr string, textToo bool) (fromJSON, fromText []string) {
	t.Helper()
	var out struct {
		Result struct {
			Status string `json:"status"`
			Remedy string `json:"remedy"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &out), "the --json run printed no JSON: %q", stdout)
	require.Equal(t, "refused", out.Result.Status, stdout)
	fromJSON, err := onboarding.SplitShell(out.Result.Remedy)
	require.NoError(t, err, "the remedy %q does not read as shell words", out.Result.Remedy)
	if textToo {
		_, tail, ok := strings.Cut(stderr, "; run: ")
		require.True(t, ok, "no remedy on the refusal line: %q", stderr)
		fromText, err = onboarding.SplitShell(strings.TrimSuffix(tail, "\n"))
		require.NoError(t, err, "the printed remedy %q does not read as shell words", tail)
	}
	return fromJSON, fromText
}

// after is the word that follows flag in args, or "" when flag is absent.
func after(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// TestARemedyKeepsEveryValueOneShellWord (review of PR 5084, finding 2):
// every caller-supplied value a refusal prints into its "run:" command is one
// shell word that reads back as the value, whatever it holds: the budget
// refusal's import (--out, --gh), the login check of the gh found (import and
// verify), and verify's remedies for a tree past --max-bytes (every input
// kept) and a tree that cannot be read (`ls -l --`, so a leading dash is a
// path, not an option).
func TestARemedyKeepsEveryValueOneShellWord(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the hostile names are file names windows does not allow")
	}
	for _, h := range hostile {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			textToo := !strings.ContainsAny(h.value, "\n")
			// fresh gives each run its own GitHub: a replay answers one run.
			check := func(name string, fresh func() github, args []string, want func(args []string)) {
				t.Helper()
				js := workMain(fresh()).Run(append(args, "--json")...)
				txt := workMain(fresh()).Run(args...)
				require.Equal(t, 2, js.Code, "%s: %s%s", name, js.Stdout, js.Stderr)
				for _, l := range strings.Split(strings.TrimSuffix(txt.Stderr, "\n"), "\n") {
					require.Regexp(t, `^(IMPORT|VERIFY) `, l, "%s: a value broke the refusal's line: %q", name, txt.Stderr)
				}
				first, _, _ := strings.Cut(txt.Stderr, "\n")
				fromJSON, fromText := remedyArgs(t, js.Stdout, first, textToo)
				want(fromJSON)
				if textToo {
					want(fromText)
				}
			}

			out := filepath.Join(dir, h.value)
			check("budget", func() github { return recorded(t, "/bin/gh") },
				[]string{"import", "--org", "mas-bandwidth", "--repo", "mas-bandwidth/reliable", "--page-size", "15", "--max-calls", "2", "--out", out, "--gh", h.value},
				func(args []string) {
					assert.Equal(t, out, after(args, "--out"), "--out in %q", args)
					assert.Equal(t, h.value, after(args, "--gh"), "--gh in %q", args)
					assert.Equal(t, "3", after(args, "--max-calls"), "--max-calls in %q", args)
					assert.Equal(t, []string{"nova-work", "import"}, args[:2])
				})

			check("gh login", func() github { return answering(h.value, errors.New("exit status 4: not logged in")) },
				[]string{"import", "--org", "acme", "--repo", "acme/x", "--dry-run"},
				func(args []string) { assert.Equal(t, []string{h.value, "auth", "status"}, args) })

			big := filepath.Join(dir, "big "+h.value)
			require.NoError(t, os.WriteFile(big, []byte(minimalTree), 0o600))
			against := filepath.Join(dir, "against "+h.value)
			check("past --max-bytes", func() github { return unreachable(t) },
				[]string{"verify", "--tree", big, "--against", against, "--max-bytes", "10"},
				func(args []string) {
					assert.Equal(t, big, after(args, "--tree"), "--tree in %q", args)
					assert.Equal(t, against, after(args, "--against"), "--against kept in %q", args)
					assert.Equal(t, []string{"nova-work", "verify"}, args[:2])
				})

			unreadable := filepath.Join(dir, "dir "+h.value)
			require.NoError(t, os.Mkdir(unreadable, 0o700))
			check("unreadable", func() github { return unreachable(t) }, []string{"verify", "--tree", unreadable},
				func(args []string) { assert.Equal(t, []string{"ls", "-l", "--", unreadable}, args) })

			tree := filepath.Join(dir, "tree "+h.value)
			require.NoError(t, os.WriteFile(tree, []byte(minimalTree), 0o600))
			check("verify's gh login", func() github { return answering(h.value, errors.New("exit status 4: not logged in")) },
				[]string{"verify", "--tree", tree},
				func(args []string) { assert.Equal(t, []string{h.value, "auth", "status"}, args) })
		})
	}
}
