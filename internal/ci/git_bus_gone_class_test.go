package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitBusNameRe is spelled in parts so this file does not name what it forbids.
var gitBusNameRe = regexp.MustCompile(`(?i)\b` + "gi" + `t[ -]?bus\b`)

// gitBusRecords are the dated records that keep the removed bus's name: they
// are history, and history is not rewritten.
var gitBusRecords = []string{
	"RESOLUTIONS.md",
	"CHANGELOG.md",
	"docs/RELEASE-NOTES-",
	"docs/ratings/",
	"internal/ci/testdata/deleted-tests.txt",
	"internal/ci/testdata/namedpaths_allowlist.txt",
}

func isGitBusRecord(path string) bool {
	for _, r := range gitBusRecords {
		if strings.HasPrefix(path, r) {
			return true
		}
	}
	return false
}

// TestTheGitBusIsNamedOnlyInRecords: the old file bus was removed 2026-10-04 and
// the Redis bus took the name nova-bus (docs/SPEC-BUS.md). A spec states the
// design and a dated record keeps the history, so no tracked file outside
// gitBusRecords names the removed bus.
func TestTheGitBusIsNamedOnlyInRecords(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	require.NoError(t, err, "git ls-files")
	for _, path := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if isGitBusRecord(path) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(src), "\n") {
			assert.False(t, gitBusNameRe.MatchString(line), "%s:%d names the removed bus outside the dated records: say what is true now, without the history", path, i+1)
		}
	}
}
