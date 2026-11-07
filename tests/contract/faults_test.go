//go:build functional && !windows

package contract

// The fault contract: every row provokes one real fault in one tool and holds
// the tool to the same five rules (AGENTS.md, "Fault contract"):
//
//  1. a non-zero exit (a tool killed by a signal, or hung, is a failure);
//  2. exactly one stderr line, apart from the receipt lines the row declares;
//  3. that line holds the reason tokens the row names;
//  4. that line ends in a remedy, "run: <command>", and the remedy is pasteable:
//     it is run (where it has no <placeholder>) and must work, and for a fix the
//     row's own command is rerun and must get past the failure;
//  5. stdout is empty unless the row documents what it carries.
//
// A row that fails today carries Known, a short label for the gap. It still
// runs: it is skipped with the printed reason while it fails, and fails the
// build the day it passes, so the fix deletes its Known. Every table is in faults_<tool>_test.go.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// How the remedy in a row is judged.
const (
	Look = iota // runs and exits 0: it inspects the thing the message names, or shows the usage (the default)
	Fix         // runs and exits 0, and the row command then gets past this failure
	Runs        // can be run (not 126/127): the thing it inspects is what is missing
	None        // no remedy: the failing command is the caller's own (documented)
)

type Want struct {
	Exit   []int          // the exit codes allowed; empty means any non-zero
	Reason []string       // every one must appear in the line, case-folded; "a|b" accepts either
	Remedy int            // Fix, Look, Runs or None
	Signal int            // the tool is the command (it execs it), so the kernel reports the death: the signal wanted
	Stdout string         // what stdout must hold; empty means stdout must be empty
	Info   *regexp.Regexp // stderr lines that are receipts of work, not of the failure
	Lines  int            // failure lines wanted on stderr; 0 means exactly 1
}

type Fault struct {
	Tool  string
	Name  string
	Setup func(e *Env)
	Argv  []string // {name} is replaced by the Env variable of that name; {dir} is the row's directory
	Stdin string
	Want  Want
	Known string // a short label for the known gap; empty when the row passes
}

var tables = map[string][]Fault{}

func register(rows ...Fault) {
	for _, r := range rows {
		tables[r.Tool] = append(tables[r.Tool], r)
	}
}

var (
	binDir               string // nova-<tool> for every tool, and nothing else
	rootDir              string // short, for sockets
	pass, known, skipped atomic.Int64
)

func TestMain(m *testing.M) {
	var err error
	if rootDir, err = os.MkdirTemp("", "fc"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binDir = filepath.Join(rootDir, "bin")
	code := 1
	if err = buildTools(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	} else {
		code = m.Run()
	}
	stopPG()
	os.RemoveAll(rootDir)
	fmt.Printf("fault contract: %d rows pass, %d known gaps, %d skipped for want of a dependency\n", pass.Load(), known.Load(), skipped.Load())
	os.Exit(code)
}

func buildTools() error {
	os.MkdirAll(binDir, 0o755)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for _, tool := range []string{"bus", "cairn", "config", "fleet", "reserve", "sandbox", "secrets"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, "nova-"+tool), "./"+tool)
			cmd.Dir = "../.."
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("go build ./%s: %v: %s", tool, err, out)
			}
		}()
	}
	wg.Wait()
	close(errs)
	return <-errs
}

// Env is one row's world: its own directory, environment and processes.
type Env struct {
	t    *testing.T
	Dir  string
	vars map[string]string
	env  map[string]string
	path []string // when set, PATH is exactly these directories
	null bool     // stdout is /dev/null
}

func newEnv(t *testing.T) *Env {
	d := t.TempDir()
	e := &Env{t: t, Dir: d, vars: map[string]string{"dir": d}, env: map[string]string{}}
	return e
}

// skip ends a row for want of a dependency. It is counted, and under
// NOVA_CONTRACT_STRICT=1 (the CI functional job) it fails the row instead, so a
// runner without sops, age, redis or postgres cannot pass with fewer rows.
func (e *Env) skip(format string, args ...any) {
	e.t.Helper()
	if os.Getenv("NOVA_CONTRACT_STRICT") == "1" {
		e.t.Fatalf("NOVA_CONTRACT_STRICT=1 and a dependency is missing: "+format, args...)
	}
	skipped.Add(1)
	e.t.Skipf(format, args...)
}

func (e *Env) Var(k, v string) string { e.vars[k] = v; return v }
func (e *Env) Set(k, v string)        { e.env[k] = v }
func (e *Env) Path(dirs ...string)    { e.path = dirs }
func (e *Env) Unset(k string)         { delete(e.env, k) }
func (e *Env) DevNull()               { e.null = true }

func (e *Env) sub(s string) string {
	for k, v := range e.vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// Tools makes a directory holding only the named commands from the real PATH.
func (e *Env) Tools(names ...string) string {
	d, err := os.MkdirTemp(e.Dir, "tools")
	if err != nil {
		e.t.Fatal(err)
	}
	for _, n := range names {
		p, err := exec.LookPath(n)
		if err != nil {
			e.skip("%s is not on PATH", n)
		}
		os.Symlink(p, filepath.Join(d, n))
	}
	return d
}

// Script puts an executable shell script in the directory and returns the directory.
func (e *Env) Script(dir, name, body string) {
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

func (e *Env) Write(name, content string) string {
	p := filepath.Join(e.Dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// Chmod changes a mode for the row and restores it so the directory can be removed.
func (e *Env) Chmod(p string, mode os.FileMode) {
	if os.Geteuid() == 0 {
		e.skip("running as root, which a read-only mode does not stop")
	}
	if err := os.Chmod(p, mode); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { os.Chmod(p, 0o755) })
}

func freePort() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

// Dead is an address nothing listens on: a stopped service.
func (e *Env) Dead() string { return "127.0.0.1:" + freePort() }

// Frozen is an address that accepts connections and never answers.
func (e *Env) Frozen() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		e.t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	e.t.Cleanup(func() {
		l.Close()
		mu.Lock()
		for _, c := range held {
			c.Close()
		}
		mu.Unlock()
	})
	return l.Addr().String()
}

// Redis starts a throwaway redis-server with the given extra arguments.
func (e *Env) Redis(args ...string) string {
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		e.skip("redis-server is not on PATH")
	}
	port := freePort()
	cmd := exec.Command(bin, append([]string{"--port", port, "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", e.Dir}, args...)...)
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	addr := "127.0.0.1:" + port
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("redis-server did not listen on %s", addr)
	return ""
}

func (e *Env) environ(sys bool) []string {
	p := append([]string{binDir}, filepath.SplitList(os.Getenv("PATH"))...)
	if e.path != nil && !sys {
		p = e.path
	}
	out := []string{"PATH=" + strings.Join(p, string(os.PathListSeparator)), "HOME=" + e.Dir, "TMPDIR=" + e.Dir, "LANG=C"}
	keys := make([]string, 0, len(e.env))
	for k := range e.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+e.sub(e.env[k]))
	}
	return out
}

type outcome struct {
	code      int // -1: killed by a signal, -2: killed at the time limit
	sig       int // the signal, when code is -1
	out, errs string
}

func (e *Env) run(cmd *exec.Cmd, stdin string, null bool) outcome {
	var so, se bytes.Buffer
	cmd.Dir = e.Dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = &so, &se
	if null {
		cmd.Stdout = nil
	}
	cmd.WaitDelay = time.Second
	start := time.Now()
	err := cmd.Run()
	o := outcome{out: so.String(), errs: se.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		o.code = ee.ExitCode()
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			o.sig = int(ws.Signal())
		}
	default:
		o.code = 127
	}
	if o.code == -1 && time.Since(start) >= 19*time.Second {
		o.code = -2 // the row's context killed it
	}
	return o
}

// Exec runs nova-<tool> as the row would, for a Setup that must leave the world in a state.
func (e *Env) Exec(tool, stdin string, args ...string) outcome {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := range args {
		args[i] = e.sub(args[i])
	}
	return e.run(e.command(ctx, tool, args, false), stdin, false)
}

func (e *Env) command(ctx context.Context, tool string, args []string, sys bool) *exec.Cmd {
	cmd := exec.CommandContext(ctx, filepath.Join(binDir, "nova-"+tool), args...)
	cmd.Env = e.environ(sys)
	return cmd
}

var remedyRe = regexp.MustCompile(`.*\brun: (\S.*)$`)

var pasteable = map[string]bool{"git": true, "psql": true, "ssh": true, "ls": true, "file": true, "iconv": true, "mkdir": true, "chmod": true,
	"age-keygen": true, "sops": true, "cd": true, "sudo": true}

func TestFaults(t *testing.T) {
	tools := make([]string, 0, len(tables))
	for tool := range tables {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			for _, f := range tables[tool] {
				t.Run(f.Name, func(t *testing.T) {
					t.Parallel()
					runFault(t, f)
				})
			}
		})
	}
}

func runFault(t *testing.T, f Fault) {
	e := newEnv(t)
	if f.Setup != nil {
		f.Setup(e)
	}
	argv := make([]string, len(f.Argv))
	for i, a := range f.Argv {
		argv[i] = e.sub(a)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first := e.run(e.command(ctx, f.Tool, argv, false), f.Stdin, e.null)
	problems := judge(t, e, f, argv, first)
	if os.Getenv("FAULTS_SHOW") != "" {
		t.Logf("exit %d stdout %q stderr %q", first.code, first.out, first.errs)
	}
	switch {
	case len(problems) > 0 && f.Known != "":
		known.Add(1)
		t.Skipf("known gap %s: %s", f.Known, strings.Join(problems, "; "))
	case len(problems) > 0:
		for _, p := range problems {
			t.Errorf("%s %s %s: %s\n  exit %d, stdout %q, stderr %q", f.Tool, f.Name, strings.Join(argv, " "), p, first.code, first.out, first.errs)
		}
	case f.Known != "":
		t.Errorf("%s %s now meets the contract: delete its Known %q", f.Tool, f.Name, f.Known)
	default:
		pass.Add(1)
	}
}

func judge(t *testing.T, e *Env, f Fault, argv []string, o outcome) []string {
	w := f.Want
	var p []string
	switch {
	case o.code == -2:
		return []string{"the tool hung and was killed at the 20 s limit"}
	case o.code == -1 && w.Signal != 0 && o.sig == w.Signal && o.out == "":
		return nil
	case o.code == -1:
		return []string{fmt.Sprintf("the tool itself died by signal %d, want %d", o.sig, w.Signal)}
	case o.code == 0:
		p = append(p, "exit 0, want a refusal")
	case len(w.Exit) > 0 && !contains(w.Exit, o.code):
		p = append(p, fmt.Sprintf("exit %d, want one of %v", o.code, w.Exit))
	}
	if w.Stdout == "" && o.out != "" {
		p = append(p, "stdout is not empty")
	} else if !strings.Contains(o.out, w.Stdout) {
		p = append(p, fmt.Sprintf("stdout lacks %q", w.Stdout))
	}
	var lines []string
	for _, l := range strings.Split(o.errs, "\n") {
		if strings.TrimSpace(l) != "" && (w.Info == nil || !w.Info.MatchString(l)) {
			lines = append(lines, l)
		}
	}
	want := max(w.Lines, 1)
	if len(lines) != want {
		return append(p, fmt.Sprintf("%d failure lines on stderr, want %d", len(lines), want))
	}
	line := strings.Join(lines, "\n")
	for _, tok := range w.Reason {
		ok := false
		for _, alt := range strings.Split(tok, "|") {
			ok = ok || strings.Contains(strings.ToLower(line), strings.ToLower(alt))
		}
		if !ok {
			p = append(p, fmt.Sprintf("no reason token %q", tok))
		}
	}
	if w.Remedy == None {
		return p
	}
	m := remedyRe.FindStringSubmatch(lines[len(lines)-1])
	if m == nil {
		return append(p, "no remedy: the line has no \"run: <command>\"")
	}
	return append(p, remedy(e, f, argv, o, m[1])...)
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// remedy proves the command pasteable: its binary exists, and where it has no
// placeholder it runs.
func remedy(e *Env, f Fault, argv []string, first outcome, cmdline string) []string {
	word := strings.Fields(cmdline)[0]
	if !strings.HasPrefix(word, "nova-") && !pasteable[word] {
		return []string{fmt.Sprintf("remedy %q starts with %q, which is not a tool of this repository or a standard command", cmdline, word)}
	}
	if regexp.MustCompile(`<[^>]+>`).MatchString(cmdline) {
		return nil // a placeholder: the usage names it, there is nothing to run
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdline)
	cmd.Env = e.environ(true)
	r := e.run(cmd, "", false)
	if r.code == 126 || r.code == 127 || r.code < 0 {
		return []string{fmt.Sprintf("remedy %q cannot run (exit %d): %s", cmdline, r.code, strings.TrimSpace(r.errs))}
	}
	if f.Want.Remedy == Runs {
		return nil
	}
	if r.code != 0 {
		return []string{fmt.Sprintf("remedy %q exits %d: %s", cmdline, r.code, strings.TrimSpace(r.errs))}
	}
	if f.Want.Remedy == Fix {
		again := e.run(e.command(ctx, f.Tool, argv, false), f.Stdin, e.null)
		if again.code == first.code && again.errs == first.errs {
			return []string{fmt.Sprintf("after remedy %q the command fails the same way", cmdline)}
		}
	}
	return nil
}
