package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestFirstRunTranscriptsMatchInstalledBuild re-runs the four first-run
// transcript lines that drifted from the installed build and slipped past the
// per-binary shape tests, which is nova-tools#1506: nova-pulse pool, the three
// nova-decide route lines, nova-work's no-verb refusal and nova-review's
// version line. Each subtest builds the tool, runs the transcript's command,
// and compares what the transcript line promises against what the tool printed.
func TestFirstRunTranscriptsMatchInstalledBuild(t *testing.T) {
	root := repoRoot(t)
	md := readFile(t, filepath.Join(root, "docs", "TESTS.md"))

	t.Run("nova-pulse-pool", func(t *testing.T) {
		bin := buildTool(t, root, "nova-pulse")
		transcript := outputAfterCommand(md, "nova-pulse pool --sources cmd/nova-pulse/testdata/sources.tsv --root ./root")
		_, stdout, stderr := runBare(t, root, "nova-pulse", bin,
			[]string{"pool", "--sources", "cmd/nova-pulse/testdata/sources.tsv", "--root", t.TempDir()})
		assertShape(t, "nova-pulse pool", transcript, stdout, stderr)
	})

	t.Run("nova-decide-route", func(t *testing.T) {
		bin := buildTool(t, root, "nova-decide")
		commands := []string{
			"nova-decide route --unit-id card-41 --kind rebase --files 2 --packages 1 --no-jev",
			"nova-decide route --unit-id card-9 --kind fleet-chore --files 1 --guard --no-jev",
			"nova-decide route --unit-id s-1 --kind guard --files 1 --attempt johnny:timeout --no-jev",
		}
		for _, cmd := range commands {
			transcript := outputAfterCommand(md, cmd)
			args := strings.Fields(strings.TrimPrefix(cmd, "nova-decide "))
			_, stdout, stderr := runBare(t, root, "nova-decide", bin, args)
			assertShape(t, cmd, transcript, stdout, stderr)
		}
	})

	t.Run("nova-work-refusal", func(t *testing.T) {
		bin := buildTool(t, root, "nova-work")
		transcript := outputAfterCommand(md, "nova-work")
		_, stdout, stderr := runBare(t, root, "nova-work", bin, nil)
		assertShape(t, "nova-work", transcript, stdout, stderr)
	})

	t.Run("nova-review-version", func(t *testing.T) {
		bin := buildTool(t, root, "nova-review")
		transcript := outputAfterCommand(md, "nova-review version")
		_, stdout, _ := runBare(t, root, "nova-review", bin, []string{"version"})
		for _, line := range transcript {
			if !isVersionLine(line, "nova-review") {
				t.Errorf("TESTS.md nova-review version transcript line\n  %s\nis not the one version line (<tool> <identity> <goos>/<goarch> <go version>); the built binary prints:\n%s", line, stdout)
			}
		}
	})
}

// isVersionLine reports whether a transcript line is the one version line the
// Conventions promise: four tokens and then key=value extras, with the tool's
// name in field one and a go version in field four. The build identity in field
// two is a run's own -- it carries the vcs stamp of whatever build printed the
// line, so it is deliberately not compared.
func isVersionLine(line, tool string) bool {
	f := strings.Fields(line)
	if len(f) < 4 || f[0] != tool {
		return false
	}
	goos, goarch, ok := strings.Cut(f[2], "/")
	if !ok || goos == "" || goarch == "" {
		return false
	}
	return strings.HasPrefix(f[3], "go")
}

// outputAfterCommand returns the transcript's output lines for the given `$`
// command line: the lines that follow it until a blank line or the next `$`.
func outputAfterCommand(md, command string) []string {
	lines := strings.Split(md, "\n")
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "$ ") || strings.TrimPrefix(ln, "$ ") != command {
			continue
		}
		var out []string
		for j := i + 1; j < len(lines); j++ {
			nxt := lines[j]
			if strings.TrimSpace(nxt) == "" || strings.HasPrefix(nxt, "$ ") || strings.HasPrefix(nxt, "```") {
				break
			}
			out = append(out, nxt)
		}
		return out
	}
	return nil
}

// assertShape compares the promise of every transcript output line against the
// promise of the lines the tool actually printed (stdout and stderr together,
// because a refusal is a stderr line).
func assertShape(t *testing.T, name string, transcript []string, stdout, stderr string) {
	t.Helper()
	printed := append(strings.Split(stdout, "\n"), strings.Split(stderr, "\n")...)
	for _, tl := range transcript {
		promised := promise(tl)
		if promised == "" {
			continue
		}
		found := false
		for _, p := range printed {
			if promise(p) == promised {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("TESTS.md %s transcript line\n  %s\npromises %q, which the built binary never prints; it printed:\n  %s%s", name, tl, promised, stdout, stderr)
		}
	}
}

// promise reduces a line to the part a transcript promises. onboarding.Shape
// handles the uppercase event lines (two-token prefix, field names in order);
// for a line Shape declines (a refusal or a version line, whose first token is
// not uppercase) it is the leading two whitespace-separated tokens.
func promise(line string) string {
	if s := onboarding.Shape(line); s != "" {
		return s
	}
	f := strings.Fields(line)
	switch len(f) {
	case 0:
		return ""
	case 1:
		return f[0]
	default:
		return f[0] + " " + f[1]
	}
}
