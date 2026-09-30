// Package darwincheck is the darwin profile check: it fills profiles/darwin.sb.tmpl
// for a scratch write set, then runs, INSIDE the wall and by ABSOLUTE path, the
// things a swarm worker does in its first second (cd, mkdir -p, git init, git
// clone --shared, a config write under HOME, cat /etc/hosts, /bin/sh -c true,
// killing its own child, stdout to a pipe and to a file in the write set), and
// asserts that a write outside, a read of the named secret, a listing of an
// ancestor, a connect to a socket outside the write set and a nested sandbox all
// FAIL, each with a control run OUTSIDE the wall so that a check cannot pass by
// being impossible.
//
// One line per check, CHECK OK name=... or CHECK FAIL name=..., and Run returns 1
// on any FAIL. The count is the check's own: every expectOK, expectDeny,
// controlOK and report in Run is one line, and no prose anywhere states a number
// that this file can outgrow.
//
// The processes it runs are the real ones (git, /bin/sh, mkdir, cat, pbpaste,
// nc, curl, sandbox-exec): the subject of the check is what those do inside the
// wall, so the driver is Go and the probes are not. Everything the driver asks of
// the machine goes through System, so a test holds the driver's own logic with a
// System that answers from a script; the wall itself is measured only by a run on
// a darwin machine, the operator's (tools/sandboxcheck) or the tool's own test.
//
// The scratch tree lives under the directory the operator chooses, never in a
// shared temp directory. The check works in its OWN os.MkdirTemp child of that
// directory and removes only that child at the end, so a directory a caller
// handed in keeps everything the check did not make. A cleanup that cannot
// remove something says so on standard error and in the exit code.
package darwincheck

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// Options is how a caller that is a TEST rather than an operator changes the run.
type Options struct {
	// Template is profiles.DarwinTemplate.
	Template string
	// Scratch is the directory the check makes its own scratch child in (a
	// fresh os.MkdirTemp directory), so a Go test can hand it t.TempDir() and
	// reach outside nothing, and whatever is already there is left alone.
	// Empty: BaseDir.
	Scratch string
	// BaseDir is where the scratch child lives when Scratch is empty; empty is
	// the working directory.
	BaseDir string
	// NoNetwork skips the two DNS checks, the only ones that touch the network.
	// The operator run keeps them: they are the measurement of rule 7 and a Go
	// test is the wrong place for it.
	NoNetwork bool
	// DumpProfile prints the filled profile on standard output and returns 0
	// before any check runs, so a test can compare the hand filler with the tool
	// generator without executing the suite.
	DumpProfile bool
	// Fill is a nova-sandbox binary. When set, the profile under test is the one
	// THAT TOOL generates for this scratch write set rather than the one filled
	// here: one text, filled two ways, and a drift between them is a FAIL here
	// rather than a surprise in a job.
	Fill string

	Stdout, Stderr io.Writer
}

// The socket wait is bounded: two seconds, never unbounded.
const (
	socketPolls    = 10
	socketInterval = 200 * time.Millisecond
)

// the walled process's PATH, and the one line the DNS check reads.
const (
	wallPath = "/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	dnsCmd   = "curl -s -o /dev/null -w '%{http_code}' --max-time 15 https://example.com"
)

// checker is one run of the suite: its scratch tree, the profile, the child
// environment, and what has been reported.
type checker struct {
	sys    System
	out    io.Writer
	failed bool

	scratch, w, ref, secret, outside, home, ntmp string
	profile                                      string
	childEnv                                     []string
	procs                                        []Process

	// parent is the directory the scratch child was made in, and parentMade says
	// the check created it, so cleanup removes it only then.
	parent     string
	parentMade bool
}

// Run is the whole check. It returns the exit code: 0 when every check passed or
// the profile was dumped, 1 on any FAIL, including a cleanup that could not
// remove what the check made.
func Run(o Options, sys System) int {
	out := o.Stdout
	if out == nil {
		out = io.Discard
	}
	c := &checker{sys: sys, out: out}

	if sys.GOOS() != "darwin" {
		fmt.Fprintln(out, "CHECK FAIL name=platform (this check is darwin only)")
		return 1
	}
	if o.Template == "" {
		fmt.Fprintln(out, "CHECK FAIL name=template_present (no profile template)")
		return 1
	}
	git, ok := sys.LookPath("git")
	if !ok {
		fmt.Fprintln(out, "CHECK FAIL name=git_present (git is on no PATH entry)")
		return 1
	}

	code := c.run(o, git)
	if err := c.cleanup(); err != nil {
		fmt.Fprintf(out, "CHECK FAIL name=cleanup (%v)\n", err)
		if o.Stderr != nil {
			fmt.Fprintf(o.Stderr, "darwincheck: the scratch tree was not fully removed: %v\n", err)
		}
		return 1
	}
	return code
}

// run makes the scratch tree and runs the checks in it; Run cleans up after it.
func (c *checker) run(o Options, git string) int {
	out, sys := c.out, c.sys
	if err := c.makeScratch(o); err != nil {
		fmt.Fprintf(out, "CHECK FAIL name=scratch (%v)\n", err)
		return 1
	}

	if err := c.seedReference(git); err != nil {
		fmt.Fprintf(out, "CHECK FAIL name=reference_repo (%v)\n", err)
		return 1
	}

	// Fill the template; the tool's own generator replaces the hand fill when asked.
	c.profile = fillTemplate(o.Template, fillInput{write: c.w, ref: c.ref, optRoots: optionalRoots(sys, filepath.Dir(git))})
	if o.Fill != "" {
		res := sys.Run(Spec{
			Name: o.Fill, Args: []string{"policy", "--read", c.ref, "--write", c.w},
			Env: withEnv(sys.Environ(), "HOME="+c.home),
		})
		if o.Stderr != nil {
			if _, err := io.WriteString(o.Stderr, res.Stderr); err != nil {
				fmt.Fprintf(out, "CHECK FAIL name=tool_generated_profile_stderr (%v)\n", err)
				return 1
			}
		}
		if res.Err != nil || res.Code != 0 {
			fmt.Fprintf(out, "CHECK FAIL name=tool_generated_profile (%s policy refused)\n", o.Fill)
			return 1
		}
		c.profile = res.Stdout
	}

	// The child environment, as a swarm worker's launcher really holds it: an SSH
	// agent socket and an agent-shaped name beside a variable that must survive.
	// The wrapper builds the child environment from it by EXCLUSION, and the set
	// is rule 9's exact one.
	agentSock := filepath.Join(c.scratch, "secret", "agent.sock")
	c.childEnv = childEnv([]string{
		"SSH_AUTH_SOCK=" + agentSock,
		"GPG_AGENT_INFO=" + agentSock + ":1:1",
		"NOVA_KEEP=kept",
	})

	if o.DumpProfile {
		if _, err := io.WriteString(out, c.profile); err != nil {
			fmt.Fprintf(out, "CHECK FAIL name=dump_profile (%v)\n", err)
			return 1
		}
		return 0
	}

	c.suite(o)
	if c.failed {
		return 1
	}
	return 0
}

// withEnv is environ with one variable set, replacing an earlier one.
func withEnv(environ []string, kv string) []string {
	key, _, _ := strings.Cut(kv, "=")
	out := make([]string, 0, len(environ)+1)
	for _, e := range environ {
		if k, _, _ := strings.Cut(e, "="); k != key {
			out = append(out, e)
		}
	}
	return append(out, kv)
}

// scratchNames are the entries the check makes directly in its scratch child.
// Only these are removed, and then the child itself if it is empty, so a leftover
// the check did not expect is reported and never deleted.
var scratchNames = []string{"w", "ref", "secret", "outside"}

// scratchPrefix names the check's own scratch child: the prefix of its
// os.MkdirTemp pattern, then the process id, then the random part.
const scratchPrefix = ".darwin-check-scratch."

// makeScratch makes the check's OWN scratch child with os.MkdirTemp, in the
// directory the caller handed in or the default one, and the write set inside
// it. A directory that was already there is never the scratch tree: the check
// neither writes into it nor removes from it.
func (c *checker) makeScratch(o Options) (err error) {
	parent := o.Scratch
	if parent == "" {
		if parent = o.BaseDir; parent == "" {
			if parent, err = os.Getwd(); err != nil {
				return err
			}
		}
	}
	if _, serr := os.Stat(parent); errors.Is(serr, fs.ErrNotExist) {
		c.parentMade = true
	}
	if err = os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	// pwd -P: paths are compared and granted by their real spelling (macOS temp
	// directories sit behind /var -> /private/var).
	if real, rerr := filepath.EvalSymlinks(parent); rerr == nil {
		parent = real
	}
	c.parent = parent
	scratch, err := os.MkdirTemp(parent, scratchPrefix+strconv.Itoa(os.Getpid())+".*")
	if err != nil {
		return err
	}
	c.scratch = scratch
	c.w = filepath.Join(scratch, "w")                  // the write set
	c.ref = filepath.Join(scratch, "ref")              // the read set: a local repo to clone --shared
	c.secret = filepath.Join(scratch, "secret", "env") // a named secret, in NEITHER list
	c.outside = filepath.Join(scratch, "outside")      // the write-outside target, in NEITHER list
	c.home = filepath.Join(c.w, "home")                // rule 9: HOME inside a --write
	c.ntmp = filepath.Join(c.w, ".nova-sandbox-tmp")   // rule 8
	for _, d := range []string{c.w, c.ref, c.home, c.ntmp, filepath.Dir(c.secret), c.outside} {
		if err = os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(c.secret, []byte("not-a-real-key\n"), 0o644)
}

// cleanup stops the listeners and removes what the check made: its own scratch
// child, and the directory that held it when the check created that too. The
// write set can hold read-only trees (git's objects), so every directory is made
// writable first. Every error is returned, joined; nothing is dropped.
func (c *checker) cleanup() error {
	var errs []error
	for _, p := range c.procs {
		if err := p.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("stopping a listener: %w", err))
		}
	}
	if c.scratch == "" {
		return errors.Join(errs...)
	}
	removed := true
	for _, n := range scratchNames {
		p := filepath.Join(c.scratch, n)
		walkErr := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return os.Chmod(path, 0o755)
			}
			return nil
		})
		if walkErr != nil {
			removed = false
			errs = append(errs, fmt.Errorf("making %s writable: %w", p, walkErr))
		}
		// Strictly below the scratch child, which safepath checks before it removes.
		if err := safepath.RemoveUnder(c.scratch, p); err != nil {
			removed = false
			errs = append(errs, fmt.Errorf("removing %s: %w", p, err))
		}
	}
	if !removed {
		return errors.Join(errs...)
	}
	// Empty by now; a leftover the check did not expect is an error, never removed.
	if err := os.Remove(c.scratch); err != nil {
		return errors.Join(append(errs, fmt.Errorf("removing the scratch directory %s: %w", c.scratch, err))...)
	}
	if c.parentMade {
		if err := os.Remove(c.parent); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("removing the directory the check created, %s: %w", c.parent, err))
		}
	}
	return errors.Join(errs...)
}

// seedReference makes the local repository the check clones with --shared.
func (c *checker) seedReference(git string) error {
	if err := os.WriteFile(filepath.Join(c.ref, "file.txt"), []byte("ref\n"), 0o644); err != nil {
		return err
	}
	ident := []string{"-c", "user.name=check", "-c", "user.email=check@example.com", "-c", "commit.gpgsign=false"}
	steps := [][]string{
		{"-c", "init.defaultBranch=main", "init", "-q", c.ref},
		append(append([]string{"-C", c.ref}, ident...), "add", "-A"),
		append(append([]string{"-C", c.ref}, ident...), "commit", "-q", "-m", "seed"),
	}
	for _, args := range steps {
		if res := c.sys.Run(Spec{Name: git, Args: args}); res.Err != nil || res.Code != 0 {
			return fmt.Errorf("git %s: %v %s", strings.Join(args[:min(len(args), 4)], " "), res.Err, strings.TrimSpace(res.Combined()))
		}
	}
	return nil
}

// report prints one check line.
func (c *checker) ok(name string) { fmt.Fprintf(c.out, "CHECK OK name=%s\n", name) }

func (c *checker) fail(name, detail string) {
	c.failed = true
	fmt.Fprintf(c.out, "CHECK FAIL name=%s %s\n", name, detail)
}

// sandboxArgs is the sandbox-exec argument list that applies a profile for the
// scratch write set and runs command in a shell. The profile text is passed
// INLINE with -p, never as a file, and without its trailing newline.
func (c *checker) sandboxArgs(profile, command string) []string {
	return []string{
		"-p", strings.TrimRight(profile, "\n"),
		"-D", "READ0=" + c.ref, "-D", "WRITE0=" + c.w, "-D", "HOME=" + c.home,
		"--", "/bin/sh", "-c", command,
	}
}

// walled runs a shell command inside the wall, from the write set (rule 13: a
// working directory outside every named path is denied to getcwd(3), and every
// git command dies with "shell-init: error retrieving current directory").
func (c *checker) walled(command string) Result {
	env := []string{
		"HOME=" + c.home, "PATH=" + wallPath,
		"TMPDIR=" + c.ntmp, "TMP=" + c.ntmp, "TEMP=" + c.ntmp,
	}
	return c.sys.Run(Spec{
		Name: "/usr/bin/sandbox-exec", Args: c.sandboxArgs(c.profile, command),
		Dir: c.w, Env: append(env, c.childEnv...),
	})
}

// firstLine is `head -1`.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// exitCode is the process's status, 127 when it never started.
func (r Result) exitCode() int {
	if r.Err != nil {
		return 127
	}
	return r.Code
}

// expectOK: the command succeeds inside the wall.
func (c *checker) expectOK(name, command string) {
	res := c.walled(command)
	if rc := res.exitCode(); rc == 0 {
		c.ok(name)
	} else {
		c.fail(name, fmt.Sprintf("rc=%d out=%s", rc, firstLine(res.Combined())))
	}
}

// expectDeny: the command fails inside the wall.
func (c *checker) expectDeny(name, command string) {
	if res := c.walled(command); res.exitCode() != 0 {
		c.ok(name)
	} else {
		c.fail(name, "SUCCEEDED inside the wall")
	}
}

// controlOK: the same thing run OUTSIDE the wall succeeds, so a denial cannot pass
// by being impossible.
func (c *checker) controlOK(name string, argv ...string) {
	res := c.sys.Run(Spec{Name: argv[0], Args: argv[1:]})
	if rc := res.exitCode(); rc == 0 {
		c.ok(name)
	} else {
		c.fail(name, fmt.Sprintf("rc=%d (outside the wall)", rc))
	}
}

// listen starts a unix-socket listener in dir on a RELATIVE path: sun_path is 104
// bytes and an absolute path under a deep scratch silently fails to bind, which
// would pass the denial for the wrong reason.
func (c *checker) listen(dir, name string) {
	p, err := c.sys.Start(Spec{Name: "/usr/bin/nc", Args: []string{"-lU", "./" + name}, Dir: dir})
	if err == nil {
		c.procs = append(c.procs, p)
	}
}

// waitForSocket polls for the socket, bounded.
func (c *checker) waitForSocket(path string) bool {
	for i := 0; i < socketPolls; i++ {
		if c.sys.IsSocket(path) {
			return true
		}
		c.sys.Sleep(socketInterval)
	}
	return false
}

// suite runs every check, in order.
func (c *checker) suite(o Options) {
	w, home, ref := c.w, c.home, c.ref
	q := func(s string) string { return "'" + s + "'" }

	// --- the worker's first second, by absolute path, inside the wall ---
	c.expectOK("cd_absolute", "cd "+q(w)+" && test \"$(pwd)\" = "+q(w))
	c.expectOK("mkdir_p_absolute", "mkdir -p "+q(w+"/a/b/c")+" && test -d "+q(w+"/a/b/c"))
	c.expectOK("git_init_absolute", "git init -q "+q(w+"/g")+" && test -d "+q(w+"/g/.git"))
	c.expectOK("git_clone_shared", "git clone -q --shared "+q(ref)+" "+q(w+"/clone")+" && test -f "+q(w+"/clone/file.txt"))
	// From inside the repo git init just made: this scratch may live inside a
	// checkout, and git's upward discovery would otherwise stop at the outer .git,
	// which the wall denies.
	c.expectOK("home_config_write", "cd "+q(w+"/g")+" && git config --global user.name nova-check && test -f "+q(home+"/.gitconfig"))
	c.expectOK("cat_etc_hosts", "cat /etc/hosts > /dev/null")
	c.expectOK("sh_c_true", "/bin/sh -c true")
	c.expectOK("child_kill", "sleep 5 & kill $!")
	c.expectOK("stdout_to_file", "echo nova > "+q(w+"/out.txt")+" && test \"$(cat "+q(w+"/out.txt")+")\" = nova")

	// /usr/bin/c++ is an Xcode shim. The profile must grant xcode_select_link (and
	// the developer directory it points at, when that is not already a root) so a
	// C++ probe compiles inside the wall the way it does outside it.
	if err := os.WriteFile(filepath.Join(w, "probe.cpp"), []byte("#include <iostream>\nint main(){ std::cout << \"ok\\n\"; return 0; }\n"), 0o644); err != nil {
		c.fail("cxx_probe_source", fmt.Sprintf("the C++ probe could not be written to the write set: %v", err))
	} else {
		c.expectOK("cxx_compile", "/usr/bin/c++ -o "+q(w+"/probe")+" "+q(w+"/probe.cpp")+" && test -x "+q(w+"/probe")+" && "+q(w+"/probe")+" | grep -qx ok")
		c.controlOK("cxx_compile_control", "/usr/bin/c++", "-o", filepath.Join(c.outside, "probe-ctl"), filepath.Join(w, "probe.cpp"))
	}

	// stdout to a pipe the caller drains (rule 12)
	if piped := c.walled("echo nova-pipe"); piped.exitCode() == 0 && strings.TrimRight(piped.Stdout, "\n") == "nova-pipe" {
		c.ok("stdout_to_pipe")
	} else {
		c.fail("stdout_to_pipe", fmt.Sprintf("rc=%d out=%s", piped.exitCode(), strings.TrimRight(piped.Stdout, "\n")))
	}

	// --- the three denials, each with a control outside the wall ---
	pid := strconv.Itoa(os.Getpid())
	c.expectDeny("write_outside", ": > "+q(filepath.Join(c.outside, "probe."+pid)))
	c.controlOK("write_outside_control", "/usr/bin/touch", filepath.Join(c.outside, "control."+pid))
	c.expectDeny("read_secret", "cat "+q(c.secret)+" > /dev/null")
	c.controlOK("read_secret_control", "/bin/cat", c.secret)
	c.expectDeny("list_ancestor", "ls "+q(c.scratch)+" > /dev/null")
	c.controlOK("list_ancestor_control", "/bin/ls", c.scratch)

	// --- no socket outside the write set is reachable ---
	// The socket lives in the secret directory, in NEITHER list, standing in for the
	// SSH agent's socket. Two listeners, because `nc -lU` serves one connection and
	// exits: the walled attempt must not consume the one the control needs.
	sockdir := filepath.Dir(c.secret)
	c.listen(sockdir, "agent.sock")
	c.listen(sockdir, "agent-control.sock")
	if c.waitForSocket(filepath.Join(sockdir, "agent.sock")) && c.waitForSocket(filepath.Join(sockdir, "agent-control.sock")) {
		c.expectDeny("unix_socket_outside", "nc -U ../secret/agent.sock < /dev/null")
		ctl := c.sys.Run(Spec{Name: "/bin/sh", Args: []string{"-c", "/usr/bin/nc -U ./agent-control.sock < /dev/null >/dev/null 2>&1"}, Dir: sockdir})
		if rc := ctl.exitCode(); rc == 0 {
			c.ok("unix_socket_outside_control")
		} else {
			c.fail("unix_socket_outside_control", fmt.Sprintf("rc=%d (outside the wall)", rc))
		}
	} else {
		c.fail("unix_socket_outside", "no listener: nc -lU did not bind (sun_path is 104 bytes)")
	}
	// the job's own socket, under the write set, is the one that must still connect
	c.listen(w, "job.sock")
	c.waitForSocket(filepath.Join(w, "job.sock"))
	c.expectOK("unix_socket_inside", "nc -U ./job.sock < /dev/null")

	// --- rule 7: DNS resolves inside the wall, and does so because of the socket ---
	// curl to a NAME, not an IP: any HTTP status at all means the name resolved and
	// the connection was made (200 and 404 both count). rc=6 / 000 is "could not
	// resolve host".
	if o.NoNetwork {
		fmt.Fprintln(c.out, "CHECK SKIP name=dns_resolves reason=no_network")
		fmt.Fprintln(c.out, "CHECK SKIP name=dns_resolves_control reason=no_network")
	} else {
		c.dnsChecks()
	}

	// --- rule 7: mach-lookup is narrowed, and the clipboard is the witness ---
	c.expectDeny("clipboard_denied", "pbpaste > /dev/null")

	// --- rule 4: a sandbox cannot be nested inside this one ---
	// A wrapped launcher must drop its own sandbox flag rather than discover this at
	// run time: sandbox-exec: sandbox_apply: Operation not permitted.
	c.expectDeny("nested_sandbox_refused", "/usr/bin/sandbox-exec -p '(version 1)(allow default)' /bin/sh -c true")

	// --- rule 9: the agent is gone from the child environment ---
	// The caller's environment held SSH_AUTH_SOCK and GPG_AGENT_INFO; the child must
	// hold neither, and must still hold what the caller meant to pass (the provider
	// key arrives this way, rule 6).
	c.expectOK("env_no_ssh_auth_sock", `test -z "${SSH_AUTH_SOCK:-}" && test -z "${GPG_AGENT_INFO:-}" && test "${NOVA_KEEP:-}" = kept`)
}

// dnsChecks are the two checks that touch the network: the name resolves inside
// the wall, and the SAME profile with the mDNSResponder literal removed must NOT
// resolve, so the first cannot pass by the socket being irrelevant.
func (c *checker) dnsChecks() {
	switch code := strings.TrimRight(c.walled(dnsCmd).Stdout, "\n"); code {
	case "000", "":
		c.fail("dns_resolves", fmt.Sprintf("http_code=%s (the name did not resolve)", code))
	default:
		c.ok("dns_resolves")
	}
	nodns := strings.ReplaceAll(c.profile, ` (literal "`+mdnsSocket+`")`, "")
	res := c.sys.Run(Spec{
		Name: "/usr/bin/sandbox-exec", Args: c.sandboxArgs(nodns, dnsCmd),
		Dir: c.w, Env: []string{"HOME=" + c.home, "PATH=" + wallPath, "TMPDIR=" + c.ntmp},
	})
	switch code := strings.TrimRight(res.Stdout, "\n"); code {
	case "000", "":
		c.ok("dns_resolves_control")
	default:
		c.fail("dns_resolves_control", fmt.Sprintf("http_code=%s WITHOUT the socket: DNS is reaching the resolver some other way", code))
	}
}
