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

// git_bus_gone_class_test.go holds the removal of the old bus to its word: the
// bus that kept its messages in git was removed on 2026-10-04 and nova-bus is the
// Redis bus (docs/SPEC-BUS.md). A spec states the design, a workflow comment says
// what the workflow does, and neither tells the history of a thing that is gone;
// the history lives in the dated records, which stay as they were written.

// goneBusName matches the old bus by name in its spelled, hyphenated and joined
// forms.
var goneBusName = regexp.MustCompile(`(?i)\bgit[ -]?bus`)

// goneBusRecords are the tracked files, and prefixes of them, that may name the
// old bus: dated records and the two ledgers of what the removal deleted.
var goneBusRecords = []string{
	"RESOLUTIONS.md",
	"CHANGELOG.md",
	"docs/RELEASE-NOTES-",
	"docs/ratings/",
	"internal/ci/testdata/deleted-tests.txt",
	"internal/ci/testdata/namedpaths_allowlist.txt",
}

func goneBusIsRecord(path string) bool {
	for _, record := range goneBusRecords {
		if strings.HasPrefix(path, record) {
			return true
		}
	}
	return false
}

// TestTheGitBusIsNamedOnlyInRecords fails for every tracked file outside the
// records that names the old bus, with the file and line to rewrite. This file
// is not exempt: its spellings below are joined from two strings, so that no line
// of it matches the pattern.
func TestTheGitBusIsNamedOnlyInRecords(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	listed, err := cmd.Output()
	require.NoError(t, err, "git ls-files")
	checked := 0
	for _, path := range strings.Split(string(listed), "\x00") {
		if path == "" || goneBusIsRecord(path) {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, path))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, path))
		require.NoError(t, err)
		checked++
		for i, line := range strings.Split(string(raw), "\n") {
			assert.False(t, goneBusName.MatchString(line), "%s:%d names the old bus; a live file states what is true now, and the history is for the dated records", path, i+1)
		}
	}
	assert.Greater(t, checked, 100, "the walk read almost nothing; git ls-files ran in the wrong place")
}

// TestGoneBusNameMatchesItsSpellings pins the pattern the class test reads with.
func TestGoneBusNameMatchesItsSpellings(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{"the git" + " bus", "git" + "-bus", "git" + "bus", "The Git" + " Bus's"} {
		assert.True(t, goneBusName.MatchString(spelling), spelling)
	}
	for _, live := range []string{"nova-bus", "the Redis bus", "digit bus", "a git remote", "nova-bus2"} {
		assert.False(t, goneBusName.MatchString(live), live)
	}
}
