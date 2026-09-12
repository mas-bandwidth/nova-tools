package ci

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// examples/run-worker.sh is the caller half of docs/SPEC-SANDBOX.md's "what
// every launcher must do to be wrappable". These witnesses drive it with FAKE
// harnesses, a fake sandbox and synthetic key data: no real model, key or
// network anywhere. The fake sandbox consumes the launcher's sandbox flags and
// execs the harness it was handed, so the launcher's process tree, deadline,
// status propagation and containment are all exercised for real.

// fakeSandbox is a stand-in for nova-sandbox that swallows the sandbox flags
// and execs the harness, with no wall.
const fakeSandbox = `#!/bin/zsh
while [ $# -gt 0 ] && [ "$1" != "--" ]; do
  case "$1" in
    --read|--write|--cwd|--name) shift 2 ;;
    *) shift ;;
  esac
done
[ "$1" = "--" ] && shift
exec "$@"
`

// writeExec writes script to dir/name (with a leading shebang) and makes it
// executable, returning its path.
func writeExec(t *testing.T, dir, name, script string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// launcherRun runs examples/run-worker.sh with args, returning its exit code,
// stdout and stderr.
func launcherRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	script := filepath.Join(repoRoot(t), "examples", "run-worker.sh")
	cmd := exec.Command("/bin/zsh", append([]string{script}, args...)...)
	// The launcher reads WORKER_* and NOVA_* from the environment as flag
	// defaults; a test host that happens to export them would silently change
	// every witness. Strip them so each flag in args is the only source.
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "WORKER_") || strings.HasPrefix(e, "NOVA_") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("running run-worker.sh: %v", err)
		}
	}
	return exit, out.String(), errb.String()
}

// pidAlive reports whether a pid names a live process, the way internal/swarm's
// Alive does: signal 0 asks the kernel and sends nothing.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// TestLauncherExampleStatusPropagation is the item-1 witness: a failed harness
// must be reported as failed. Today the background job is `(...) | cat >log`,
// so wait reports cat's status and a harness exiting 17 is reported 0.
func TestLauncherExampleStatusPropagation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run-worker.sh is a zsh example with no Windows equivalent")
	}
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker")
	if err := os.MkdirAll(worker, 0o755); err != nil {
		t.Fatal(err)
	}
	harness := writeExec(t, dir, "harness", "#!/bin/zsh\nexit 17\n")
	sandbox := writeExec(t, dir, "sandbox", fakeSandbox)

	exit, stdout, stderr := launcherRun(t,
		"--worker", worker,
		"--harness", harness,
		"--sandbox", sandbox,
		"--", "exit seventeen",
	)

	if exit != 17 {
		t.Errorf("launcher exit = %d, want 17 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "exit=17") {
		t.Errorf("launcher printed no status line naming 17:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
}
