package darwincheck

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests drive the check's driver against a System whose processes answer
// from a script: what it runs, in what environment, and what counts as a pass.
// Whether the wall really denies what the check says it denies is measured on a
// darwin machine, by tools/sandboxcheck and by the tool's own test.

const fakeTemplate = ";; template\n@@OPTROOTS@@\n@@ANCESTORS@@\n@@READS@@\n@@WRITES@@\n@@NET@@\n"

// fakeSystem is the machine under test.
type fakeSystem struct {
	fakeFS
	mu      sync.Mutex
	goos    string
	environ []string
	noGit   bool
	// answer is the reply for a process; nil means the default wall below.
	answer  func(s Spec) (Result, bool)
	ran     []Spec
	started []Spec
	stopped int
	// stopErr is what every listener's Stop returns.
	stopErr error
	sockets map[string]bool
	sleeps  int
}

func newFake() *fakeSystem {
	return &fakeSystem{
		goos:    "darwin",
		environ: []string{"PATH=/usr/bin:/bin", "HOME=/Users/x"},
		sockets: map[string]bool{},
	}
}

func (f *fakeSystem) GOOS() string      { return f.goos }
func (f *fakeSystem) Environ() []string { return f.environ }
func (f *fakeSystem) LookPath(name string) (string, bool) {
	if f.noGit && name == "git" {
		return "", false
	}
	return "/usr/bin/" + name, true
}
func (f *fakeSystem) IsSocket(p string) bool { return f.sockets[p] }
func (f *fakeSystem) Sleep(time.Duration)    { f.mu.Lock(); f.sleeps++; f.mu.Unlock() }

type fakeProc struct{ f *fakeSystem }

func (p fakeProc) Stop() error {
	p.f.mu.Lock()
	defer p.f.mu.Unlock()
	p.f.stopped++
	return p.f.stopErr
}

func (f *fakeSystem) Start(s Spec) (Process, error) {
	f.mu.Lock()
	f.started = append(f.started, s)
	f.mu.Unlock()
	// The listener binds: the socket appears where it was asked to.
	f.sockets[filepath.Join(s.Dir, strings.TrimPrefix(s.Args[len(s.Args)-1], "./"))] = true
	return fakeProc{f}, nil
}

// defaultWall is a wall that behaves as the profile is meant to: what the
// denials name fail, everything else passes, and DNS resolves only with the
// mDNSResponder literal in the profile.
func (f *fakeSystem) defaultWall(s Spec) Result {
	if s.Name != "/usr/bin/sandbox-exec" {
		return Result{}
	}
	profile, command := s.Args[1], s.Args[len(s.Args)-1]
	switch {
	case strings.HasPrefix(command, "curl "):
		if strings.Contains(profile, mdnsSocket) {
			return Result{Stdout: "200"}
		}
		return Result{Stdout: "000", Code: 6}
	case strings.HasPrefix(command, ": > "), strings.HasPrefix(command, "cat '") && strings.Contains(command, "/secret/"),
		strings.HasPrefix(command, "ls '"), strings.HasPrefix(command, "nc -U ../secret"),
		command == "pbpaste > /dev/null", strings.HasPrefix(command, "/usr/bin/sandbox-exec -p"):
		return Result{Stderr: "Operation not permitted\n", Code: 1}
	case command == "echo nova-pipe":
		return Result{Stdout: "nova-pipe\n"}
	}
	return Result{}
}

func (f *fakeSystem) Run(s Spec) Result {
	f.mu.Lock()
	f.ran = append(f.ran, s)
	f.mu.Unlock()
	if f.answer != nil {
		if r, ok := f.answer(s); ok {
			return r
		}
	}
	return f.defaultWall(s)
}

// walledRuns are the processes started inside the wall.
func (f *fakeSystem) walledRuns() []Spec {
	var out []Spec
	for _, s := range f.ran {
		if s.Name == "/usr/bin/sandbox-exec" {
			out = append(out, s)
		}
	}
	return out
}

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return d
}

// childRe is the check's own scratch child inside the directory the tests hand in
// as Scratch (named "check"): an os.MkdirTemp directory named for the process.
const childRe = `/check/\.darwin-check-scratch\.[0-9]+\.[^/]+`

// run is the check with its scratch child made inside a temp directory.
func run(t *testing.T, f *fakeSystem, mod func(*Options)) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	o := Options{Template: fakeTemplate, Scratch: filepath.Join(realDir(t), "check"), NoNetwork: true, Stdout: &out, Stderr: &errb}
	if mod != nil {
		mod(&o)
	}
	return Run(o, f), out.String(), errb.String()
}

// suiteNames are the checks a full run with the network prints, in order.
var suiteNames = []string{
	"cd_absolute", "mkdir_p_absolute", "git_init_absolute", "git_clone_shared", "home_config_write",
	"cat_etc_hosts", "sh_c_true", "child_kill", "stdout_to_file", "cxx_compile", "cxx_compile_control",
	"stdout_to_pipe", "write_outside", "write_outside_control", "read_secret", "read_secret_control",
	"list_ancestor", "list_ancestor_control", "unix_socket_outside", "unix_socket_outside_control",
	"unix_socket_inside", "dns_resolves", "dns_resolves_control", "clipboard_denied",
	"nested_sandbox_refused", "env_no_ssh_auth_sock",
}

func checkLines(out string) (ok, fail, skip []string) {
	for _, l := range strings.Split(out, "\n") {
		_, rest, found := strings.Cut(l, " name=")
		if !found {
			continue
		}
		name, _, _ := strings.Cut(rest, " ")
		switch {
		case strings.HasPrefix(l, "CHECK OK "):
			ok = append(ok, name)
		case strings.HasPrefix(l, "CHECK FAIL "):
			fail = append(fail, name)
		case strings.HasPrefix(l, "CHECK SKIP "):
			skip = append(skip, name)
		}
	}
	return
}

func TestAWallThatBehavesPassesEveryCheckInOrder(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, out, _ := run(t, f, func(o *Options) { o.NoNetwork = false })
	ok, fail, skip := checkLines(out)
	require.Equal(t, 0, code, "fail %v skip %v:\n%s", fail, skip, out)
	assert.Empty(t, fail)
	assert.Empty(t, skip)
	assert.Equal(t, suiteNames, ok)
}

func TestNoNetworkSkipsTheTwoDNSChecksAndTouchesNoNetwork(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, out, _ := run(t, f, nil)
	ok, fail, skip := checkLines(out)
	require.Equal(t, 0, code, out)
	assert.Empty(t, fail)
	assert.Equal(t, []string{"dns_resolves", "dns_resolves_control"}, skip)
	assert.Len(t, ok, len(suiteNames)-2)
	assert.Contains(t, out, "CHECK SKIP name=dns_resolves reason=no_network\n", "the skip line is what callers grep")
	for _, s := range f.walledRuns() {
		assert.NotContains(t, s.Args[len(s.Args)-1], "curl", "curl ran with the network off: %v", s.Args)
	}
}

func TestAWallThatLetsEverythingThroughFailsEveryDenial(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.answer = func(s Spec) (Result, bool) {
		if s.Name == "/usr/bin/sandbox-exec" {
			return Result{Stdout: "nova-pipe\n"}, true
		}
		return Result{}, true
	}
	code, out, _ := run(t, f, nil)
	_, fail, _ := checkLines(out)
	want := []string{"write_outside", "read_secret", "list_ancestor", "unix_socket_outside", "clipboard_denied", "nested_sandbox_refused"}
	assert.Equal(t, 1, code, out)
	assert.Equal(t, want, fail, out)
	assert.Contains(t, out, "CHECK FAIL name=write_outside SUCCEEDED inside the wall\n", "the denial's failure line is what callers grep")
}

func TestAWallThatDeniesEverythingFailsTheFirstSecond(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.answer = func(s Spec) (Result, bool) {
		if s.Name == "/usr/bin/sandbox-exec" {
			return Result{Stderr: "Operation not permitted\nsecond line\n", Code: 1}, true
		}
		return Result{}, true
	}
	code, out, _ := run(t, f, nil)
	_, fail, _ := checkLines(out)
	for _, n := range []string{"cd_absolute", "git_clone_shared", "cxx_compile", "stdout_to_pipe", "unix_socket_inside", "env_no_ssh_auth_sock"} {
		assert.Contains(t, fail, n, "%s did not fail under a wall that denies everything", n)
	}
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "CHECK FAIL name=cd_absolute rc=1 out=Operation not permitted\n", "an expectOK failure names the rc and the first line of output")
}

// A denial with no control that passes outside the wall proves nothing, so a
// control that fails is a FAIL of its own.
func TestAControlThatFailsOutsideTheWallIsAFail(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.answer = func(s Spec) (Result, bool) {
		if s.Name == "/bin/cat" {
			return Result{Code: 1}, true
		}
		return Result{}, false
	}
	code, out, _ := run(t, f, nil)
	_, fail, _ := checkLines(out)
	assert.Equal(t, 1, code, out)
	assert.Equal(t, []string{"read_secret_control"}, fail, out)
	assert.Contains(t, out, "CHECK FAIL name=read_secret_control rc=1 (outside the wall)\n")
}

func TestEveryDenialHasItsControlRunOutsideTheWall(t *testing.T) {
	t.Parallel()
	f := newFake()
	_, out, _ := run(t, f, nil)
	ok, _, _ := checkLines(out)
	have := map[string]bool{}
	for _, n := range ok {
		have[n] = true
	}
	for _, denial := range []string{"write_outside", "read_secret", "list_ancestor", "unix_socket_outside"} {
		assert.True(t, have[denial], "%s did not pass", denial)
		assert.True(t, have[denial+"_control"], "%s has no passing control %s_control", denial, denial)
	}
	// The controls run the named programs, outside: never through sandbox-exec.
	controls := map[string]bool{}
	for _, s := range f.ran {
		if s.Name != "/usr/bin/sandbox-exec" {
			controls[s.Name] = true
		}
	}
	for _, p := range []string{"/usr/bin/touch", "/bin/cat", "/bin/ls", "/usr/bin/c++"} {
		assert.True(t, controls[p], "the control %s did not run outside the wall", p)
	}
}

func TestEveryWalledCommandRunsFromTheWriteSetInAClosedEnvironment(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.environ = append(f.environ, "SSH_AUTH_SOCK=/s", "AWS_SECRET=leaky")
	run(t, f, nil)
	walled := f.walledRuns()
	require.NotEmpty(t, walled, "no walled run")
	for _, s := range walled {
		assert.Regexp(t, childRe+"/w$", s.Dir, "walled run not from the write set")
		env := strings.Join(s.Env, "\n")
		assert.NotContains(t, env, "AWS_SECRET", "the walled environment is not closed and scrubbed")
		assert.NotContains(t, env, "SSH_AUTH_SOCK", "the walled environment is not closed and scrubbed")
		assert.NotContains(t, env, "GPG_AGENT_INFO", "the walled environment is not closed and scrubbed")
		for _, want := range []string{"HOME=" + s.Dir + "/home", "PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + s.Dir + "/.nova-sandbox-tmp", "TMP=", "TEMP=", "NOVA_KEEP=kept"} {
			assert.Contains(t, env, want)
		}
		// The profile is inline, never a file; shell runs the command.
		require.GreaterOrEqual(t, len(s.Args), 4, "walled argv: %v", s.Args)
		assert.Equal(t, "-p", s.Args[0], "walled argv: %v", s.Args)
		assert.Equal(t, []string{"--", "/bin/sh", "-c"}, s.Args[len(s.Args)-4:len(s.Args)-1], "walled argv: %v", s.Args)
		for _, d := range []string{"READ0=", "WRITE0=", "HOME="} {
			found := false
			for i, a := range s.Args {
				found = found || (a == "-D" && strings.HasPrefix(s.Args[i+1], d))
			}
			assert.True(t, found, "walled argv lacks -D %s: %v", d, s.Args)
		}
		assert.False(t, strings.HasSuffix(s.Args[1], "\n"), "the inline profile keeps its trailing newline")
	}
}

func TestTheReferenceRepositoryIsSeededByGitBeforeAnyCheck(t *testing.T) {
	t.Parallel()
	f := newFake()
	run(t, f, nil)
	var git [][]string
	for _, s := range f.ran {
		if s.Name == "/usr/bin/git" {
			git = append(git, s.Args)
		}
	}
	require.Len(t, git, 3, "git ran %d times, want init, add, commit: %v", len(git), git)
	joined := func(a []string) string { return strings.Join(a, " ") }
	assert.Regexp(t, `^-c init\.defaultBranch=main init -q .*`+childRe+`/ref$`, joined(git[0]))
	for i, verb := range []string{" add -A", " commit -q -m seed"} {
		a := joined(git[i+1])
		assert.Contains(t, a, "-c user.name=check -c user.email=check@example.com -c commit.gpgsign=false", "git step %d", i+1)
		assert.True(t, strings.HasSuffix(a, verb), "git step %d: %s", i+1, a)
	}
	assert.Equal(t, "/usr/bin/git", f.ran[0].Name, "a walled run came before the repository was seeded")
}

func TestAFailedGitSeedIsAFailNotASilentExit(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.answer = func(s Spec) (Result, bool) {
		if s.Name == "/usr/bin/git" {
			return Result{Stderr: "fatal: nope\n", Code: 128}, true
		}
		return Result{}, false
	}
	code, out, _ := run(t, f, nil)
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "CHECK FAIL name=reference_repo")
	assert.Empty(t, f.walledRuns())
}

func TestNotDarwinIsOneFailLineAndNothingRuns(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.goos = "linux"
	code, out, _ := run(t, f, nil)
	assert.Equal(t, 1, code)
	assert.Equal(t, "CHECK FAIL name=platform (this check is darwin only)\n", out)
	assert.Empty(t, f.ran, "processes ran on a platform with no wall")
	assert.Empty(t, f.started, "processes ran on a platform with no wall")
}

func TestNoTemplateAndNoGitAreNamedFails(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, out, _ := run(t, f, func(o *Options) { o.Template = "" })
	assert.Equal(t, 1, code)
	assert.True(t, strings.HasPrefix(out, "CHECK FAIL name=template_present"), out)
	g := newFake()
	g.noGit = true
	code, out, _ = run(t, g, nil)
	assert.Equal(t, 1, code)
	assert.True(t, strings.HasPrefix(out, "CHECK FAIL name=git_present"), out)
}

func TestDumpProfilePrintsTheFilledProfileAndRunsNoCheck(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, out, _ := run(t, f, func(o *Options) { o.DumpProfile = true })
	require.Equal(t, 0, code, out)
	assert.NotContains(t, out, "CHECK ", "the dump carries check lines")
	assert.NotContains(t, out, "@@", "the dump carries an unfilled marker")
	for _, want := range []string{`(allow file-read* (subpath (param "READ0")))`, `(literal "/private/var/run/mDNSResponder")`, `(allow file-read-metadata (literal "`} {
		assert.Contains(t, out, want)
	}
	assert.Empty(t, f.walledRuns(), "a dump ran walled commands")
	// The ancestors name the real write set, which is what makes the two fill modes comparable.
	require.NotEmpty(t, f.ran)
	assert.Contains(t, out, `(literal "`+filepath.Dir(f.ran[0].Args[len(f.ran[0].Args)-1])+`")`, "the dump does not grant the scratch directory's metadata")
}

func TestFillUsesTheToolsProfileInPlaceOfTheHandFilledOne(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.answer = func(s Spec) (Result, bool) {
		if s.Name == "/tmp/nova-sandbox" {
			return Result{Stdout: "(version 1)\n;; the tool's own\n(literal \"" + mdnsSocket + "\")\n", Stderr: "POLICY OK\n"}, true
		}
		return Result{}, false
	}
	code, out, errOut := run(t, f, func(o *Options) { o.Fill = "/tmp/nova-sandbox" })
	require.Equal(t, 0, code, out)
	assert.Contains(t, errOut, "POLICY OK", "the generator's stderr was swallowed")
	var gen Spec
	for _, s := range f.ran {
		if s.Name == "/tmp/nova-sandbox" {
			gen = s
		}
	}
	require.Len(t, gen.Args, 5, "the generator was run as %v", gen.Args)
	assert.Equal(t, "policy", gen.Args[0])
	assert.Equal(t, "--read", gen.Args[1])
	assert.Equal(t, "--write", gen.Args[3])
	assert.Regexp(t, childRe+"/ref$", gen.Args[2])
	assert.Regexp(t, childRe+"/w$", gen.Args[4])
	assert.True(t, strings.HasPrefix(lastEnv(gen.Env, "HOME"), gen.Args[4]), "the generator ran with HOME %q, want one inside the write set", lastEnv(gen.Env, "HOME"))
	for _, s := range f.walledRuns() {
		assert.Contains(t, s.Args[1], ";; the tool's own", "a walled run used the hand-filled profile")
		assert.NotContains(t, s.Args[1], ";; template", "a walled run used the hand-filled profile")
	}
}

func lastEnv(environ []string, key string) string {
	v := ""
	for _, e := range environ {
		if k, val, _ := strings.Cut(e, "="); k == key {
			v = val
		}
	}
	return v
}

func TestAGeneratorThatRefusesIsANamedFail(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.answer = func(s Spec) (Result, bool) {
		if s.Name == "/tmp/nova-sandbox" {
			return Result{Stderr: "POLICY REFUSED\n", Code: 2}, true
		}
		return Result{}, false
	}
	code, out, _ := run(t, f, func(o *Options) { o.Fill = "/tmp/nova-sandbox" })
	assert.Equal(t, 1, code)
	assert.Equal(t, "CHECK FAIL name=tool_generated_profile (/tmp/nova-sandbox policy refused)\n", out)
	assert.Empty(t, f.walledRuns(), "checks ran against a profile that was not generated")
}

func TestTheDNSChecksAndTheirControl(t *testing.T) {
	t.Parallel()
	net := func(o *Options) { o.NoNetwork = false }
	t.Run("the control runs the same profile without the resolver socket", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		run(t, f, net)
		var withSock, without []Spec
		for _, s := range f.walledRuns() {
			if !strings.HasPrefix(s.Args[len(s.Args)-1], "curl ") {
				continue
			}
			if strings.Contains(s.Args[1], mdnsSocket) {
				withSock = append(withSock, s)
			} else {
				without = append(without, s)
			}
		}
		require.Len(t, withSock, 1, "curl with the socket")
		require.Len(t, without, 1, "curl without the socket")
		assert.Equal(t, without[0].Args[1], strings.Replace(withSock[0].Args[1], ` (literal "`+mdnsSocket+`")`, "", 1), "the control profile differs from the profile under test by more than the socket literal")
		env := strings.Join(without[0].Env, " ")
		assert.NotContains(t, env, "NOVA_KEEP", "the control's environment is not the bare one")
		assert.NotContains(t, env, "TMP=", "the control's environment is not the bare one")
	})
	t.Run("a name that does not resolve fails dns_resolves", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.answer = func(s Spec) (Result, bool) {
			if s.Name == "/usr/bin/sandbox-exec" && strings.HasPrefix(s.Args[len(s.Args)-1], "curl ") {
				return Result{Stdout: "000"}, true
			}
			return Result{}, false
		}
		code, out, _ := run(t, f, net)
		_, fail, _ := checkLines(out)
		assert.Equal(t, 1, code, out)
		assert.Equal(t, []string{"dns_resolves"}, fail, out)
		assert.Contains(t, out, "CHECK FAIL name=dns_resolves http_code=000 (the name did not resolve)\n")
	})
	t.Run("an empty answer also fails it", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.answer = func(s Spec) (Result, bool) {
			if s.Name == "/usr/bin/sandbox-exec" && strings.HasPrefix(s.Args[len(s.Args)-1], "curl ") && strings.Contains(s.Args[1], mdnsSocket) {
				return Result{}, true
			}
			return Result{}, false
		}
		_, out, _ := run(t, f, net)
		assert.Contains(t, out, "CHECK FAIL name=dns_resolves http_code= (the name did not resolve)\n")
	})
	t.Run("a control that resolves without the socket fails", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.answer = func(s Spec) (Result, bool) {
			if s.Name == "/usr/bin/sandbox-exec" && strings.HasPrefix(s.Args[len(s.Args)-1], "curl ") {
				return Result{Stdout: "404"}, true
			}
			return Result{}, false
		}
		code, out, _ := run(t, f, net)
		_, fail, _ := checkLines(out)
		assert.Equal(t, 1, code, out)
		assert.Equal(t, []string{"dns_resolves_control"}, fail, out)
		assert.Contains(t, out, "http_code=404 WITHOUT the socket: DNS is reaching the resolver some other way")
	})
	t.Run("any status other than 000 is a resolved name", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.answer = func(s Spec) (Result, bool) {
			if s.Name == "/usr/bin/sandbox-exec" && strings.HasPrefix(s.Args[len(s.Args)-1], "curl ") && strings.Contains(s.Args[1], mdnsSocket) {
				return Result{Stdout: "404"}, true
			}
			return Result{}, false
		}
		code, out, _ := run(t, f, net)
		assert.Equal(t, 0, code, "a 404 is a name that resolved:\n%s", out)
	})
}

// The socket denial needs its listener: with none bound the check fails by
// name and the control is not reported, rather than passing for the wrong reason.
func TestNoListenerIsAFailNotAPass(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, out, _ := runWithSockets(t, f, false)
	ok, fail, _ := checkLines(out)
	assert.Equal(t, 1, code, out)
	assert.Equal(t, []string{"unix_socket_outside"}, fail, out)
	assert.NotContains(t, ok, "unix_socket_outside_control", "reported with no listener")
	assert.NotContains(t, ok, "unix_socket_outside", "reported with no listener")
	assert.Contains(t, out, "no listener: nc -lU did not bind (sun_path is 104 bytes)")
	// Two bounded waits (the outside pair, then the job's own), ten polls each,
	// on a clock that does not wait.
	assert.Equal(t, 2*socketPolls, f.sleeps, "the wait is bounded")
}

// runWithSockets runs the suite with listeners that do (true) or do not (false) bind.
func runWithSockets(t *testing.T, f *fakeSystem, bind bool) (int, string, string) {
	t.Helper()
	sys := &noBind{fakeSystem: f, bind: bind}
	var out, errb bytes.Buffer
	o := Options{Template: fakeTemplate, Scratch: filepath.Join(realDir(t), "check"), NoNetwork: true, Stdout: &out, Stderr: &errb}
	return Run(o, sys), out.String(), errb.String()
}

type noBind struct {
	*fakeSystem
	bind bool
}

func (n *noBind) Start(s Spec) (Process, error) {
	if n.bind {
		return n.fakeSystem.Start(s)
	}
	return fakeProc{n.fakeSystem}, nil
}

func TestTheSocketsAreListenedOnByRelativePath(t *testing.T) {
	t.Parallel()
	f := newFake()
	run(t, f, nil)
	require.Len(t, f.started, 3, "want the two outside and the job's own")
	for _, s := range f.started {
		assert.Equal(t, "/usr/bin/nc", s.Name)
		require.Len(t, s.Args, 2, "listener %v", s)
		assert.Equal(t, "-lU", s.Args[0])
		assert.True(t, strings.HasPrefix(s.Args[1], "./"), "the socket path must be relative (sun_path is 104 bytes): %v", s)
		assert.NotContains(t, s.Args[1][2:], "/", "the socket path must be relative (sun_path is 104 bytes): %v", s)
	}
	assert.True(t, strings.HasSuffix(f.started[0].Dir, "/secret"), f.started[0].Dir)
	assert.Regexp(t, childRe+"/w$", f.started[2].Dir)
	assert.Equal(t, 3, f.stopped, "every listener is stopped")
}

// Only what the check made is removed. A scratch directory a caller handed in
// keeps everything the caller put there, including a file or a directory named
// for one of the check's own (w, ref, secret, outside); the check works in its
// own child of it and removes that child. A directory the check created is
// removed whole.
func TestCleanupRemovesOnlyWhatTheCheckMade(t *testing.T) {
	t.Parallel()
	t.Run("a scratch directory that existed keeps its own contents and nothing of the run is left", func(t *testing.T) {
		t.Parallel()
		scratch := filepath.Join(realDir(t), "mine")
		require.NoError(t, os.MkdirAll(filepath.Join(scratch, "w"), 0o755))
		keep := filepath.Join(scratch, "keep.txt")
		mine := filepath.Join(scratch, "w", "mine.txt")
		require.NoError(t, os.WriteFile(keep, []byte("x"), 0o644))
		require.NoError(t, os.WriteFile(mine, []byte("y"), 0o644))
		f := newFake()
		code, out, errOut := run(t, f, func(o *Options) { o.Scratch = scratch })
		require.Equal(t, 0, code, "%s%s", out, errOut)
		assert.FileExists(t, keep, "the caller's file was removed")
		assert.FileExists(t, mine, "the caller's file in a directory named like the check's own was removed")
		var names []string
		entries, err := os.ReadDir(scratch)
		require.NoError(t, err)
		for _, e := range entries {
			names = append(names, e.Name())
		}
		assert.ElementsMatch(t, []string{"keep.txt", "w"}, names, "the run's files are gone and nothing else is left")
	})
	t.Run("a scratch directory the check made is removed", func(t *testing.T) {
		t.Parallel()
		scratch := filepath.Join(realDir(t), "new")
		f := newFake()
		run(t, f, func(o *Options) { o.Scratch = scratch })
		assert.NoDirExists(t, scratch, "the scratch directory the check created is still there")
	})
	t.Run("a read-only tree in the write set is removed too", func(t *testing.T) {
		t.Parallel()
		scratch := filepath.Join(realDir(t), "new")
		f := newFake()
		var made bool
		f.answer = func(s Spec) (Result, bool) {
			if s.Name == "/usr/bin/git" && !made {
				made = true
				// git's objects are read-only, and so is their directory. The first
				// git call is the init, whose last argument is the reference repo
				// beside the write set.
				d := filepath.Join(filepath.Dir(s.Args[len(s.Args)-1]), "w", "g", "objects")
				assert.NoError(t, os.MkdirAll(d, 0o755))
				assert.NoError(t, os.WriteFile(filepath.Join(d, "obj"), []byte("x"), 0o444))
				assert.NoError(t, os.Chmod(d, 0o555))
			}
			return Result{}, false
		}
		code, out, errOut := run(t, f, func(o *Options) { o.Scratch = scratch })
		assert.Equal(t, 0, code, "%s%s", out, errOut)
		assert.NoDirExists(t, scratch, "a read-only tree kept the scratch directory alive")
	})
	t.Run("the default scratch child is made in the base directory and named for the process", func(t *testing.T) {
		t.Parallel()
		base := realDir(t)
		var seen string
		f := newFake()
		f.answer = func(s Spec) (Result, bool) {
			if s.Name == "/usr/bin/git" && seen == "" {
				seen = s.Args[len(s.Args)-1]
			}
			return Result{}, false
		}
		run(t, f, func(o *Options) { o.Scratch = ""; o.BaseDir = base })
		assert.Regexp(t, "^"+regexp.QuoteMeta(base)+`/\.darwin-check-scratch\.`+strconv.Itoa(os.Getpid())+`\.[^/]+/ref$`, seen, "the reference repository")
		entries, err := os.ReadDir(base)
		require.NoError(t, err)
		assert.Empty(t, entries, "the base directory kept %v", entries)
	})
	t.Run("a dump cleans up too", func(t *testing.T) {
		t.Parallel()
		scratch := filepath.Join(realDir(t), "new")
		run(t, newFake(), func(o *Options) { o.Scratch = scratch; o.DumpProfile = true })
		assert.NoDirExists(t, scratch, "a dump left its scratch tree")
	})
}

// A cleanup that cannot remove everything says so, on standard error and in the
// exit code, and never removes what it did not expect.
func TestACleanupThatCannotRemoveSaysSo(t *testing.T) {
	t.Parallel()
	t.Run("a file the check did not make is reported and left", func(t *testing.T) {
		t.Parallel()
		scratch := filepath.Join(realDir(t), "new")
		f := newFake()
		var stray string
		f.answer = func(s Spec) (Result, bool) {
			if s.Name == "/usr/bin/git" && stray == "" {
				stray = filepath.Join(filepath.Dir(s.Args[len(s.Args)-1]), "stray.txt")
				assert.NoError(t, os.WriteFile(stray, []byte("x"), 0o644))
			}
			return Result{}, false
		}
		code, out, errOut := run(t, f, func(o *Options) { o.Scratch = scratch })
		assert.Equal(t, 1, code, out)
		assert.Contains(t, out, "CHECK FAIL name=cleanup (")
		assert.Contains(t, errOut, "the scratch tree was not fully removed")
		assert.Contains(t, errOut, "not empty")
		assert.FileExists(t, stray, "an unexpected file was deleted")
	})
	t.Run("a listener that will not stop is reported", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.stopErr = errors.New("wait: pipe held open")
		code, out, errOut := run(t, f, nil)
		assert.Equal(t, 1, code, out)
		assert.Contains(t, out, "CHECK FAIL name=cleanup (")
		assert.Contains(t, errOut, "pipe held open")
	})
}

func TestTheScratchHoldsTheSecretTheDenialsNameAndTheSecretDirectory(t *testing.T) {
	t.Parallel()
	scratch := filepath.Join(realDir(t), "check")
	f := newFake()
	var secret string
	f.answer = func(s Spec) (Result, bool) {
		if s.Name == "/bin/cat" {
			b, err := os.ReadFile(s.Args[0])
			assert.NoError(t, err, "the named secret %s is not there", s.Args[0])
			secret = string(b)
		}
		return Result{}, false
	}
	run(t, f, func(o *Options) { o.Scratch = scratch })
	assert.Equal(t, "not-a-real-key\n", secret)
}

func TestOSSystemRunsRealProcessesAndReportsThem(t *testing.T) {
	t.Parallel()
	sys := OSSystem{}
	res := sys.Run(Spec{Name: "/bin/sh", Args: []string{"-c", "echo out; echo err >&2; exit 3"}, Env: []string{"PATH=/usr/bin:/bin"}})
	assert.NoError(t, res.Err)
	assert.Equal(t, 3, res.Code)
	assert.Equal(t, "out\n", res.Stdout)
	assert.Equal(t, "err\n", res.Stderr)
	assert.Equal(t, "out\nerr\n", res.Combined())
	r := sys.Run(Spec{Name: filepath.Join(t.TempDir(), "absent")})
	assert.Error(t, r.Err, "a program that cannot start reported %+v", r)
	assert.Equal(t, 127, r.exitCode())
	assert.True(t, sys.IsDir(t.TempDir()))
	assert.False(t, sys.IsDir(filepath.Join(t.TempDir(), "absent")))
	dir := t.TempDir()
	link := filepath.Join(dir, "l")
	require.NoError(t, os.Symlink(dir, link))
	target, ok := sys.Readlink(link)
	assert.True(t, ok)
	assert.Equal(t, dir, target)
	_, ok = sys.Readlink(dir)
	assert.False(t, ok, "a directory is not a symlink")
	resolved := sys.Real(link)
	assert.True(t, resolved != link || resolved == dir, "Real did not follow the link")
}

func TestOSSystemStartsAListenerAndStopsItWithoutAnError(t *testing.T) {
	t.Parallel()
	p, err := OSSystem{}.Start(Spec{Name: "/bin/sh", Args: []string{"-c", "exec sleep 30"}, Env: []string{"PATH=/usr/bin:/bin"}})
	require.NoError(t, err)
	assert.NoError(t, p.Stop(), "a killed listener is the expected end")
	_, err = OSSystem{}.Start(Spec{Name: filepath.Join(t.TempDir(), "absent")})
	assert.Error(t, err, "a listener that cannot start is an error")
}
