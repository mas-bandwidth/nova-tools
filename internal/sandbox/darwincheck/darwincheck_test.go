package darwincheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

func (p fakeProc) Stop() { p.f.mu.Lock(); p.f.stopped++; p.f.mu.Unlock() }

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
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// run is the check with a scratch tree under a temp directory.
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
	if code != 0 || len(fail) != 0 || len(skip) != 0 {
		t.Fatalf("exit %d, fail %v skip %v:\n%s", code, fail, skip, out)
	}
	if strings.Join(ok, " ") != strings.Join(suiteNames, " ") {
		t.Errorf("checks\n got %v\nwant %v", ok, suiteNames)
	}
}

func TestNoNetworkSkipsTheTwoDNSChecksAndTouchesNoNetwork(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, out, _ := run(t, f, nil)
	ok, fail, skip := checkLines(out)
	if code != 0 || len(fail) != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if strings.Join(skip, " ") != "dns_resolves dns_resolves_control" {
		t.Errorf("skipped %v", skip)
	}
	if len(ok) != len(suiteNames)-2 {
		t.Errorf("%d checks passed, want %d", len(ok), len(suiteNames)-2)
	}
	if !strings.Contains(out, "CHECK SKIP name=dns_resolves reason=no_network\n") {
		t.Errorf("the skip line is not the one callers grep:\n%s", out)
	}
	for _, s := range f.walledRuns() {
		if strings.Contains(s.Args[len(s.Args)-1], "curl") {
			t.Errorf("curl ran with the network off: %v", s.Args)
		}
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
	want := "write_outside read_secret list_ancestor unix_socket_outside clipboard_denied nested_sandbox_refused"
	if code != 1 || strings.Join(fail, " ") != want {
		t.Errorf("exit %d, failed %v, want %s:\n%s", code, fail, want, out)
	}
	if !strings.Contains(out, "CHECK FAIL name=write_outside SUCCEEDED inside the wall\n") {
		t.Errorf("the denial's failure line is not the one callers grep:\n%s", out)
	}
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
		found := false
		for _, x := range fail {
			found = found || x == n
		}
		if !found {
			t.Errorf("%s did not fail under a wall that denies everything", n)
		}
	}
	if code != 1 || !strings.Contains(out, "CHECK FAIL name=cd_absolute rc=1 out=Operation not permitted\n") {
		t.Errorf("exit %d; an expectOK failure names the rc and the first line of output:\n%s", code, out)
	}
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
	if code != 1 || strings.Join(fail, " ") != "read_secret_control" {
		t.Errorf("exit %d, failed %v:\n%s", code, fail, out)
	}
	if !strings.Contains(out, "CHECK FAIL name=read_secret_control rc=1 (outside the wall)\n") {
		t.Errorf("control failure line:\n%s", out)
	}
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
		if !have[denial] || !have[denial+"_control"] {
			t.Errorf("%s has no passing control %s_control", denial, denial)
		}
	}
	// The controls run the named programs, outside: never through sandbox-exec.
	controls := map[string]bool{}
	for _, s := range f.ran {
		if s.Name != "/usr/bin/sandbox-exec" {
			controls[s.Name] = true
		}
	}
	for _, p := range []string{"/usr/bin/touch", "/bin/cat", "/bin/ls", "/usr/bin/c++"} {
		if !controls[p] {
			t.Errorf("the control %s did not run outside the wall", p)
		}
	}
}

func TestEveryWalledCommandRunsFromTheWriteSetInAClosedEnvironment(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.environ = append(f.environ, "SSH_AUTH_SOCK=/s", "AWS_SECRET=leaky")
	run(t, f, nil)
	walled := f.walledRuns()
	if len(walled) == 0 {
		t.Fatal("no walled run")
	}
	for _, s := range walled {
		if !strings.HasSuffix(s.Dir, "/check/w") {
			t.Errorf("walled run from %s, want the write set", s.Dir)
		}
		env := strings.Join(s.Env, "\n")
		if strings.Contains(env, "AWS_SECRET") || strings.Contains(env, "SSH_AUTH_SOCK") || strings.Contains(env, "GPG_AGENT_INFO") {
			t.Errorf("the walled environment is not closed and scrubbed:\n%s", env)
		}
		for _, want := range []string{"HOME=" + s.Dir + "/home", "PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + s.Dir + "/.nova-sandbox-tmp", "TMP=", "TEMP=", "NOVA_KEEP=kept"} {
			if !strings.Contains(env, want) {
				t.Errorf("the walled environment lacks %s:\n%s", want, env)
			}
		}
		// The profile is inline, never a file; shell runs the command.
		if s.Args[0] != "-p" || s.Args[len(s.Args)-4] != "--" || s.Args[len(s.Args)-3] != "/bin/sh" || s.Args[len(s.Args)-2] != "-c" {
			t.Errorf("walled argv: %v", s.Args)
		}
		for _, d := range []string{"READ0=", "WRITE0=", "HOME="} {
			found := false
			for i, a := range s.Args {
				found = found || (a == "-D" && strings.HasPrefix(s.Args[i+1], d))
			}
			if !found {
				t.Errorf("walled argv lacks -D %s: %v", d, s.Args)
			}
		}
		if strings.HasSuffix(s.Args[1], "\n") {
			t.Errorf("the inline profile keeps its trailing newline")
		}
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
	if len(git) != 3 {
		t.Fatalf("git ran %d times, want init, add, commit: %v", len(git), git)
	}
	joined := func(a []string) string { return strings.Join(a, " ") }
	if !strings.HasPrefix(joined(git[0]), "-c init.defaultBranch=main init -q ") || !strings.HasSuffix(joined(git[0]), "/check/ref") {
		t.Errorf("init: %v", git[0])
	}
	for i, verb := range []string{" add -A", " commit -q -m seed"} {
		a := joined(git[i+1])
		if !strings.Contains(a, "-c user.name=check -c user.email=check@example.com -c commit.gpgsign=false") || !strings.HasSuffix(a, verb) {
			t.Errorf("git step %d: %s", i+1, a)
		}
	}
	if f.ran[0].Name != "/usr/bin/git" {
		t.Errorf("a walled run came before the repository was seeded")
	}
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
	if code != 1 || !strings.Contains(out, "CHECK FAIL name=reference_repo") || len(f.walledRuns()) != 0 {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestNotDarwinIsOneFailLineAndNothingRuns(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.goos = "linux"
	code, out, _ := run(t, f, nil)
	if code != 1 || out != "CHECK FAIL name=platform (this check is darwin only)\n" {
		t.Errorf("exit %d: %q", code, out)
	}
	if len(f.ran) != 0 || len(f.started) != 0 {
		t.Errorf("processes ran on a platform with no wall: %v", f.ran)
	}
}

func TestNoTemplateAndNoGitAreNamedFails(t *testing.T) {
	t.Parallel()
	f := newFake()
	if code, out, _ := run(t, f, func(o *Options) { o.Template = "" }); code != 1 || !strings.HasPrefix(out, "CHECK FAIL name=template_present") {
		t.Errorf("exit %d: %q", code, out)
	}
	g := newFake()
	g.noGit = true
	if code, out, _ := run(t, g, nil); code != 1 || !strings.HasPrefix(out, "CHECK FAIL name=git_present") {
		t.Errorf("exit %d: %q", code, out)
	}
}

func TestDumpProfilePrintsTheFilledProfileAndRunsNoCheck(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, out, _ := run(t, f, func(o *Options) { o.DumpProfile = true })
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "CHECK ") || strings.Contains(out, "@@") {
		t.Errorf("the dump carries check lines or an unfilled marker:\n%s", out)
	}
	for _, want := range []string{`(allow file-read* (subpath (param "READ0")))`, `(literal "/private/var/run/mDNSResponder")`, `(allow file-read-metadata (literal "`} {
		if !strings.Contains(out, want) {
			t.Errorf("the dump lacks %s:\n%s", want, out)
		}
	}
	if len(f.walledRuns()) != 0 {
		t.Errorf("a dump ran %d walled commands", len(f.walledRuns()))
	}
	// The ancestors name the real write set, which is what makes the two fill modes comparable.
	if !strings.Contains(out, `(literal "`+filepath.Dir(f.ran[0].Args[len(f.ran[0].Args)-1])+`")`) {
		t.Errorf("the dump does not grant the scratch directory's metadata:\n%s", out)
	}
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
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(errOut, "POLICY OK") {
		t.Errorf("the generator's stderr was swallowed: %q", errOut)
	}
	var gen Spec
	for _, s := range f.ran {
		if s.Name == "/tmp/nova-sandbox" {
			gen = s
		}
	}
	if len(gen.Args) != 5 || gen.Args[0] != "policy" || gen.Args[1] != "--read" || gen.Args[3] != "--write" || !strings.HasSuffix(gen.Args[2], "/check/ref") || !strings.HasSuffix(gen.Args[4], "/check/w") {
		t.Errorf("the generator was run as %v", gen.Args)
	}
	if !strings.HasPrefix(lastEnv(gen.Env, "HOME"), gen.Args[4]) {
		t.Errorf("the generator ran with HOME %q, want one inside the write set", lastEnv(gen.Env, "HOME"))
	}
	for _, s := range f.walledRuns() {
		if !strings.Contains(s.Args[1], ";; the tool's own") || strings.Contains(s.Args[1], ";; template") {
			t.Errorf("a walled run used the hand-filled profile: %s", s.Args[1])
		}
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
	if code != 1 || out != "CHECK FAIL name=tool_generated_profile (/tmp/nova-sandbox policy refused)\n" {
		t.Errorf("exit %d: %q", code, out)
	}
	if len(f.walledRuns()) != 0 {
		t.Errorf("checks ran against a profile that was not generated")
	}
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
		if len(withSock) != 1 || len(without) != 1 {
			t.Fatalf("curl ran %d times with the socket and %d without", len(withSock), len(without))
		}
		if strings.Replace(withSock[0].Args[1], ` (literal "`+mdnsSocket+`")`, "", 1) != without[0].Args[1] {
			t.Errorf("the control profile differs from the profile under test by more than the socket literal")
		}
		if env := strings.Join(without[0].Env, " "); strings.Contains(env, "NOVA_KEEP") || strings.Contains(env, "TMP=") {
			t.Errorf("the control's environment is not the bare one: %s", env)
		}
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
		if code != 1 || strings.Join(fail, " ") != "dns_resolves" || !strings.Contains(out, "CHECK FAIL name=dns_resolves http_code=000 (the name did not resolve)\n") {
			t.Errorf("exit %d, failed %v:\n%s", code, fail, out)
		}
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
		if !strings.Contains(out, "CHECK FAIL name=dns_resolves http_code= (the name did not resolve)\n") {
			t.Errorf("\n%s", out)
		}
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
		if code != 1 || strings.Join(fail, " ") != "dns_resolves_control" || !strings.Contains(out, "http_code=404 WITHOUT the socket: DNS is reaching the resolver some other way") {
			t.Errorf("exit %d, failed %v:\n%s", code, fail, out)
		}
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
		if code, out, _ := run(t, f, net); code != 0 {
			t.Errorf("a 404 is a name that resolved:\n%s", out)
		}
	})
}

// The socket denial needs its listener: with none bound the check fails by
// name and the control is not reported, rather than passing for the wrong reason.
func TestNoListenerIsAFailNotAPass(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, out, _ := runWithSockets(t, f, false)
	ok, fail, _ := checkLines(out)
	if code != 1 || strings.Join(fail, " ") != "unix_socket_outside" {
		t.Errorf("exit %d, failed %v:\n%s", code, fail, out)
	}
	for _, n := range ok {
		if n == "unix_socket_outside_control" || n == "unix_socket_outside" {
			t.Errorf("%s was reported with no listener", n)
		}
	}
	if !strings.Contains(out, "no listener: nc -lU did not bind (sun_path is 104 bytes)") {
		t.Errorf("\n%s", out)
	}
	// Two bounded waits (the outside pair, then the job's own), ten polls each,
	// on a clock that does not wait.
	if f.sleeps != 2*socketPolls {
		t.Errorf("slept %d times, want %d: the wait is bounded", f.sleeps, 2*socketPolls)
	}
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
	if len(f.started) != 3 {
		t.Fatalf("started %d listeners, want the two outside and the job's own", len(f.started))
	}
	for _, s := range f.started {
		if s.Name != "/usr/bin/nc" || len(s.Args) != 2 || s.Args[0] != "-lU" || !strings.HasPrefix(s.Args[1], "./") || strings.Contains(s.Args[1][2:], "/") {
			t.Errorf("listener %v: the socket path must be relative (sun_path is 104 bytes)", s)
		}
	}
	if !strings.HasSuffix(f.started[0].Dir, "/secret") || !strings.HasSuffix(f.started[2].Dir, "/check/w") {
		t.Errorf("listener directories %s, %s", f.started[0].Dir, f.started[2].Dir)
	}
	if f.stopped != 3 {
		t.Errorf("%d listeners stopped, want all 3", f.stopped)
	}
}

// Only what the check made is removed. A scratch directory a caller handed in
// keeps what the caller put there; one the check created is removed whole.
func TestCleanupRemovesOnlyWhatTheCheckMade(t *testing.T) {
	t.Parallel()
	t.Run("a scratch directory that existed is kept, with its own contents", func(t *testing.T) {
		t.Parallel()
		scratch := filepath.Join(realDir(t), "mine")
		if err := os.MkdirAll(scratch, 0o755); err != nil {
			t.Fatal(err)
		}
		keep := filepath.Join(scratch, "keep.txt")
		if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		f := newFake()
		run(t, f, func(o *Options) { o.Scratch = scratch })
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("the caller's file was removed: %v", err)
		}
		entries, _ := os.ReadDir(scratch)
		if len(entries) != 1 {
			t.Errorf("the check left %d entries in the scratch directory: %v", len(entries)-1, entries)
		}
	})
	t.Run("a scratch directory the check made is removed", func(t *testing.T) {
		t.Parallel()
		scratch := filepath.Join(realDir(t), "new")
		f := newFake()
		run(t, f, func(o *Options) { o.Scratch = scratch })
		if _, err := os.Stat(scratch); err == nil {
			t.Errorf("the scratch directory the check created is still there")
		}
	})
	t.Run("a read-only tree in the write set is removed too", func(t *testing.T) {
		t.Parallel()
		scratch := filepath.Join(realDir(t), "new")
		f := newFake()
		f.answer = func(s Spec) (Result, bool) {
			if s.Name == "/usr/bin/git" {
				// git's objects are read-only, and so is their directory.
				d := filepath.Join(scratch, "w", "g", "objects")
				_ = os.MkdirAll(d, 0o755)
				_ = os.WriteFile(filepath.Join(d, "obj"), []byte("x"), 0o444)
				_ = os.Chmod(d, 0o555)
			}
			return Result{}, false
		}
		run(t, f, func(o *Options) { o.Scratch = scratch })
		if _, err := os.Stat(scratch); err == nil {
			t.Errorf("a read-only tree kept the scratch directory alive")
		}
	})
	t.Run("the default scratch is beside the base directory and named for the process", func(t *testing.T) {
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
		if want := filepath.Join(base, ".darwin-check-scratch."+strconv.Itoa(os.Getpid()), "ref"); seen != want {
			t.Errorf("the reference repository was made at %s, want %s", seen, want)
		}
		if entries, _ := os.ReadDir(base); len(entries) != 0 {
			t.Errorf("the base directory kept %v", entries)
		}
	})
	t.Run("a dump cleans up too", func(t *testing.T) {
		t.Parallel()
		scratch := filepath.Join(realDir(t), "new")
		run(t, newFake(), func(o *Options) { o.Scratch = scratch; o.DumpProfile = true })
		if _, err := os.Stat(scratch); err == nil {
			t.Errorf("a dump left its scratch tree")
		}
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
			if err != nil {
				t.Errorf("the named secret %s is not there: %v", s.Args[0], err)
			}
			secret = string(b)
		}
		return Result{}, false
	}
	run(t, f, func(o *Options) { o.Scratch = scratch })
	if secret != "not-a-real-key\n" {
		t.Errorf("secret = %q", secret)
	}
}

func TestOSSystemRunsRealProcessesAndReportsThem(t *testing.T) {
	t.Parallel()
	sys := OSSystem{}
	res := sys.Run(Spec{Name: "/bin/sh", Args: []string{"-c", "echo out; echo err >&2; exit 3"}, Env: []string{"PATH=/usr/bin:/bin"}})
	if res.Err != nil || res.Code != 3 || res.Stdout != "out\n" || res.Stderr != "err\n" || res.Combined() != "out\nerr\n" {
		t.Errorf("Run = %+v", res)
	}
	if r := sys.Run(Spec{Name: filepath.Join(t.TempDir(), "absent")}); r.Err == nil || r.exitCode() != 127 {
		t.Errorf("a program that cannot start reported %+v", r)
	}
	if !sys.IsDir(t.TempDir()) || sys.IsDir(filepath.Join(t.TempDir(), "absent")) {
		t.Errorf("IsDir")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "l")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if target, ok := sys.Readlink(link); !ok || target != dir {
		t.Errorf("Readlink = %q, %v", target, ok)
	}
	if _, ok := sys.Readlink(dir); ok {
		t.Errorf("a directory is not a symlink")
	}
	if sys.Real(link) == link && sys.Real(link) != dir {
		t.Errorf("Real did not follow the link")
	}
}
