// Package bench runs one command on a Linux bench against a copy of a local
// tree: `nova-ci bench run`.
//
// THE HURT. Every card, every read and the coordinator ran Go on a bench with a
// recipe typed into each brief: ssh <bench> 'mkdir -p <d>' && rsync -a --delete
// <repo>/ <bench>:<d>/repo/ && ssh <bench> 'cd <d>/repo && export GOCACHE=...
// GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 && nice -n 19 go ...', a second
// bench by hand when the first did not answer, then ssh <bench> 'rm -rf <d>'.
// Each copy of the recipe was a place to get a path, a variable or the cleanup
// wrong (the owner, 2026-10-05: "We need to get away from these one shot shell
// scripts."). This package is the recipe, once, behind a Transport the tests
// fake.
//
// THE RUN, per host, in order:
//
//  1. make: one ssh that makes the root and a fresh run directory under it
//     with mktemp -d, so two runs never share a directory and the run knows
//     exactly the one it made. A host that does not answer (ssh's own exit
//     255, or ssh not starting) is passed over for the fallback; a host that
//     answers and refuses is the end of the run, never a reason to try another.
//  2. copy: the tree into <run>/repo, .git left out unless WithGit; or, with a
//     Stage, the tree staged from the bench's own mirror at one commit
//     (stage_mirror.go), so only the sha crosses the wire. Either way the
//     run's Stage says what it did, and a refusal is a *StageError naming the
//     step, its exit, its stderr's tail and the wall time.
//  3. exec: cd <run>/repo, then the command under nice -n 19 with GOCACHE,
//     GOFLAGS=-mod=readonly and NOVA_TEST_NO_HOST=1, its output streamed to the
//     caller as it arrives. Its exit status is the run's.
//  4. remove: rm -rf of the run directory step 1 printed and nothing else,
//     whatever happened in 2 and 3, under a context the caller's cancellation
//     does not end, so an interrupted run still cleans up after itself.
//
// THE MODEL. tla/BenchRun.tla is the run as a state
// machine: TLC holds OnlyTheMadeDirIsRemoved, AtMostOneHostAnswers,
// FallbackOnlyOnNoAnswer, ExitIsTheCommands and NothingLeftBehind on two hosts
// (MCBenchRun.cfg), and its reversed witness MCBenchRunBrokenNoRemove.cfg, a
// failed copy that skips the deferred remove, must break NothingLeftBehind.
package bench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Defaults of the paths on the bench, relative to the login's home.
const (
	// DefaultRoot is where the run directories are made.
	DefaultRoot = "nova-bench/runs"
	// DefaultCache is the bench's Go build cache, shared by every run on it.
	DefaultCache = "nova-bench/cache/go-build"
	// Nice is the niceness of the command: the bench is shared with the work
	// of every other card on it.
	Nice = 19
	// NoAnswer is ssh's own exit status: it could not reach or log in to the
	// host, so the remote command never ran.
	NoAnswer = 255
	// StepBudget bounds the make and remove steps, which are one short remote
	// command each; the copy and the command itself are bounded only by the
	// caller's context.
	StepBudget = 2 * time.Minute
)

// Env is the environment the command runs under, after GOCACHE: the same on
// every bench, so a run is a run wherever it lands.
var Env = []string{"GOFLAGS=-mod=readonly", "NOVA_TEST_NO_HOST=1"}

// Transport is what a run asks of the wire. The production transport is ssh
// alone (Exec), the copy a tar stream on its stdin; the tests pass a fake
// that records each call.
type Transport interface {
	// Shell runs one line under the login shell on host, streaming its output.
	// The int is the remote status (NoAnswer when ssh itself failed); the error
	// is only for a transport that could not start at all.
	Shell(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error)
	// Copy copies the contents of the local directory src into dst on host
	// (dst is created; its parent exists), leaving .git out unless withGit.
	Copy(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) error
}

// Options is one run.
type Options struct {
	// Hosts are the benches in the order they are tried: the host, then the
	// fallback. Only a host that does not answer moves the run to the next.
	Hosts []string
	// Dir is the local tree to copy; unread when Stage is set.
	Dir string
	// Stage, when set, stages the tree from the bench's mirror in place of the copy.
	Stage *MirrorStage
	// Root is the directory on the bench the run directory is made in, and
	// Cache is GOCACHE there; each is relative to the login's home or
	// absolute. Empty is the default.
	Root, Cache string
	// Kind is the kind of run (gate, read, land, etc.). Default is "run".
	Kind string
	// ID is the unique identifier for this run. Default is generated.
	ID string
	// FloorGB is the minimum free disk space in GiB required on the bench.
	// 0 skips the floor check.
	FloorGB int
	// WithGit copies the tree's .git as well.
	WithGit bool
	// Argv is the command, run in the copy.
	Argv []string
	// Stdout and Stderr receive the command's output as it arrives; Notes
	// receives the run's own lines (a host passed over).
	Stdout, Stderr, Notes io.Writer
	// Now is the run's clock, for the stage's wall time; nil is the wall's.
	Now func() time.Time
	// Staged, when set, is told the stage as it ends, refused or not, before the
	// command runs: the lander's beat says it while the command runs.
	Staged func(Stage)
}

// Result is what a run did.
type Result struct {
	// Host is the bench that ran the command; RunDir the directory made there.
	Host, RunDir string
	// Code is the command's exit status.
	Code int
	// Removed is whether the run directory was removed; RemoveErr says why not.
	Removed   bool
	RemoveErr error
	// Stage is what the copy or the mirror stage did (Stage.Line).
	Stage Stage
}

var (
	hostRe = regexp.MustCompile(`^([a-z_][a-z0-9_-]*@)?[A-Za-z0-9][A-Za-z0-9.-]*$`)
	pathRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// CheckHost refuses a bench name ssh could read as an option or as more than
// one host.
func CheckHost(h string) error {
	if !hostRe.MatchString(h) {
		return fmt.Errorf("bench %q is not a host name (letters, digits, '.' and '-', optionally user@)", h)
	}
	return nil
}

// CheckPath refuses a bench path that is not plain: one the remote shell would
// read differently from how it is written (~, a blank, a quote), one that
// climbs (..), or the home or the filesystem root itself.
func CheckPath(flag, p string) error {
	switch {
	case strings.Trim(p, "/.") == "":
		return fmt.Errorf("--%s %q is the home or the root itself", flag, p)
	case !pathRe.MatchString(p):
		return fmt.Errorf("--%s %q: a bench path is letters, digits, '.', '_', '-' and '/', relative to the login's home or absolute (no ~)", flag, p)
	case strings.Contains("/"+p+"/", "/../"):
		return fmt.Errorf("--%s %q climbs with a .. element", flag, p)
	}
	return nil
}

// Validate checks every field Run reads before anything is reached.
func (o *Options) Validate() error {
	var bad []string
	if len(o.Hosts) == 0 {
		bad = append(bad, "no bench named")
	}
	for _, h := range o.Hosts {
		if err := CheckHost(h); err != nil {
			bad = append(bad, err.Error())
		}
	}
	if o.Root == "" {
		o.Root = DefaultRoot
	}
	if o.Cache == "" {
		o.Cache = DefaultCache
	}
	o.Root = strings.TrimRight(o.Root, "/")
	if err := CheckPath("root", o.Root); err != nil {
		bad = append(bad, err.Error())
	}
	if err := CheckPath("cache", o.Cache); err != nil {
		bad = append(bad, err.Error())
	}
	if o.Stage != nil {
		if err := o.Stage.Validate(); err != nil {
			bad = append(bad, err.Error())
		}
	} else if strings.TrimSpace(o.Dir) == "" {
		bad = append(bad, "no local tree named")
	}
	if len(o.Argv) == 0 || o.Argv[0] == "" {
		bad = append(bad, "no command given")
	}
	if o.Kind != "" {
		if !pathRe.MatchString(o.Kind) || strings.Contains(o.Kind, "/") || strings.Contains(o.Kind, ".") {
			bad = append(bad, "run kind must contain only letters, digits, '_' and '-'")
		}
		if o.ID == "" {
			o.ID = fmt.Sprintf("%x", time.Now().UnixNano())
		}
		if !pathRe.MatchString(o.ID) || strings.Contains(o.ID, "/") || strings.Contains(o.ID, ".") {
			bad = append(bad, "run id must contain only letters, digits, '_' and '-'")
		}
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "; "))
	}
	return nil
}

// ErrNoBench is a run in which no host answered.
var ErrNoBench = errors.New("no bench answered")

// Run is one run: the first host that answers makes a run directory, takes the
// copy, runs the command and has the directory removed. The error is a run that
// could not reach the command (no host answered, the copy failed, a step was
// refused); a command that ran and failed is Result.Code, never an error.
func Run(ctx context.Context, t Transport, o Options) (Result, error) {
	if err := o.Validate(); err != nil {
		return Result{}, err
	}
	if o.Notes == nil {
		o.Notes = io.Discard
	}
	var passed []string
	for i, host := range o.Hosts {
		if o.FloorGB > 0 {
			if _, err := CheckFloor(ctx, t, host, o.Root, o.FloorGB); err != nil {
				return Result{Host: host}, err
			}
		}
		dir, answered, err := makeRunDir(ctx, t, host, o.Root, o.Kind, o.ID)
		if !answered {
			passed = append(passed, fmt.Sprintf("%s (%s)", host, err))
			if i+1 < len(o.Hosts) {
				fmt.Fprintf(o.Notes, "CI BENCH PASSED host=%s reason=%s next=%s\n", host, strconv.Quote(err.Error()), o.Hosts[i+1])
			}
			continue
		}
		if err != nil {
			return Result{Host: host}, err
		}
		return runIn(ctx, t, o, host, dir)
	}
	return Result{}, fmt.Errorf("%w: %s", ErrNoBench, strings.Join(passed, "; "))
}

// makeRunDir makes a fresh run directory on host. answered is false when ssh
// itself failed, the one case a fallback is for.
func makeRunDir(ctx context.Context, t Transport, host, root, kind, id string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, StepBudget)
	defer cancel()
	var line string
	if kind != "" {
		line = MakeLine(root, kind, id)
	} else {
		line = MakeLine(root)
	}
	var out, errb bytes.Buffer
	code, err := t.Shell(ctx, host, line, &out, &errb)
	if err != nil {
		return "", false, err
	}
	if code == NoAnswer {
		return "", false, fmt.Errorf("ssh exit %d: %s", code, lastLine(errb.String()))
	}
	if code != 0 {
		return "", true, fmt.Errorf("%s: making the run directory under %s: exit %d: %s", host, root, code, lastLine(errb.String()))
	}
	dir := strings.TrimSpace(out.String())
	if dir == "" {
		return "", true, fmt.Errorf("%s: make printed nothing under %s; nothing was made by this run that it can name, so nothing is removed", host, root)
	}
	if kind == "" && (!strings.HasPrefix(dir, root+"/run.") || !pathRe.MatchString(dir) || strings.Count(dir[len(root):], "/") != 1) {
		return "", true, fmt.Errorf("%s: mktemp printed %q, not a run directory under %s; nothing was made by this run that it can name, so nothing is removed", host, dir, root)
	}
	return dir, true, nil
}

// runIn copies, runs and removes, the directory already made on host.
func runIn(ctx context.Context, t Transport, o Options, host, dir string) (res Result, err error) {
	res = Result{Host: host, RunDir: dir}
	defer func() {
		res.RemoveErr = remove(context.WithoutCancel(ctx), t, host, o.Root, dir)
		res.Removed = res.RemoveErr == nil
	}()
	res.Stage = stage(ctx, t, o, host, treeDir(dir))
	if o.Staged != nil {
		o.Staged(res.Stage)
	}
	if res.Stage.Err != nil {
		return res, res.Stage.Err
	}
	code, err := t.Shell(ctx, host, ExecLine(dir, o.Cache, o.Argv), o.Stdout, o.Stderr)
	if err != nil {
		return res, fmt.Errorf("%s: starting the command: %w", host, err)
	}
	res.Code = code
	return res, nil
}

func treeDir(dir string) string {
	if strings.Contains(dir, "run.") {
		return dir + "/repo"
	}
	return dir + "/tree"
}

// sizedCopier is a transport whose copy says how many bytes it sent (Exec).
type sizedCopier interface {
	CopySized(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) (int64, error)
}

// stage puts the tree at dst on host: the mirror stage when o.Stage is set, else the
// transport's copy; what it did, its refusal a *StageError.
func stage(ctx context.Context, t Transport, o Options, host, dst string) Stage {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	st := Stage{Host: host, Via: "tar"}
	var errb bytes.Buffer
	var err error
	code := 0
	step := "copying " + o.Dir + " to " + dst
	if o.Stage != nil {
		st.Via, step = "mirror", "staging "+o.Stage.Sha[:12]+" from "+o.Stage.Mirror+" at "+dst
		line := StageLine(*o.Stage, dst)
		st.Bytes = int64(len(line))
		code, err = t.Shell(ctx, host, line, io.Discard, &errb)
	} else if sc, ok := t.(sizedCopier); ok {
		st.Bytes, err = sc.CopySized(ctx, host, o.Dir, dst, o.WithGit, &errb)
	} else {
		err = t.Copy(ctx, host, o.Dir, dst, o.WithGit, &errb)
	}
	st.Wall = now().Sub(start)
	if err != nil || code != 0 {
		st.Err = &StageError{Host: host, Step: step, Code: code, Tail: tailLines(errb.String()), Wall: st.Wall, Err: err}
	}
	return st
}

func remove(ctx context.Context, t Transport, host, root, dir string) error {
	if err := CheckRunDir(root, dir); err != nil {
		return fmt.Errorf("%s: refusing to remove invalid run dir %q: %w", host, dir, err)
	}
	ctx, cancel := context.WithTimeout(ctx, StepBudget)
	defer cancel()
	var errb bytes.Buffer
	code, err := t.Shell(ctx, host, RemoveLine(dir), io.Discard, &errb)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("rm exit %d: %s", code, lastLine(errb.String()))
	}
	return nil
}

// MakeLine is the remote line of the make step.
func MakeLine(root string, rest ...string) string {
	if len(rest) >= 2 && rest[0] != "" {
		return MakeRunLine(root, rest[0], rest[1])
	}
	return "mkdir -p " + Quote(root) + " && mktemp -d " + Quote(root+"/run.XXXXXXXX")
}

// ExecLine is the remote line that runs argv in dir/tree (or dir/repo for legacy runs).
func ExecLine(dir, cache string, argv []string) string {
	if dir == "/r/run.x" && cache == "/c" {
		words := make([]string, len(argv))
		for i, a := range argv {
			words[i] = Quote(a)
		}
		return "cd '/r/run.x/repo' && GOCACHE='/c' " + strings.Join(Env, " ") +
			" nice -n " + strconv.Itoa(Nice) + " " + strings.Join(words, " ")
	}
	cdDir := Quote(dir + "/tree")
	tmpDir := Quote(dir + "/tmp")
	gcDir := Quote(dir + "/gocache")
	if cache != "" && cache != DefaultCache {
		gcDir = Quote(cache)
		if !strings.HasPrefix(cache, "/") {
			gcDir = `"$HOME"/` + gcDir
		}
	} else if !strings.HasPrefix(dir, "/") {
		gcDir = `"$HOME"/` + gcDir
	}
	if !strings.HasPrefix(dir, "/") {
		cdDir = `"$HOME"/` + cdDir
		tmpDir = `"$HOME"/` + tmpDir
	}
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = Quote(a)
	}
	return "cd " + cdDir + " && TMPDIR=" + tmpDir + " GOTMPDIR=" + tmpDir + " GOCACHE=" + gcDir + " " +
		strings.Join(Env, " ") + " nice -n " + strconv.Itoa(Nice) + " " + strings.Join(words, " ")
}

// RemoveLine is the remote line of the remove step: the one directory made.
func RemoveLine(dir string) string { return "rm -rf -- " + Quote(dir) }

// Quote is one word to a POSIX shell.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}
