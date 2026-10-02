package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// When the private copy's directory cannot be made, every line of the run says the same
// thing about the source: its SOURCE line carries the ordinary OpenCode metadata
// (reports=, files=1) and unreadable=1, one UNREADABLE line names it, and the summary
// counts it once. The allocator is a parameter, so the failure is the test's own, never a
// file mode a privileged run would ignore; nothing is copied, queried or cleaned up.
func TestAPrivateCopyThatCannotBeMadeIsOneUnreadableOnEveryLine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scratch := mkdir(t, filepath.Join(dir, "scratch"))
	out := mkdir(t, filepath.Join(dir, "out"))
	db := write(t, filepath.Join(dir, "opencode.db"), "SQLite format 3\x00 not really\n")
	repos := reposFile(t, dir)
	refused := errors.New("no room for a private copy")
	failing := func(string) (string, error) { return "", refused }
	opencode := []string{"--repos", repos, "--opencode", "a=" + db, "--opencode", "b=" + db, "--scratch", scratch}

	for _, tc := range []struct {
		name, token string
		args        []string
	}{
		{"sources", "SOURCES", append([]string{"sources", "--all"}, opencode...)},
		{"fold --dry-run", "TOKENS", append([]string{"fold", "--out", out, "--day", "2026-09-11", "--dry-run"}, opencode...)},
		{"report --dry-run", "TOKENS", append([]string{"report", "--who", "ada", "--day", "2026-09-11", "--dry-run"}, opencode...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWith(tc.args, &stdout, &stderr, foldStamp, failing)
			all := stdout.String() + stderr.String()
			for _, label := range []string{"opencode:a", "opencode:b"} {
				assert.Equal(t, 1, strings.Count(all, tc.token+" UNREADABLE label="+label+" "), "UNREADABLE lines for %s:\n%s", label, all)
				assert.Contains(t, all, "no room for a private copy")
				if tc.name != "report --dry-run" { // report prints no SOURCE lines
					assert.Contains(t, all, tc.token+" SOURCE label="+label+" kind=opencode path="+db+" reports=input,output,cache_write,cache_read,reasoning day_basis=utc files=1 unreadable=1 ")
				}
			}
			switch tc.name {
			case "sources":
				assert.Contains(t, stdout.String(), "SOURCES OK sources=2 files=2 messages=0 unreadable=2 ")
				assert.Equal(t, 0, code) // sources answers and never asserts
			case "fold --dry-run":
				assert.Contains(t, stderr.String(), "TOKENS FAIL days=0 rows=0 sources=2 unreadable=2 ")
				assert.Equal(t, 1, code)
			default:
				assert.Equal(t, 1, code)
			}
			ents, err := os.ReadDir(scratch)
			require.NoError(t, err)
			assert.Empty(t, ents, "something was made in --scratch")
		})
	}
}
