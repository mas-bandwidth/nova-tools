//go:build functional

package ci

import (
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"path/filepath"
	"strings"
	"testing"
)

// transcript_drift_functional_test.go builds three tools and runs their
// transcript lines: builds are the functional tier's (Glenn 2026-09-26,
// nova-tools#4328). The shape helpers and their negative controls stay in
// transcript_drift_test.go.

// TestFirstRunTranscriptsMatchInstalledBuild re-runs the four first-run
// transcript lines that drifted from the installed build and slipped past the
// per-binary shape tests, which is nova-tools#1506 (nova-pulse pool went with the frozen verbs): the three
// nova-decide route lines, nova-work's no-verb refusal and nova-review's
// version line. Each subtest builds the tool, runs the transcript's command,
// and compares what the transcript line promises against what the tool printed.
func TestFirstRunTranscriptsMatchInstalledBuild(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	md := readFile(t, filepath.Join(root, "docs", "TESTS.md"))

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
