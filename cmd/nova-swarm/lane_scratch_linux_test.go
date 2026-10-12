//go:build linux

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const laneScratchChildEnv = "NOVA_TEST_LANE_SCRATCH_CHILD"

// TestALaneScratchWorksUnderTheWallAndTmpIsDenied builds the wall exactly as a lane does —
// nativeSandboxArgv over a temporary slot and job, with the environment nativeChildEnvFrom
// gives — and runs three children under it, each through the real nova-sandbox body: the
// test binary itself as a helper calling os.CreateTemp (whose scratch lands under <job>/tmp),
// `go test` on a one-file package under <job>/repo (proving go's own temp work lands inside
// the wall), and a shell that writes to /tmp (refused). On a kernel with no landlock the wall
// refuses to run (no_sandbox); this follows pkg/sandbox/run_linux_test.go's walledRun skip —
// `if _, ok := landlockABI(); !ok { t.Skip("this kernel has no landlock; the wall cannot be
// built here") }` (TestNoLandlockRefusesOnLinux) — and never skips silently.
func TestALaneScratchWorksUnderTheWallAndTmpIsDenied(t *testing.T) {
	t.Parallel()
	if os.Getenv(laneScratchChildEnv) == "1" {
		f, err := os.CreateTemp("", "lane-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, "createtemp:", err)
			os.Exit(3)
		}
		fmt.Printf("SCRATCH=%s\n", f.Name())
		os.Exit(0)
	}

	require.NoError(t, buildShared(), "building the binaries these tests run")
	laneRequireWall(t)

	_, slot := aSlot(t)
	if got, err := filepath.EvalSymlinks(slot); err == nil {
		slot = got
	}
	label := "lane-scratch"
	jobDir := filepath.Join(slot, "jobs", label)
	data := filepath.Join(slot, "data")
	tmp := filepath.Join(jobDir, "tmp")
	repo := filepath.Join(jobDir, "repo")
	for _, d := range []string{jobDir, data, tmp, repo} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}

	// The bench home points at a sdk symlinked to the discovered Go root, so the wall grants
	// the Go toolchain the way a provisioned lane does (swarm.ToolchainRoots under home).
	top := t.TempDir()
	goroot := goGOROOT(t)
	require.NoError(t, os.Symlink(filepath.Dir(goroot), filepath.Join(top, "sdk")))
	cfg := nativeRunConfig{slotDir: slot, benchHome: top, benchOS: "linux"}

	t.Run("helper_scratch_inside_the_job", func(t *testing.T) {
		launch := []string{os.Args[0], "-test.run=^TestALaneScratchWorksUnderTheWallAndTmpIsDenied$", "-test.count=1"}
		code, out := laneWalledRun(t, cfg, data, jobDir, tmp, launch, []string{laneScratchChildEnv + "=1"})
		require.Equal(t, 0, code, "the helper child failed:\n%s", out)
		path := ""
		for _, line := range strings.Split(out, "\n") {
			if v, ok := strings.CutPrefix(line, "SCRATCH="); ok {
				path = v
			}
		}
		require.NotEmpty(t, path, "the helper printed no SCRATCH line:\n%s", out)
		assert.True(t, withinDir(path, tmp), "the helper's scratch is %q, want under the job's tmp %q", path, tmp)
	})

	t.Run("go_test_lands_inside_the_wall", func(t *testing.T) {
		write(t, filepath.Join(repo, "go.mod"), "module example.com/lane\n\ngo 1.22\n")
		write(t, filepath.Join(repo, "lane_test.go"), "package lane\n\nimport \"testing\"\n\nfunc TestLane(t *testing.T) {\n\tif testing.Short() {\n\t\tt.Skip(\"short\")\n\t}\n}\n")
		goBin := filepath.Join(goroot, "bin", "go")
		launch := []string{goBin, "-C", "./repo", "test", "-count=1", "-timeout=120s"}
		extra := []string{"GOFLAGS=-mod=mod", "GOPROXY=off"}
		code, out := laneWalledRun(t, cfg, data, jobDir, tmp, launch, extra)
		require.Equal(t, 0, code, "go test inside the wall failed:\n%s", out)
	})

	t.Run("tmp_write_is_denied", func(t *testing.T) {
		name := fmt.Sprintf("/tmp/lane-scratch-%d", time.Now().UnixNano())
		script := "echo x > " + name
		launch := []string{"/bin/sh", "-c", script}
		code, out := laneWalledRun(t, cfg, data, jobDir, tmp, launch, nil)
		require.NotEqual(t, 0, code, "a write to /tmp was allowed:\n%s", out)
		assert.Contains(t, out, "Permission denied", "the shell did not print Permission denied:\n%s", out)
		_, err := os.Stat(name)
		assert.True(t, os.IsNotExist(err), "the denied /tmp write exists after the run: %s", name)
	})
}

// laneRequireWall skips when this kernel cannot build the wall, exactly as
// pkg/sandbox/run_linux_test.go's walledRun does: no landlock is a skip that says so, never a
// silent one.
func laneRequireWall(t *testing.T) {
	t.Helper()
	out, err := exec.Command(builtSandbox, "check").Output()
	if err != nil || !strings.HasPrefix(string(out), "CHECK OK") || strings.Contains(string(out), "backend=none") {
		t.Skipf("this kernel has no landlock; the wall cannot be built here (pkg/sandbox/run_linux_test.go TestNoLandlockRefusesOnLinux): out=%q err=%v", out, err)
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (rule 2)")
	}
}

func goGOROOT(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "env", "GOROOT")
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.Output()
	require.NoError(t, err, "go env GOROOT failed")
	return strings.TrimSpace(string(out))
}

// withinDir reports whether path lies strictly inside dir.
func withinDir(path, dir string) bool {
	rp, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rd, rp)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// laneWalledRun builds the wall for one child (nativeSandboxArgv over the temporary slot and
// job, with the environment nativeChildEnvFrom gives) and runs it through the real
// nova-sandbox body, returning the child's exit code and combined output.
func laneWalledRun(t *testing.T, cfg nativeRunConfig, data, jobDir, tmp string, launch, extraEnv []string) (int, string) {
	t.Helper()
	argv := nativeSandboxArgv(launch, cfg, data, jobDir, tmp)
	env := append(nativeChildEnvFrom(os.Environ(), data, jobDir, tmp, "", "", "", "", nil), extraEnv...)
	cmd := exec.Command(builtSandbox, argv...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else if err != nil {
		code = -1
	}
	return code, out.String()
}
