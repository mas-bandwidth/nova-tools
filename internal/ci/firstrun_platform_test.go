package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// A first-run transcript whose expected output carries a platform-specific token
// must carry a platform line in its section: one line, beginning `Platform:`,
// that names the platform (or the machine state) the transcript was recorded on.
// The point is the one the issue is filed for — a reader of docs/TESTS.md is
// never invited to run a first-run transcript that cannot reproduce on the bench
// in front of them. The tokens are read out of the file, so no binary is run and
// this test guards the document rather than the sandbox body.
//
// Two tokens are read today:
//
//   - `backend=sandbox-exec` and `abi=` — the sandbox tool's transcript was
//     recorded on macOS; a Linux bench prints `backend=landlock`, an `abi=`
//     value and `hosts=`, `gpu=`, `used=` and `ancestors=` fields instead.
//   - `sandbox_probe` — a wall probe that failed; the nova-swarm section
//     documents a machine whose containment is broken, the last machine a friend
//     should be reading a quickstart on.
//
// The whole `## <tool>` section is read, not just `### First run`, because the
// nova-swarm wall-probe transcript lives under its own `### The wall at the
// launch seam` heading, and the platform line belongs in the section's prose
// where a stranger reads it before the fence.
func TestFirstRunTranscriptsNameTheirPlatform(t *testing.T) {
	root := repoRoot(t)
	md := readFile(t, filepath.Join(root, "docs", "TESTS.md"))

	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tool := e.Name()
		section, ok := onboarding.Section(md, tool)
		if !ok {
			continue
		}
		found++
		if !carriesPlatformToken(section) {
			continue
		}
		if !carriesPlatformLine(section) {
			t.Errorf("the `## %s` section carries a platform-specific transcript but no platform line; say which platform it was recorded on, in one line the section carries (a line beginning `Platform:`)", tool)
		}
	}
	if found == 0 {
		t.Fatal("no `## <tool>` sections found in docs/TESTS.md; this test was looking in the wrong place and would have passed by checking nothing")
	}
}

// carriesPlatformToken reports whether the section's transcript carries a
// platform-specific token, read out of the file rather than produced by a
// binary: `backend=sandbox-exec` and `abi=` (the sandbox tool's macOS backend
// and abi field) and `sandbox_probe` (a wall probe that failed).
func carriesPlatformToken(section string) bool {
	return strings.Contains(section, "backend=sandbox-exec") ||
		strings.Contains(section, "abi=") ||
		strings.Contains(section, "sandbox_probe")
}

// carriesPlatformLine reports whether the section carries a platform line: a
// line whose trimmed form begins `Platform:` and names something after it.
func carriesPlatformLine(section string) bool {
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Platform:") && strings.TrimSpace(strings.TrimPrefix(line, "Platform:")) != "" {
			return true
		}
	}
	return false
}
