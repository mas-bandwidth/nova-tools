package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// goSetupMarker is the line every copy of ci.yml's Go setup step carries: the
// newest-Go-under-~/sdk fallback.
const goSetupMarker = `sdk=$(ls -d "$HOME"/sdk/go*/bin`

// goSetupRuns returns the run: script of every ci.yml step that picks the Go
// toolchain.
func goSetupRuns(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
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
	if len(runs) != 6 {
		t.Fatalf("ci.yml has %d Go setup steps carrying %q, want 6 (update this test with the workflow)", len(runs), goSetupMarker)
	}
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
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, v := range tc.sdks {
				b := filepath.Join(home, "sdk", v, "bin")
				if err := os.MkdirAll(b, 0o755); err != nil {
					t.Fatal(err)
				}
				fake := "#!/bin/sh\ncase \"$1\" in version) echo \"go version " + v + " fake\";; env) echo \"/" + v + "/$2\";; esac\n"
				if err := os.WriteFile(filepath.Join(b, "go"), []byte(fake), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			// the fleet-probe copy also wants an sbcl
			if err := os.WriteFile(filepath.Join(home, ".local", "bin", "sbcl"), []byte("#!/bin/sh\necho SBCL fake\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.gomod != "" {
				if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte(tc.gomod), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ghPath := filepath.Join(dir, "github_path")
			cmd := exec.Command(bash, "-e", "-c", script)
			cmd.Dir = work
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GITHUB_PATH=" + ghPath, "RUNNER_NAME=r"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("step %d, %s: %v\n%s", i, tc.name, err, out)
			}
			added, _ := os.ReadFile(ghPath)
			wantBin := filepath.Join(home, "sdk", tc.want, "bin")
			if first, _, _ := strings.Cut(string(added), "\n"); first != wantBin {
				t.Errorf("step %d, %s: GITHUB_PATH first line %q, want %q", i, tc.name, first, wantBin)
			}
			receipt := "GO SETUP want=" + tc.wantName + " go=" + filepath.Join(wantBin, "go") +
				" gomodcache=/" + tc.want + "/GOMODCACHE gocache=/" + tc.want + "/GOCACHE\n"
			if !strings.Contains(string(out), receipt) {
				t.Errorf("step %d, %s: output lacks %q:\n%s", i, tc.name, receipt, out)
			}
		}
	}
}
