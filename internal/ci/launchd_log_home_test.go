package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launchd_log_home_test.go asserts the launchd agent log location rule
// (BRIEF fp-fric-19-launchd-logs-under-home, docs/FLEET.md):
// launchd cannot open its own log file on a network volume such as
// /Volumes/nova, so an agent whose StandardOutPath or StandardErrorPath
// lives there runs but its job never starts or its output vanishes.
// Every launchd agent this repository installs must put its launchd log
// under the user's home (for the loops ~/Library/Logs/nova-loop-<name>.log).

var (
	reStandardOutPath   = regexp.MustCompile(`(?s)<key>StandardOutPath</key>\s*<string>([^<]+)</string>`)
	reStandardErrorPath = regexp.MustCompile(`(?s)<key>StandardErrorPath</key>\s*<string>([^<]+)</string>`)
)

// isHomeLogPath reports whether path is under a home directory variable or
// home directory prefix (e.g. {{ ansible_env.HOME }}/Library/Logs/... or
// ~/Library/Logs/...).
func isHomeLogPath(path string) bool {
	p := strings.TrimSpace(path)
	return strings.HasPrefix(p, "{{ ansible_env.HOME }}/") ||
		strings.HasPrefix(p, "~/") ||
		strings.HasPrefix(p, "$HOME/") ||
		strings.HasPrefix(p, "${HOME}/")
}

// TestEveryLaunchdAgentLogsUnderTheHome parses fleet/templates/nova-loop.plist.j2
// and plist-writing files in repo and asserts StandardOutPath and StandardErrorPath
// values are under home directory variables.
func TestEveryLaunchdAgentLogsUnderTheHome(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	// Specifically verify the loop launchd template exists
	loopTmpl := filepath.Join(root, "fleet", "templates", "nova-loop.plist.j2")
	b, err := os.ReadFile(loopTmpl)
	require.NoError(t, err, "fleet/templates/nova-loop.plist.j2 must exist")

	outMatches := reStandardOutPath.FindStringSubmatch(string(b))
	require.Len(t, outMatches, 2, "nova-loop.plist.j2 must declare StandardOutPath")
	outPath := strings.TrimSpace(outMatches[1])
	assert.True(t, isHomeLogPath(outPath),
		"fleet/templates/nova-loop.plist.j2 StandardOutPath %q is not under a home directory variable (want e.g. {{ ansible_env.HOME }}/Library/Logs/...)", outPath)
	assert.Contains(t, outPath, "Library/Logs", "StandardOutPath must be under Library/Logs")

	errMatches := reStandardErrorPath.FindStringSubmatch(string(b))
	require.Len(t, errMatches, 2, "nova-loop.plist.j2 must declare StandardErrorPath")
	errPath := strings.TrimSpace(errMatches[1])
	assert.True(t, isHomeLogPath(errPath),
		"fleet/templates/nova-loop.plist.j2 StandardErrorPath %q is not under a home directory variable (want e.g. {{ ansible_env.HOME }}/Library/Logs/...)", errPath)
	assert.Contains(t, errPath, "Library/Logs", "StandardErrorPath must be under Library/Logs")

	// Walk repo for all plist files and plist-writing templates/sources
	var checked []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "_cache" || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Skip docs, markdown, go tests
		if strings.HasPrefix(rel, "docs/") || strings.HasSuffix(rel, ".md") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		if !strings.HasSuffix(rel, ".plist") && !strings.HasSuffix(rel, ".plist.j2") {
			content, rerr := os.ReadFile(path)
			if rerr != nil || (!strings.Contains(string(content), "<key>StandardOutPath</key>") && !strings.Contains(string(content), "<key>StandardErrorPath</key>")) {
				return nil
			}
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)

		outs := reStandardOutPath.FindAllStringSubmatch(text, -1)
		for _, m := range outs {
			checked = append(checked, rel)
			val := strings.TrimSpace(m[1])
			assert.True(t, isHomeLogPath(val),
				"%s: StandardOutPath %q is not under a home directory variable", rel, val)
			assert.Contains(t, val, "Library/Logs", "%s: StandardOutPath %q must be under Library/Logs", rel, val)
		}

		errs := reStandardErrorPath.FindAllStringSubmatch(text, -1)
		for _, m := range errs {
			val := strings.TrimSpace(m[1])
			assert.True(t, isHomeLogPath(val),
				"%s: StandardErrorPath %q is not under a home directory variable", rel, val)
			assert.Contains(t, val, "Library/Logs", "%s: StandardErrorPath %q must be under Library/Logs", rel, val)
		}
		return nil
	})
	require.NoError(t, err)
	assert.NotEmpty(t, checked, "must check at least one plist source")
}
