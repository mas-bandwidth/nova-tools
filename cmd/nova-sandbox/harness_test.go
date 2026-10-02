package main

// harness_test.go is what this package's tests share: the job (one worker's
// directories, made under t.TempDir), and the tool and its verbs as testkit.Main
// values, so that every run in every file is one testkit.Ran -- r.Code, r.Stdout,
// r.Stderr -- and every check on it is testify's or the Ran's own. A bench that
// stands one verb's seams up (runBench, winBench, egressBench, the worktree job)
// stays in that verb's file, beside the tests that read it.

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

// Every test here runs the REAL thing on this Mac: a real sandbox-exec, a real profile
// generated from profiles/darwin.sb.tmpl, a real wrapped command. On another platform
// each one skips BY NAME rather than silently, because a green from a suite that ran
// nothing reads exactly like a green from one that ran (test on multiple platforms).
func needDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: the darwin body needs sandbox-exec, which is macOS only", runtime.GOOS)
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("skipped: sandbox-exec is not on this machine")
	}
}

// job is one worker's shape: a write set with its data home, a read set, and a secret
// directory in NEITHER list — the thing the wall exists to keep unreadable.
type job struct{ base, write, read, home, secret, outside string }

func newJob(t *testing.T) job {
	t.Helper()
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	j := job{base: base,
		write:   filepath.Join(base, "w"),
		read:    filepath.Join(base, "r"),
		outside: filepath.Join(base, "outside"),
	}
	j.home = filepath.Join(j.write, "home")
	secretDir := filepath.Join(base, "secret")
	j.secret = filepath.Join(secretDir, "env")
	for _, d := range []string{j.write, j.read, j.home, j.outside} {
		testkit.Mkdir(t, d)
	}
	testkit.WriteFile(t, j.secret, "not-a-real-key\n", 0o600)
	return j
}

func (j job) env(extra ...string) []string {
	return append([]string{
		"HOME=" + j.home,
		"PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin",
	}, extra...)
}

// run is nova-sandbox in process with this job's environment; runEnv with another.
func (j job) run(t *testing.T, args ...string) testkit.Ran {
	t.Helper()
	return j.runEnv(t, j.env(), args...)
}

func (j job) runEnv(t *testing.T, env []string, args ...string) testkit.Ran {
	t.Helper()
	return withEnv(run, env).Do(t, args...)
}

// withEnv is an entry point that also takes the environment (the tool's run, the run
// verb's runVerb) bound to one environment, as the testkit.Main every check runs on.
func withEnv(main func(args []string, stdin io.Reader, stdout, stderr io.Writer, env []string) int, env []string) testkit.Main {
	return func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return main(args, stdin, stdout, stderr, env)
	}
}

// novaSandbox is the tool with no environment at all, for the tests that name every path
// themselves.
var novaSandbox = withEnv(run, nil)

// swap sets the package seam *seam to v for the rest of the test and puts the old value
// back when the test ends. A test that swaps a seam cannot run in parallel: it stays on
// internal/ci's serial-tests allowlist with the seams it swaps named.
func swap[T any](t *testing.T, seam *T, v T) {
	t.Helper()
	old := *seam
	t.Cleanup(func() { *seam = old })
	*seam = v
}

// statErr is os.Stat's error alone, so that "it is not there" (require.Error), "it is
// there" (require.NoError) and "it is gone" (assert.ErrorIs fs.ErrNotExist) are each one
// line that keeps its own question and its own sentence.
func statErr(path string) error {
	_, err := os.Stat(path)
	return err
}

// saw is what a reader saw running one documented command under env, as
// onboarding.Compare takes it.
func saw(t *testing.T, env []string, args ...string) onboarding.Result {
	t.Helper()
	return onboarding.Result(withEnv(run, env).Do(t, args...).Result)
}

// hostPath is the one variable the verb-level tests hand the tool: this host's PATH, so a
// command resolves (rule 5) and nothing else of the test's environment leaks in.
func hostPath() []string { return []string{"PATH=" + os.Getenv("PATH")} }

// shell is the command the tests wrap, and its flag: /bin/sh -c on unix, cmd.exe /c on
// windows. The tests that RUN a script inside the wall are darwin's; the ones that assert a
// REFUSAL run everywhere, and on windows a hard-coded /bin/sh made them pass on
// "/bin/sh is on no PATH entry" — a green about the wrong refusal.
func (j job) shell(t *testing.T) []string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return []string{"/bin/sh", "-c"}
	}
	for _, candidate := range []string{os.Getenv("COMSPEC"), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")} {
		if candidate == "" {
			continue
		}
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return []string{candidate, "/c"}
		}
	}
	t.Skip("skipped: this windows machine has no cmd.exe, and rule 5 resolves the command before any policy")
	return nil
}

// noopScript and touchScript are the two scripts these tests need, in the shell of the
// platform: one that does nothing and one that would create a file. The second is what makes
// "the command did NOT run" an assertion rather than a hope.
func noopScript() string {
	if runtime.GOOS == "windows" {
		return "exit /b 0"
	}
	return "true"
}

func touchScript(path string) string {
	if runtime.GOOS == "windows" {
		return `type nul > "` + path + `"`
	}
	return "touch '" + path + "'"
}

// wrapped runs one shell script INSIDE the wall with this job's lists.
func (j job) wrapped(t *testing.T, script string, extraEnv ...string) testkit.Ran {
	t.Helper()
	args := []string{"--read", j.read, "--write", j.write, "--"}
	args = append(args, j.shell(t)...)
	args = append(args, script)
	return j.runEnv(t, j.env(extraEnv...), args...)
}
