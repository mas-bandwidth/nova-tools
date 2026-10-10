//go:build functional

package ci

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

// version_functional_test.go builds every cmd/nova-* and runs it: a build is the functional
// tier's, never a unit test (Glenn 2026-09-26, nova-tools#4328: unit tests under
// 2 s and frugal with the machine's cores).

// TestEveryToolPrintsTheOneVersionLine is the class rule behind #1297 and
// #1264, kept where the next binary will trip over it.
//
// The hurt: `nova-version snapshot --bin ~/.local/bin` refused an entire
// install, exit 2, because nova-merge printed five tokens where the reader
// wanted four -- and nova-sandbox, in the same directory, printed a line of a
// different shape altogether (`SANDBOX VERSION tool=... version=...`). Two
// shapes meant every reader of a version line carried its own tolerant parser,
// and a tool that said one more true thing about itself broke them one at a
// time.
//
// The class fix is that there is ONE grammar -- four tokens and then
// `key=value` extras -- that pkg/buildinfo both writes and reads, and this
// test runs the REAL binary of every cmd/nova-* through the REAL reader. It
// walks cmd/ rather than holding a list, so a tool added tomorrow is held to
// the grammar on the day it appears. No network: every binary is built from
// this checkout and run with one argument.
func TestEveryToolPrintsTheOneVersionLine(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	tools := novaCommands(t, root)
	require.NotEmpty(t, tools, "no nova-* command directories found under cmd/; this test was looking in the wrong place and would have passed by checking nothing")
	bin := builtTools(t, root)

	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			// Both spellings, because the Conventions promise both and a tool
			// answers only one is a tool a reader has to try twice.
			for _, verb := range []string{"version", "--version"} {
				exit, stdout, stderr := runBare(t, root, tool, filepath.Join(bin, exeName(tool)), []string{verb})
				if !assert.Equalf(t, 0, exit, "`%s %s` exits %d, want 0; stderr: %s", tool, verb, exit, stderr) {
					continue
				}
				lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
				if !assert.Equalf(t, 1, len(lines), "`%s %s` printed %d lines, want exactly 1:\n%s", tool, verb, len(lines), stdout) {
					continue
				}
				f, ok := buildinfo.Parse(stdout)
				if !assert.Truef(t, ok, "`%s %s` printed a line pkg/buildinfo.Parse refuses; the grammar is `<tool> <identity> <goos>/<goarch> <go version>` and then any number of key=value extras:\n%s", tool, verb, stdout) {
					continue
				}
				assert.Equalf(t, tool, f.Tool, "`%s %s` names itself %q in field one; a reader holding two pastes reads the tool out of field one", tool, verb, f.Tool)
				assert.NotEmptyf(t, f.Version, "`%s %s` carries an empty identity in field two", tool, verb)
				wantPlatform := runtime.GOOS + "/" + runtime.GOARCH
				assert.Equalf(t, wantPlatform, f.Platform, "`%s %s` reports platform %q, want %q", tool, verb, f.Platform, wantPlatform)
				assert.Truef(t, strings.HasPrefix(f.GoVersion, "go"), "`%s %s` reports go version %q, want a go1.x", tool, verb, f.GoVersion)
			}
		})
	}
}

// novaCommands lists cmd/nova-* by walking the directory, never a list.
func novaCommands(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoErrorf(t, err, "reading cmd/: %v", err)
	var tools []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "nova-") {
			tools = append(tools, e.Name())
		}
	}
	return tools
}

func exeName(tool string) string {
	if runtime.GOOS == "windows" {
		return tool + ".exe"
	}
	return tool
}
