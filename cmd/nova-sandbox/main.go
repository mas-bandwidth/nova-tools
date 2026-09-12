// nova-sandbox runs ONE command with its filesystem reach cut down by the operating
// system: the OS and toolchain roots and every --read directory are readable, every
// --write directory is readable and writable, and everything else on disk is denied to
// it by the kernel. docs/SPEC-SANDBOX.md is normative and this binary is the darwin half
// of it; on every other platform the wrap REFUSES, because a sandbox that silently does
// nothing is the failure the tool exists to close.
//
// Exit codes are env(1)'s, not SPEC.md's 0/1/2, and the reason is in the spec: the exec
// path's status belongs to the wrapped command. 0-124 the command's own, 125 the tool
// said NO before it ran, 126 it could not be executed, 127 it was on no PATH entry,
// 128+N killed by signal N. The probe, check and version verbs are not wrappers and use
// SPEC.md's grammar unchanged.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// readRemedy is the one sentence that must live in the banner rather than in a NOTE: on
// linux the tool has exec'd itself away by the time the command dies, so a remedy printed
// after the fact is a promise one platform can keep and the others cannot.
const readRemedy = "A command that runs OUTSIDE the wall and dies inside it is missing a --read"

const usage = `nova-sandbox: one command, contained by the OS (see docs/SPEC-SANDBOX.md)

usage:
  nova-sandbox --read <dir>... --write <dir>... [--net-deny] [--net-listen] [--cwd <dir>]
               [--tmp <dir>] [--name <container>] -- <command> <args...>
  nova-sandbox probe --write <dir>... [--read <dir>...] --secret <path> [--net-deny]
  nova-sandbox policy --read <dir>... --write <dir>... [--net-deny] [--net-listen]
  nova-sandbox check [--max <n>]
  nova-sandbox version
  nova-sandbox help

  --read <dir>    readable, recursively, and NOT writable. Repeatable, no default.
                  Shared inputs go here, named once, so N workers read one copy.
  --write <dir>   readable AND writable, recursively. Repeatable, no default, and
                  REQUIRED: a command with no writable directory is a
                  misconfiguration, not a tighter sandbox. The FIRST --write is
                  where the working directory and the temp directory default to.
  --cwd <dir>     the command's working directory; must be inside a --write.
                  Default: the first --write. A cwd outside the wall denies
                  getcwd(3) and every git command dies before it reads anything.
  --tmp <dir>     TMPDIR/TMP/TEMP for the child; must be inside a --write.
                  Default: <first --write>/.nova-sandbox-tmp, the one directory
                  this tool creates.
  --net-deny      an ENFORCED network denial, or a refusal. Without it the tool
                  makes no promise about the network and the line says
                  net=nopromise.
  --net-listen    grant INBOUND ip as well; without it a job that does not
                  listen cannot be listened to. Never with --net-deny.
  --name <c>      the windows container name. Accepted and ignored on darwin, so
                  one caller builds one argv for three platforms.
  --secret <path> probe only: the file a probe proves it cannot read. A path is
                  not a secret; the file's contents are never read.
  --max <n>       how many lines a listing prints before one MORE line stands for
                  the rest. Default 20, and 0 means all.

Every path is yours and none is guessed: a --read, a --write, a --cwd or a --tmp
that does not exist is a refusal and is NOT created. HOME must resolve inside a
--write (the caller sets it), because almost every tool derives a path from it
and an inherited HOME is denied by the wall.

` + readRemedy + `:
a toolchain in a user directory is exactly a caller-supplied read-only root.

example:
  nova-sandbox --read /Users/me/pool/ref/nova-tools@abc123 \
               --write /Users/me/pool/jobs/j1 \
               -- /opt/homebrew/bin/git -C /Users/me/pool/jobs/j1/repo status

  nova-sandbox probe --write /Users/me/pool/jobs/j1 \
               --secret /Users/me/.config/anthropic/env
`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ())) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	if len(args) == 0 {
		// ONBOARDING.md point 2: the banner is behind `help`, not in front of every
		// mistake. No arguments is "could not run", which is the 2 of SPEC.md's
		// grammar and not the 125 of a wrap that refused — nothing was wrapped.
		fmt.Fprint(stderr, "nova-sandbox: no arguments; every path is yours and none is guessed, so a run names at least one --write and a command after --; run: nova-sandbox help\n")
		return sandbox.ExitCannotRun
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "SANDBOX VERSION tool=nova-sandbox version=%s backend=%s platform=%s\n",
			oneline.Field(buildVersion()), oneline.Field(sandbox.Backend), oneline.Field(runtime.GOOS))
		return 0
	case "check":
		return checkVerb(stdout)
	case "policy":
		return policyVerb(args[1:], stdout, stderr, env)
	case "probe":
		return probeVerb(args[1:], stdout, stderr, env)
	}
	return execVerb(args, stdin, stdout, stderr, env)
}

// flags is the argv before --, parsed by hand because every list flag is repeatable and
// because the split at -- must be exact: everything after it is the command, verbatim.
type flags struct {
	reads, writes          []string
	cwd, tmp, name, secret string
	netDeny, netListen     bool
	max                    int
	maxSet                 bool
	argv                   []string
	sawDashDash            bool
	bad                    []sandbox.Refusal
}

func parse(args []string) flags {
	f := flags{max: 20}
	want := func(i int, flag string) (string, int) {
		if i+1 >= len(args) {
			f.bad = append(f.bad, sandbox.Refusal{Reason: "no_command", Text: flag + " wants a value: " + flag + " <dir>"})
			return "", i
		}
		return args[i+1], i + 1
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			f.sawDashDash = true
			f.argv = append(f.argv, args[i+1:]...)
			return f
		}
		var v string
		switch a {
		case "--read":
			v, i = want(i, "--read")
			f.reads = append(f.reads, v)
		case "--write":
			v, i = want(i, "--write")
			f.writes = append(f.writes, v)
		case "--cwd":
			f.cwd, i = want(i, "--cwd")
		case "--tmp":
			f.tmp, i = want(i, "--tmp")
		case "--name":
			f.name, i = want(i, "--name")
		case "--secret":
			f.secret, i = want(i, "--secret")
		case "--net-deny":
			f.netDeny = true
		case "--net-listen":
			f.netListen = true
		case "--max":
			v, i = want(i, "--max")
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 0 {
				f.bad = append(f.bad, sandbox.Refusal{Reason: "no_command", Text: "--max wants a whole number, 0 for all: --max <n>"})
			}
			f.max, f.maxSet = n, true
		default:
			f.bad = append(f.bad, sandbox.Refusal{Reason: "no_command",
				Text: oneline.Escape(a) + " is not a flag this tool has; run: nova-sandbox help"})
		}
	}
	return f
}

// refuseAll prints one SANDBOX REFUSED line per independent problem — rule 16 asks for
// every problem at once, and one line per problem is how a scanner reads them — and
// returns 125 unless EVERY problem is not_found, in which case it returns 127. Returning
// the first problem's code made the status depend on the order the flags were typed: two
// problems, one of them a missing command, exited 127 or 125 by argv order. 125 is the
// tool's own refusal and it is the answer whenever anything but "the command is not
// there" is among the reasons.
func refuseAll(stderr io.Writer, bad []sandbox.Refusal) int {
	allNotFound := true
	for _, r := range bad {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n", oneline.Field(r.Reason), oneline.Escape(r.Text))
		if r.Reason != "not_found" {
			allNotFound = false
		}
	}
	if allNotFound {
		return sandbox.ExitNotFound
	}
	return sandbox.ExitRefused
}

func homeOf(env []string) string {
	for _, kv := range env {
		if name, value, _ := strings.Cut(kv, "="); name == "HOME" {
			return value
		}
	}
	return ""
}

// execVerb is the bare form, which is the form the spec fixes: the flags, then --, then
// the command. There is no exec keyword, because a keyword before flags would be a
// second spelling of the same thing.
func execVerb(args []string, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	f := parse(args)
	if !f.sawDashDash {
		f.bad = append(f.bad, sandbox.Refusal{Reason: "no_command",
			Text: "no --; the command comes after it: nova-sandbox --write <dir> -- <command> <args...>"})
	}
	if len(f.bad) > 0 {
		return refuseAll(stderr, f.bad)
	}
	p, bad := sandbox.Build(sandbox.Input{
		Reads: f.reads, Writes: f.writes, Cwd: f.cwd, Tmp: f.tmp, Name: f.name,
		NetDeny: f.netDeny, NetListen: f.netListen, Argv: f.argv, Home: homeOf(env),
	})
	if len(bad) > 0 {
		return refuseAll(stderr, bad)
	}
	childEnv := sandbox.ChildEnv(env, p.Tmp)
	okLine := func() {
		// Every NOTE is printed BEFORE the command starts, and there is no note about a
		// failure the command suffered inside the wall, on any platform.
		if dropped := sandbox.DroppedEnv(env); len(dropped) > 0 {
			fmt.Fprintf(stderr, "SANDBOX NOTE dropped from the child's environment: %s; an agent socket speaks for a key the wall denies\n",
				oneline.Escape(strings.Join(dropped, " ")))
		}
		fmt.Fprintf(stderr, "SANDBOX OK backend=%s abi=%s read=%d write=%d net=%s cwd=%s cmd=%s\n",
			oneline.Field(sandbox.Backend), oneline.Field(sandbox.ABI), len(p.Reads), len(p.Writes),
			oneline.Field(p.Net()), oneline.Field(p.Cwd), oneline.Field(p.CmdName()))
		if flusher, ok := stderr.(interface{ Sync() error }); ok {
			_ = flusher.Sync()
		}
	}
	code, err := sandbox.Run(p, childEnv, stdin, stdout, stderr, okLine)
	if err != nil {
		var r sandbox.Refusal
		if ok := asRefusal(err, &r); ok {
			return refuseAll(stderr, []sandbox.Refusal{r})
		}
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=sandbox_failed: %s\n", oneline.Err(err))
		return sandbox.ExitRefused
	}
	return code
}

func asRefusal(err error, out *sandbox.Refusal) bool {
	if r, ok := err.(sandbox.Refusal); ok {
		*out = r
		return true
	}
	return false
}

// checkVerb reports what this machine can enforce and exits 0 either way, because it is
// a question, not an attempt.
func checkVerb(stdout io.Writer) int {
	backend, ok := sandbox.Available()
	name, note := sandbox.Backend, sandbox.Note()
	net := "unenforceable"
	if !ok {
		name = "none"
	} else if sandbox.NetEnforceable() {
		net = "enforceable"
		note = note + "; backend at " + backend
	}
	fmt.Fprintf(stdout, "CHECK OK backend=%s abi=%s net=%s note=%s\n",
		oneline.Field(name), oneline.Field(sandbox.ABI), oneline.Field(net), oneline.Escape(note))
	return 0
}

// probeVerb is rule 10: five checks under the REAL policy for this platform, run once
// before the first task. A wall that denies the work too is broken, and a two-check
// probe would call it a pass.
func probeVerb(args []string, stdout, stderr io.Writer, env []string) int {
	f := parse(args)
	if len(f.bad) > 0 {
		for _, r := range f.bad {
			fmt.Fprintf(stderr, "PROBE REFUSED reason=check: %s\n", oneline.Escape(r.Text))
		}
		return sandbox.ExitCannotRun
	}
	if f.secret == "" {
		fmt.Fprint(stderr, "PROBE REFUSED reason=check: --secret is required and names the file this probe proves it cannot read: --secret <path>\n")
		return sandbox.ExitCannotRun
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=check: sh is on no PATH entry: %s\n", oneline.Err(err))
		return sandbox.ExitCannotRun
	}
	secret, err := filepath.Abs(f.secret)
	if err != nil {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=check: --secret %s could not be made absolute\n", oneline.Escape(f.secret))
		return sandbox.ExitCannotRun
	}

	p, bad := sandbox.Build(sandbox.Input{
		Reads: f.reads, Writes: f.writes, NetDeny: f.netDeny, NetListen: f.netListen,
		Argv: []string{shell, "-c", "true"}, Home: homeOf(env),
	})
	if len(bad) > 0 {
		for _, r := range bad {
			fmt.Fprintf(stderr, "PROBE REFUSED reason=check: %s\n", oneline.Escape(r.Text))
		}
		return sandbox.ExitCannotRun
	}
	// rule 6: a --secret inside a named path is a misconfiguration, not a failed probe.
	if resolved, err := filepath.EvalSymlinks(secret); err == nil {
		secret = resolved
	}
	for _, d := range append(append([]string{}, p.Reads...), p.Writes...) {
		if sandbox.Inside(secret, d) {
			fmt.Fprintf(stderr, "PROBE REFUSED reason=secret_inside_allow: --secret %s is inside %s; the secret is never inside either list\n",
				oneline.Escape(secret), oneline.Escape(d))
			return sandbox.ExitCannotRun
		}
	}

	// rule 10: the outside path is NAMED, never os.TempDir(), because rule 8 points
	// TMPDIR inside the wall and a probe built on it would fail on a working wall.
	outside := filepath.Join(filepath.Dir(p.Writes[0]), fmt.Sprintf(".nova-sandbox-probe-%d", os.Getpid()))
	for _, d := range append(append([]string{}, p.Reads...), p.Writes...) {
		if sandbox.Inside(outside, d) {
			fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_outside_inside: %s is inside %s; a probe that cannot find an outside cannot answer the question\n",
				oneline.Escape(outside), oneline.Escape(d))
			return sandbox.ExitCannotRun
		}
	}

	type step struct{ name, path, expect string }
	steps := []step{
		{"write_outside_control", outside, "allow"},
		{"write_outside", outside, "deny"},
		{"read_secret", secret, "deny"},
		{"write_inside", filepath.Join(p.Writes[0], ".nova-sandbox-probe-inside"), "allow"},
		{"read_root", p.Command, "allow"},
	}
	passed, failed := 0, 0
	for _, s := range steps {
		var got string
		switch s.name {
		case "write_outside_control":
			// The control runs OUTSIDE the wall, first: a denial that was never possible
			// is not a wall. If this one says deny, the machine cannot answer the
			// question and the probe is exit 2, not a failed check.
			if err := os.WriteFile(s.path, []byte("nova"), 0o600); err != nil {
				fmt.Fprintf(stdout, "PROBE STEP name=%s expect=allow got=deny path=%s\n", oneline.Field(s.name), oneline.Escape(s.path))
				fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_outside_unwritable: %s is not writable by this user anyway, so a deny there proves nothing\n", oneline.Escape(s.path))
				return sandbox.ExitCannotRun
			}
			_ = os.Remove(s.path)
			got = "allow"
		default:
			got = walled(p, env, s.name, s.path)
		}
		fmt.Fprintf(stdout, "PROBE STEP name=%s expect=%s got=%s path=%s\n",
			oneline.Field(s.name), oneline.Field(s.expect), oneline.Field(got), oneline.Escape(s.path))
		if got == s.expect {
			passed++
			continue
		}
		failed++
		fmt.Fprintf(stderr, "PROBE REFUSED reason=check: %s expected %s and got %s at %s\n",
			oneline.Field(s.name), s.expect, got, oneline.Escape(s.path))
	}
	if failed > 0 {
		return sandbox.ExitProbeFailed
	}
	fmt.Fprintf(stdout, "PROBE OK backend=%s abi=%s steps=%d passed=%d net=%s\n",
		oneline.Field(sandbox.Backend), oneline.Field(sandbox.ABI), len(steps), passed, oneline.Field(p.Net()))
	return 0
}

// walled runs one probe check INSIDE the wall and reports allow or deny. The secret's
// contents are never read: the check opens the file and closes it.
func walled(p *sandbox.Policy, env []string, name, path string) string {
	script := ""
	switch name {
	case "write_outside":
		script = ": > '" + path + "'"
	case "read_secret":
		script = "exec 3< '" + path + "'" // open only; nothing is read
	case "write_inside":
		script = ": > '" + path + "' && rm -f '" + path + "'"
	case "read_root":
		script = "dd if='" + path + "' of=/dev/null bs=1 count=1 2>/dev/null"
	}
	run := *p
	run.Argv = []string{p.Command, "-c", script}
	code, err := sandbox.Run(&run, sandbox.ChildEnv(env, p.Tmp), nil, io.Discard, io.Discard, nil)
	if err != nil || code != 0 {
		return "deny"
	}
	return "allow"
}

// policyVerb prints the generated policy for a read/write pair and runs NOTHING. It is
// how a reader checks the wall without trusting the document — and it is how
// profiles/darwin-check.sh can be run against the profile THIS TOOL generates, so that
// the script and the tool cannot drift apart (rule 15: generated, never hand-edited).
func policyVerb(args []string, stdout, stderr io.Writer, env []string) int {
	f := parse(args)
	if len(f.bad) > 0 {
		for _, r := range f.bad {
			fmt.Fprintf(stderr, "POLICY REFUSED reason=%s: %s\n", oneline.Field(r.Reason), oneline.Escape(r.Text))
		}
		return sandbox.ExitCannotRun
	}
	// The policy is about the two lists, so the command is only what rule 5 resolves a
	// root from: /bin/sh is the floor every wrapped shell command already stands on.
	shell, err := exec.LookPath("sh")
	if err != nil {
		fmt.Fprintf(stderr, "POLICY REFUSED reason=bad_read: sh is on no PATH entry: %s\n", oneline.Err(err))
		return sandbox.ExitCannotRun
	}
	p, bad := sandbox.Build(sandbox.Input{
		Reads: f.reads, Writes: f.writes, Cwd: f.cwd, Tmp: f.tmp, Name: f.name,
		NetDeny: f.netDeny, NetListen: f.netListen, Argv: []string{shell, "-c", "true"}, Home: homeOf(env),
	})
	if len(bad) > 0 {
		for _, r := range bad {
			fmt.Fprintf(stderr, "POLICY REFUSED reason=%s: %s\n", oneline.Field(r.Reason), oneline.Escape(r.Text))
		}
		return sandbox.ExitCannotRun
	}
	text, _, err := sandbox.DarwinProfile(p)
	if err != nil {
		fmt.Fprintf(stderr, "POLICY REFUSED reason=bad_write: %s\n", oneline.Err(err))
		return sandbox.ExitCannotRun
	}
	fmt.Fprint(stdout, text)
	fmt.Fprintf(stderr, "POLICY OK backend=%s read=%d write=%d bytes=%d\n",
		oneline.Field(sandbox.Backend), len(p.Reads), len(p.Writes), len(text))
	return 0
}
