package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// host is every question the witness puts to the machine it runs on, and the
// one thing it does to it (Kill). The real machine is osHost; a test hands in a
// host whose process table, systemctl, df and tools answer from a script, so the
// verdict on a bench is reproduced without the bench.
//
// A method reads; none but Kill, MkdirTemp/MkdirAll and RemoveUnder writes, and
// those three serve the two probes that run inside the sandbox wall: each makes
// a scratch directory of its own and removes exactly that directory.
type host interface {
	// OS is the operating system's name in lower case: linux, darwin, windows.
	OS() string
	// Environ is the process environment, KEY=value.
	Environ() []string
	// SourceEnv returns the variables a shell holds after it sources file with
	// environ as its environment (only those the file set or changed), and what
	// the sourcing printed (its errors and its own output), which the caller shows.
	SourceEnv(file string, environ []string) (set map[string]string, said string, err error)

	Stat(path string) (fs.FileInfo, error)
	ReadFile(path string) ([]byte, error)
	// Glob is filepath.Glob: the matches in lexical order.
	Glob(pattern string) ([]string, error)
	// EvalSymlinks is readlink -f: the path with every link followed.
	EvalSymlinks(path string) (string, error)
	MkdirTemp(dir, pattern string) (string, error)
	MkdirAll(path string, perm fs.FileMode) error
	// RemoveUnder removes path, which must be strictly below root: a directory
	// the witness made is removed through the one check that refuses any other.
	RemoveUnder(root, path string) error

	// LookPath is `command -v`: the first executable file called name in the
	// directories of pathEnv, or false.
	LookPath(name, pathEnv string) (string, bool)
	// Run starts a process and waits for it.
	Run(spec runSpec) runResult
	// ProcCgroup is the text of /proc/<pid>/cgroup, false when it cannot be read.
	ProcCgroup(pid string) (string, bool)
	// Kill sends SIGTERM to the process. It is the only mutation of the machine
	// the witness makes, and only under --apply.
	Kill(pid int) error
}

// runSpec is one process to run: the program, its arguments, and its whole
// environment.
type runSpec struct {
	name string
	args []string
	env  []string
}

// runResult is what a process returned. err is set only when it could not be
// started or was cut off at the deadline; a process that ran and exited
// non-zero has err nil and its code in code.
type runResult struct {
	stdout, stderr string
	code           int
	err            error
}

// ok is a process that ran and exited zero.
func (r runResult) ok() bool { return r.err == nil && r.code == 0 }

// combined is the output of `cmd 2>&1`: stdout then stderr.
func (r runResult) combined() string { return r.stdout + r.stderr }

// probeTimeout bounds every process the witness starts, so a bench whose tool
// hangs is reported as drift and never hangs the witness.
const probeTimeout = 60 * time.Second

// osHost is the machine the witness is running on.
type osHost struct{}

func (osHost) OS() string                             { return runtime.GOOS }
func (osHost) Environ() []string                      { return os.Environ() }
func (osHost) Stat(p string) (fs.FileInfo, error)     { return os.Stat(p) }
func (osHost) ReadFile(p string) ([]byte, error)      { return os.ReadFile(p) }
func (osHost) Glob(p string) ([]string, error)        { return filepath.Glob(p) }
func (osHost) EvalSymlinks(p string) (string, error)  { return filepath.EvalSymlinks(p) }
func (osHost) MkdirTemp(d, p string) (string, error)  { return os.MkdirTemp(d, p) }
func (osHost) MkdirAll(p string, m fs.FileMode) error { return os.MkdirAll(p, m) }
func (osHost) RemoveUnder(root, p string) error       { return safepath.RemoveUnder(root, p) }
func (osHost) ProcCgroup(pid string) (string, bool) {
	b, err := os.ReadFile("/proc/" + pid + "/cgroup")
	return string(b), err == nil
}

func (osHost) Kill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

func (osHost) LookPath(name, pathEnv string) (string, bool) {
	if strings.Contains(name, "/") {
		return name, isExecutableFile(name)
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if isExecutableFile(p) {
			return p, true
		}
	}
	return "", false
}

// isExecutableFile is a regular file, reached through any links, with an
// execute bit.
func isExecutableFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

func (osHost) Run(s runSpec) runResult {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := subproc.Context(ctx, s.name, s.args...)
	cmd.Env = s.env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := runResult{stdout: out.String(), stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		res.err = ctx.Err()
	case errors.As(err, &ee):
		res.code = ee.ExitCode()
	default:
		res.err = err
	}
	return res
}

// SourceEnv runs `bash -c '. "$1" >&2; env'` (sh where there is no bash): the card
// environment is the one a person gets by sourcing the file in bash, whatever
// the caller's PATH held. The file's own output and every error it raises go to
// stderr and come back as said, for the witness to show, never into the
// environment it reads. A value spanning lines is rejoined from the lines that
// are not assignments.
func (h osHost) SourceEnv(file string, environ []string) (map[string]string, string, error) {
	shell := "sh"
	if p, err := exec.LookPath("bash"); err == nil {
		shell = p
	}
	res := h.Run(runSpec{name: shell, args: []string{"-c", `. "$1" >&2; env`, "sh", file}, env: environ})
	if res.err != nil {
		return nil, res.stderr, res.err
	}
	before := maps.Clone(parseEnv(environ))
	changed := map[string]string{}
	for k, v := range parseEnv(strings.Split(strings.TrimRight(res.stdout, "\n"), "\n")) {
		switch k {
		case "_", "SHLVL", "PWD", "OLDPWD":
			continue
		}
		if old, had := before[k]; !had || old != v {
			changed[k] = v
		}
	}
	return changed, res.stderr, nil
}

// parseEnv reads KEY=value lines. A line that does not begin an assignment is a
// continuation of the value before it.
func parseEnv(lines []string) map[string]string {
	out := map[string]string{}
	last := ""
	for _, l := range lines {
		k, v, ok := strings.Cut(l, "=")
		if ok && isEnvName(k) {
			out[k] = v
			last = k
			continue
		}
		if last != "" {
			out[last] += "\n" + l
		}
	}
	return out
}

func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// envList is a map as a KEY=value list, in key order.
func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, k+"="+m[k])
	}
	return out
}
