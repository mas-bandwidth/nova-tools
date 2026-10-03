package cardtree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// StepBudget bounds one program, one POST command or one git of a script step; the card's own
// deadline bounds the whole run.
const StepBudget = 10 * time.Minute

// Wall is a script step's own wall (docs/SPEC-SPRINT.md, a card is a tree of steps), tighter
// than a child's: the network denied (`--net-deny`, which the wall refuses where it cannot
// enforce it), no credential in the environment (ScrubEnv; the step needs no model), and the
// write set the checkout and a private temp only. Bin is the wall binary; "" runs with no
// wall, which only an explicit --no-wall asks for. Read is the read flags the step needs (the
// built programs, the toolchain, the checkout's borrowed objects): `--read <dir>` and
// `--read-noexec <dir>` pairs. Tmp is the private temp; HOME is <Tmp>/home.
type Wall struct {
	Bin  string
	Read []string
	Tmp  string
}

// Argv is the wall's argv around one command run in the checkout dir.
func (w Wall) Argv(dir string, argv []string) []string {
	out := []string{"--write", w.Tmp, "--write", dir, "--tmp", w.Tmp, "--cwd", dir, "--net-deny"}
	out = append(append(out, w.Read...), "--")
	return append(out, argv...)
}

// Env is the environment of a command in the step's wall: the caller's, scrubbed of every
// credential (ScrubEnv), HOME the private one, git kept off any configuration but the
// checkout's own, Go's and the shell's temps the private one (a caller's GOTMPDIR or TMPDIR,
// a CI runner's own directory, is outside the wall's write set), Go's build cache a private
// one in the temp (the bench's shared cache is
// neither read nor written by a step, so no card's program can poison it), modules read
// from the module cache the wall reads and never fetched.
func (w Wall) Env(env []string) []string {
	var kept []string
	for _, kv := range ScrubEnv(env) {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "GOCACHE", "GOFLAGS", "GOPROXY", "GOTMPDIR", "TMPDIR":
		default:
			kept = append(kept, kv)
		}
	}
	return append(kept, "HOME="+filepath.Join(w.Tmp, "home"), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GOCACHE="+filepath.Join(w.Tmp, "go-build"), "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOTMPDIR="+w.Tmp, "TMPDIR="+w.Tmp)
}

// userinfoRE is a URL carrying a password: `redis://:pw@host`, `https://user:pw@host`.
var userinfoRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^/@\s]*:[^/@\s]*@`)

// secretWords mark a variable that may carry a credential: a script step runs with none.
var secretWords = []string{"KEY", "TOKEN", "SECRET", "AUTH", "PASSWORD", "PASSWD", "CREDENTIAL"}

// ScrubEnv is env without a variable whose name carries a credential word or whose value holds
// a URL's `user:password@`, and without HOME, GIT_CONFIG_GLOBAL and GIT_CONFIG_NOSYSTEM, which
// the step's wall sets for itself. It is a denylist: run by native, the executor's
// environment is already the child's allowlist (keepNativeEnv), and this is the second
// filter; run directly, `nova-swarm step` passes the caller's other variables through.
func ScrubEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		up := strings.ReplaceAll(strings.ToUpper(name), "AUTHOR", "") // GIT_AUTHOR_NAME is an identity, no credential
		drop := up == "HOME" || up == "GIT_CONFIG_GLOBAL" || up == "GIT_CONFIG_NOSYSTEM" || userinfoRE.MatchString(value)
		for _, w := range secretWords {
			drop = drop || strings.Contains(up, w)
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// buildEnv is the toolchain's environment for a program's source: no credential, no cgo, no
// module fetched, no inherited GOFLAGS (a -toolexec there would run anything).
func buildEnv(env []string) []string {
	return append(ScrubEnv(env), "HOME="+os.Getenv("HOME"), "CGO_ENABLED=0", "GOFLAGS=", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local")
}

// OSSys is the machine's Sys: the toolchain builds outside the wall, and the card's code and
// git's commit run in w, one wall per command, with no shell.
func OSSys(work string, w Wall) Sys {
	walled := func(dir string, argv ...string) (string, int, error) {
		env := w.Env(os.Environ())
		if err := os.MkdirAll(filepath.Join(w.Tmp, "home"), 0o700); err != nil {
			return "", -1, err
		}
		if w.Bin != "" {
			argv = append([]string{w.Bin}, w.Argv(dir, argv)...)
		}
		return osExec(dir, env, argv)
	}
	run := func(dir string, argv ...string) error {
		_, _, err := walled(dir, argv...)
		return err
	}
	return Sys{
		Build: func(dir string, argv ...string) error {
			_, _, err := osExec(dir, buildEnv(os.Environ()), argv)
			return err
		},
		Run:  run,
		Work: work,
		Commit: func(dir string, paths []string, message string) (string, error) {
			if err := run(dir, append([]string{"git", "add", "-A", "--"}, paths...)...); err != nil {
				return "", err
			}
			switch _, code, err := walled(dir, "git", "diff", "--cached", "--quiet"); {
			case err == nil:
				return "", nil // nothing staged: the step changed nothing
			case code != 1:
				return "", fmt.Errorf("git diff --cached: %v", err)
			}
			if err := run(dir, "git", "commit", "-q", "--no-verify", "-m", message); err != nil {
				return "", err
			}
			out, _, err := walled(dir, "git", "rev-parse", "HEAD")
			if err != nil {
				return "", fmt.Errorf("git rev-parse HEAD: %v", err)
			}
			return strings.TrimSpace(out), nil
		},
	}
}

// osExec runs argv in dir with env and no shell: its stdout, its exit code (-1 when it did
// not run) and an error carrying its last line of output when it did not exit 0.
func osExec(dir string, env, argv []string) (string, int, error) {
	if len(argv) == 0 {
		return "", -1, errors.New("no command")
	}
	cmd, cancel := subproc.CommandFor(context.Background(), StepBudget, argv[0], argv[1:]...)
	defer cancel()
	cmd.Dir, cmd.Env = dir, env
	var out, both bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &both
	err := cmd.Run()
	if err != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		return out.String(), code, fmt.Errorf("%v: %s", err, oneline.Cap(lastLine(out.String()+"\n"+both.String()), 300))
	}
	return out.String(), 0, nil
}

// lastLine is the last line of output that is the command's own, not the wall's receipt.
func lastLine(s string) string {
	last := ""
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "SANDBOX ") {
			last = l
		}
	}
	return last
}
