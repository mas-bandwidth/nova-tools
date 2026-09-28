package ci

import (
	"strings"
	"testing"
)

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

// TestFirstRunTranscriptsNegativeControls tests that the helpers reject
// missing commands, empty transcript blocks, and malformed version outputs.
func TestFirstRunTranscriptsNegativeControls(t *testing.T) {
	t.Parallel()

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
