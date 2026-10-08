package ci

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

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
	"internal/sprint/seatinstall.go": {"cmd/nova-sprint/seatinstall.go", `filepath.Join(home, "Library", "Logs"`},
	"internal/friend/launchd.go":     {"cmd/nova-friend/main.go", `filepath.Join(w.home, "Library", "Logs"`},
	"internal/up/redis.go":           {"internal/up/redis.go", `filepath.Join(e.Home, "Library", "Logs"`},
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

// walkLaunchdTextSources keeps the broad source search while excluding built
// executables: arbitrary byte sequences in an ELF can resemble plist keys and
// values. A new text plist, template, script, or Go writer still enters the
// class rule, regardless of its filename extension.
func walkLaunchdTextSources(root string, visit func(rel string, body []byte)) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
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
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(body, 0) >= 0 || !utf8.Valid(body) || !reLaunchdKey.Match(body) {
			return nil
		}
		visit(rel, body)
		return nil
	})
}

func literalLaunchdLogPaths(rel string, body []byte) [][]string {
	if strings.HasSuffix(rel, ".go") {
		return nil // Go writers are checked against their pinned defaults instead.
	}
	return reLaunchdPath.FindAllStringSubmatch(string(body), -1)
}

func invalidHomeLogPaths(matches [][]string) [][]string {
	var invalid [][]string
	for _, m := range matches {
		if !isHomeLogPath(m[2]) {
			invalid = append(invalid, m)
		}
	}
	return invalid
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
	err := walkLaunchdTextSources(root, func(rel string, b []byte) {
		matches := literalLaunchdLogPaths(rel, b)
		literals += len(matches)
		for _, m := range invalidHomeLogPaths(matches) {
			assert.Fail(t, "launchd log outside home", "%s: %s %q is not under a home directory variable and Library/Logs (launchd cannot open a log on /Volumes); use {{ nova_home | e }}/Library/Logs/...", rel, m[1], m[2])
		}
		if w, ok := goPlistWriters[rel]; ok {
			seenWriters[rel] = true
			assert.Contains(t, read(w.defaults), w.snippet, "%s: the default log of the plist %s writes must stay under the home's Library/Logs", w.defaults, rel)
		} else {
			assert.NotEmpty(t, matches, "%s names StandardOutPath/StandardErrorPath with no literal <string> value (and is no pinned Go writer); its log cannot be checked: pin it in goPlistWriters", rel)
		}
	})
	require.NoError(t, err)
	assert.Positive(t, literals, "at least one literal launchd log path must be read")
	for rel := range goPlistWriters {
		assert.True(t, seenWriters[rel], "%s is pinned in goPlistWriters but no longer writes a launchd log path", rel)
	}
}

func TestLaunchdTextScanIgnoresBinaryButChecksBadPlist(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "fleet", "templates"), 0o755))
	bad := `<key>StandardErrorPath</key><string>/Volumes/nova/bad.log</string>`
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "nova-friend"), append([]byte("\x7fELF\x00"), bad...), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "fleet", "templates", "bad.plist"), []byte(bad), 0o644))
	seen := map[string]string{}
	var invalid [][]string
	err := walkLaunchdTextSources(root, func(rel string, body []byte) {
		matches := literalLaunchdLogPaths(rel, body)
		for _, m := range matches {
			seen[rel] = m[2]
		}
		invalid = append(invalid, invalidHomeLogPaths(matches)...)
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"fleet/templates/bad.plist": "/Volumes/nova/bad.log"}, seen)
	require.Len(t, invalid, 1, "the real plist must still fail the home-log rule")
	assert.Equal(t, "/Volumes/nova/bad.log", invalid[0][2])
}
