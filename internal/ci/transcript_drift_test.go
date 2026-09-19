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
		transcript, found := outputAfterCommand(md, "nova-pulse pool --sources cmd/nova-pulse/testdata/sources.tsv --root ./root")
		if !found {
			t.Fatalf("TESTS.md missing command %q", "nova-pulse pool --sources cmd/nova-pulse/testdata/sources.tsv --root ./root")
		}
		if len(transcript) == 0 {
			t.Fatalf("TESTS.md has empty transcript for nova-pulse pool")
		}
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
			transcript, found := outputAfterCommand(md, cmd)
			if !found {
				t.Fatalf("TESTS.md missing command %q", cmd)
			}
			if len(transcript) == 0 {
				t.Fatalf("TESTS.md has empty transcript for %q", cmd)
			}
			args := strings.Fields(strings.TrimPrefix(cmd, "nova-decide "))
			_, stdout, stderr := runBare(t, root, "nova-decide", bin, args)
			assertShape(t, cmd, transcript, stdout, stderr)
		}
	})

	t.Run("nova-work-refusal", func(t *testing.T) {
		bin := buildTool(t, root, "nova-work")
		transcript, found := outputAfterCommand(md, "nova-work")
		if !found {
			t.Fatalf("TESTS.md missing command %q", "nova-work")
		}
		if len(transcript) == 0 {
			t.Fatalf("TESTS.md has empty transcript for nova-work")
		}
		code, stdout, stderr := runBare(t, root, "nova-work", bin, nil)
		if code == 0 {
			t.Fatalf("nova-work bare must exit non-zero on refusal, got 0")
		}
		assertShape(t, "nova-work", transcript, stdout, stderr)
	})

	t.Run("nova-review-version", func(t *testing.T) {
		bin := buildTool(t, root, "nova-review")
		transcript, found := outputAfterCommand(md, "nova-review version")
		if !found {
			t.Fatalf("TESTS.md missing command %q", "nova-review version")
		}
		if len(transcript) == 0 {
			t.Fatalf("TESTS.md has empty transcript for nova-review version")
		}
		code, stdout, stderr := runBare(t, root, "nova-review", bin, []string{"version"})
		if code != 0 {
			t.Fatalf("nova-review version exit = %d, want 0; stderr=%s", code, stderr)
		}
		stdoutLine := strings.TrimSpace(stdout)
		if !isVersionLine(stdoutLine, "nova-review") {
			t.Fatalf("nova-review version stdout is not a valid version line: %q", stdoutLine)
		}
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
// If the command is not present in md, found is false.
func outputAfterCommand(md, command string) (lines []string, found bool) {
	allLines := strings.Split(md, "\n")
	for i, ln := range allLines {
		if !strings.HasPrefix(ln, "$ ") || strings.TrimPrefix(ln, "$ ") != command {
			continue
		}
		found = true
		for j := i + 1; j < len(allLines); j++ {
			nxt := allLines[j]
			if strings.TrimSpace(nxt) == "" || strings.HasPrefix(nxt, "$ ") || strings.HasPrefix(nxt, "```") {
				break
			}
			lines = append(lines, nxt)
		}
		return lines, true
	}
	return nil, false
}

// assertShape compares the promise of every transcript output line against the
// promise of the lines the tool actually printed (stdout and stderr together,
// because a refusal is a stderr line).
func assertShape(t *testing.T, name string, transcript []string, stdout, stderr string) {
	t.Helper()
	if len(transcript) == 0 {
		t.Fatalf("TESTS.md %s transcript has no expected output lines", name)
	}
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

// TestFirstRunTranscriptsNegativeControls tests that the helpers reject
// missing commands, empty transcript blocks, and malformed version outputs.
func TestFirstRunTranscriptsNegativeControls(t *testing.T) {
	fixture := `$ command-empty
$ command-with-output
OUTPUT OK val=1
`
	// 1. Missing command returns found=false
	if _, found := outputAfterCommand(fixture, "command-nonexistent"); found {
		t.Errorf("outputAfterCommand found nonexistent command")
	}

	// 2. Command with empty output block returns found=true but len == 0
	lines, found := outputAfterCommand(fixture, "command-empty")
	if !found {
		t.Errorf("outputAfterCommand failed to find command-empty")
	}
	if len(lines) != 0 {
		t.Errorf("expected empty lines for command-empty, got %v", lines)
	}

	// 3. Command with output returns lines
	lines, found = outputAfterCommand(fixture, "command-with-output")
	if !found || len(lines) != 1 || lines[0] != "OUTPUT OK val=1" {
		t.Errorf("expected ['OUTPUT OK val=1'], got found=%v, lines=%v", found, lines)
	}

	// 4. isVersionLine negative controls
	badVersions := []struct {
		name string
		line string
		tool string
	}{
		{"empty", "", "nova-review"},
		{"single-token", "nova-review", "nova-review"},
		{"two-tokens", "nova-review devel", "nova-review"},
		{"three-tokens", "nova-review devel darwin/arm64", "nova-review"},
		{"wrong-tool", "nova-pulse devel darwin/arm64 go1.26.1", "nova-review"},
		{"no-arch-slash", "nova-review devel darwinarm64 go1.26.1", "nova-review"},
		{"empty-os", "nova-review devel /arm64 go1.26.1", "nova-review"},
		{"empty-arch", "nova-review devel darwin/ go1.26.1", "nova-review"},
		{"non-go-runtime", "nova-review devel darwin/arm64 rustc1.80.0", "nova-review"},
	}
	for _, tc := range badVersions {
		if isVersionLine(tc.line, tc.tool) {
			t.Errorf("isVersionLine(%q, %q) returned true, want false (%s)", tc.line, tc.tool, tc.name)
		}
	}
}
