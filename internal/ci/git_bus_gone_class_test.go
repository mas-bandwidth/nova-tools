package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// git_bus_gone_class_test.go keeps the removed git bus out of live text. A spec
// states the design and a dated record keeps the history, so the name appears
// only in the records below.

var gitBusName = regexp.MustCompile(`(?i)git[ -]?bus|gitbus`)

// gitBusRecords are the dated records that may name the git bus.
var gitBusRecords = []string{
	"RESOLUTIONS.md",
	"CHANGELOG.md",
	"docs/RELEASE-NOTES-",
	"docs/ratings/",
	"internal/ci/testdata/deleted-tests.txt",
	"internal/ci/testdata/namedpaths_allowlist.txt",
	"internal/ci/git_bus_gone_class_test.go",
}

func isGitBusRecord(rel string) bool {
	for _, r := range gitBusRecords {
		if rel == r || (strings.HasSuffix(r, "/") || strings.HasSuffix(r, "-")) && strings.HasPrefix(rel, r) {
			return true
		}
	}
	return false
}

func TestTheGitBusIsNamedOnlyInRecords(t *testing.T) {
	root := repoRoot(t)
	var live []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || rel == "jobs" {
				return filepath.SkipDir
			}
			return nil
		}
		if isGitBusRecord(rel) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if gitBusName.MatchString(line) {
				live = append(live, rel+":"+strconv.Itoa(i+1))
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, live, "the git bus is named in live text; state the design only, the history lives in the records")
}

func TestGitBusRecordsAreRecognised(t *testing.T) {
	require.True(t, isGitBusRecord("RESOLUTIONS.md"))
	require.True(t, isGitBusRecord("docs/RELEASE-NOTES-1.1.0.md"))
	require.True(t, isGitBusRecord("docs/ratings/1.1.0/johnny/bus-read.md"))
	require.False(t, isGitBusRecord("docs/SPEC-BUS.md"))
	require.False(t, isGitBusRecord("docs/CLI.md"))
	require.True(t, gitBusName.MatchString("the Git bus"))
	require.True(t, gitBusName.MatchString("git-bus"))
	require.False(t, gitBusName.MatchString("nova-bus"))
}
