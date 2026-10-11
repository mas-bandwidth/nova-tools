package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launchd_log_home_test.go holds the `launchd-logs-home` class rule
// (docs/SPEC-CI.md, docs/FLEET.md): launchd cannot open its own log file on a
// network volume such as /Volumes/nova, so an agent whose StandardOutPath or
// StandardErrorPath lives there runs but its job never starts or its output
// vanishes (measured 2026-10-03/04). Every launchd agent this repository
// installs puts its launchd log under the user's home; the program's own state
// and logs stay where they are.

var (
	reLaunchdKey  = regexp.MustCompile(`<key>Standard(?:Out|Error)Path</key>`)
	reLaunchdPath = regexp.MustCompile(`(?s)<key>(Standard(?:Out|Error)Path)</key>\s*<string>([^<]*)</string>`)
)

// homePrefixes are the spellings of "under the user's home" a literal plist or
// an Ansible template may use.
var homePrefixes = []string{"{{ nova_home", "{{ ansible_env.HOME", "~/", "$HOME/", "${HOME}/"}

// goPlistWriters are the Go sources that build a plist from a variable log
// path, so no literal value exists to read. Each is pinned by the snippet in
// the file that chooses the path's default (the flag that overrides it is the
// operator's choice).
var goPlistWriters = map[string]struct{ defaults, snippet string }{
	"pkg/friend/launchd.go": {"cmd/nova-friend/main.go", `filepath.Join(w.home, "Library", "Logs"`},
	"internal/up/redis.go":  {"internal/up/redis.go", `filepath.Join(e.Home, "Library", "Logs"`},
}

// isHomeLogPath reports whether a StandardOutPath value starts at a home
// directory variable and sits under Library/Logs.
func isHomeLogPath(path string) bool {
	p := strings.TrimSpace(path)
	for _, pre := range homePrefixes {
		if strings.HasPrefix(p, pre) {
			return strings.Contains(p, "/Library/Logs/")
		}
	}
	return false
}

// TestEveryLaunchdAgentLogsUnderTheHome reads every non-test source that names
// StandardOutPath or StandardErrorPath: a literal value must be under the home,
// and a Go source, which builds the value from a variable, must be a writer pinned in
// goPlistWriters, whose default log is under the home.
func TestEveryLaunchdAgentLogsUnderTheHome(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		require.NoError(t, err, rel)
		return string(b)
	}

	assert.Regexp(t, reLaunchdPath, read("fleet/templates/nova-loop.plist.j2"), "the loop template must declare both log paths")

	seenWriters := map[string]bool{}
	literals := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "_cache", "vendor", "testdata", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, "_test.go") || strings.HasSuffix(rel, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || !reLaunchdKey.Match(b) {
			return nil
		}
		var matches [][]string
		if !strings.HasSuffix(rel, ".go") {
			matches = reLaunchdPath.FindAllStringSubmatch(string(b), -1)
		}
		for _, m := range matches {
			literals++
			assert.True(t, isHomeLogPath(m[2]), "%s: %s %q is not under a home directory variable and Library/Logs (launchd cannot open a log on /Volumes); use {{ nova_home | e }}/Library/Logs/...", rel, m[1], m[2])
		}
		if w, ok := goPlistWriters[rel]; ok {
			seenWriters[rel] = true
			assert.Contains(t, read(w.defaults), w.snippet, "%s: the default log of the plist %s writes must stay under the home's Library/Logs", w.defaults, rel)
		} else {
			assert.NotEmpty(t, matches, "%s names StandardOutPath/StandardErrorPath with no literal <string> value (and is no pinned Go writer); its log cannot be checked: pin it in goPlistWriters", rel)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Positive(t, literals, "at least one literal launchd log path must be read")
	for rel := range goPlistWriters {
		assert.True(t, seenWriters[rel], "%s is pinned in goPlistWriters but no longer writes a launchd log path", rel)
	}
}
