//go:build functional

package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ci_gosetup_functional_test.go runs every copy of ci.yml's Go setup step
// under bash, twenty-four processes: exec of whole programs is the functional
// tier's (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s).

// goSetupMarker is the line every copy of ci.yml's Go setup step carries: the
// newest-Go-under-~/sdk fallback.
const goSetupMarker = `sdk=$(ls -d "$HOME"/sdk/go*/bin`

// goSetupRuns returns the run: script of every ci.yml step that picks the Go
// toolchain.
func goSetupRuns(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	require.NoError(t, err)
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &wf))
	var runs []string
	for _, j := range wf.Jobs {
		for _, s := range j.Steps {
			if strings.Contains(s.Run, goSetupMarker) {
				runs = append(runs, s.Run)
			}
		}
	}
	return runs
}

// TestCIGoSetupPrefersTheGoModToolchain (#4080) runs every copy of ci.yml's Go
// setup step under bash with a fake HOME: the Go under ~/sdk that go.mod names
// (its toolchain line, else its go line) wins over the newest one, the newest
// one is still the fallback when ~/sdk does not hold it, and the step's receipt
// names the Go, its GOMODCACHE and its GOCACHE. On space the runner took the
// newest sdk Go while the release build beside it switched toolchains in the
// same caches.
func TestCIGoSetupPrefersTheGoModToolchain(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the step runs under bash on the self-hosted Linux and macOS runners")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	runs := goSetupRuns(t)
	// 8: the test-race-queue job, and its copy of the step, went with #4866; the lisp job
	// carries one because its steps run tools/ci (go run), which needs Go on PATH.
	require.Equal(t, 8, len(runs), "ci.yml has %d Go setup steps carrying %q, want 8 (update this test with the workflow)", len(runs), goSetupMarker)
	// A step copied verbatim into another job (the functional job's is the
	// test job's) is the same script: it runs once, so a copy costs the unit
	// tier's one-second test budget nothing.
	seen := map[string]bool{}
	unique := runs[:0:0]
	for _, run := range runs {
		if !seen[run] {
			seen[run] = true
			unique = append(unique, run)
		}
	}
	runs = unique
	expr := regexp.MustCompile(`\$\{\{[^}]*\}\}`)
	cases := []struct {
		name, gomod string
		sdks        []string
		want        string // the sdk dir picked
		wantName    string // the want= field
	}{
		{"toolchain line", "module m\n\ngo 1.26.6\n\ntoolchain go1.27.1\n", []string{"go1.26.6", "go1.27.1", "go1.28.0"}, "go1.27.1", "go1.27.1"},
		{"go line", "module m\n\ngo 1.26.6\n", []string{"go1.26.6", "go1.27.1"}, "go1.26.6", "go1.26.6"},
		{"not under sdk", "module m\n\ngo 1.26.6\n\ntoolchain go1.27.1\n", []string{"go1.26.6", "go1.28.0"}, "go1.28.0", "go1.27.1"},
		{"no go.mod", "", []string{"go1.26.6", "go1.27.1"}, "go1.27.1", "none"},
	}
	for i, run := range runs {
		script := expr.ReplaceAllString(run, "x")
		for _, tc := range cases {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			work := filepath.Join(dir, "work")
			for _, d := range []string{home, work, filepath.Join(home, ".local", "bin")} {
				require.NoError(t, os.MkdirAll(d, 0o755))
			}
			for _, v := range tc.sdks {
				b := filepath.Join(home, "sdk", v, "bin")
				require.NoError(t, os.MkdirAll(b, 0o755))
				fake := "#!/bin/sh\ncase \"$1\" in version) echo \"go version " + v + " fake\";; env) echo \"/" + v + "/$2\";; esac\n"
				require.NoError(t, os.WriteFile(filepath.Join(b, "go"), []byte(fake), 0o755))
			}
			// the fleet-probe copy also wants an sbcl
			require.NoError(t, os.WriteFile(filepath.Join(home, ".local", "bin", "sbcl"), []byte("#!/bin/sh\necho SBCL fake\n"), 0o755))
			if tc.gomod != "" {
				require.NoError(t, os.WriteFile(filepath.Join(work, "go.mod"), []byte(tc.gomod), 0o644))
			}
			ghPath := filepath.Join(dir, "github_path")
			cmd := exec.Command(bash, "-e", "-c", script)
			cmd.Dir = work
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GITHUB_PATH=" + ghPath, "RUNNER_NAME=r"}
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "step %d, %s: %v\n%s", i, tc.name, err, out)
			added, _ := os.ReadFile(ghPath)
			wantBin := filepath.Join(home, "sdk", tc.want, "bin")
			first, _, _ := strings.Cut(string(added), "\n")
			assert.Equal(t, wantBin, first, "step %d, %s: GITHUB_PATH first line %q, want %q", i, tc.name, first, wantBin)
			receipt := "GO SETUP want=" + tc.wantName + " go=" + filepath.Join(wantBin, "go") +
				" gomodcache=/" + tc.want + "/GOMODCACHE gocache=/" + tc.want + "/GOCACHE\n"
			assert.Contains(t, string(out), receipt, "step %d, %s: output lacks %q:\n%s", i, tc.name, receipt, out)
		}
	}
}
