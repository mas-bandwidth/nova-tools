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
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
               [--tmp <dir>] [--name <container>] [--acl tool|caller] -- <command> <args...>
  nova-sandbox probe --write <dir>... [--read <dir>...] --secret <path> [--net-deny]
  nova-sandbox policy --read <dir>... --write <dir>... [--net-deny] [--net-listen]
               [-- <command> <args...>]
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
  --acl <t|c>     who adds the windows ACEs. Accepted and ignored on darwin,
                  with one NOTE line, for the same reason as --name.
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
	case probeStepVerbName:
		return probeStepVerb(args[1:], stderr, env)
	}
	return execVerb(args, stdin, stdout, stderr, env)
}

// flags is the argv before --, parsed by hand because every list flag is repeatable and
// because the split at -- must be exact: everything after it is the command, verbatim.
type flags struct {
	reads, writes               []string
	cwd, tmp, name, secret, acl string
	netDeny, netListen          bool
	max                         int
	maxSet                      bool
	argv                        []string
	sawDashDash                 bool
	bad                         []sandbox.Refusal
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
		case "--acl":
			// Rule 22's flag, and it is the WINDOWS body's. The spec's verb table has it
			// on the bare form for all three platforms and says --name and --acl are
			// "accepted and ignored" on darwin and linux, "so one caller has one script
			// for three platforms" — a caller that builds one argv and gets
			// SANDBOX REFUSED here has exactly the problem that sentence exists to
			// prevent. The VALUE is still checked, because an ignored flag with a
			// misspelt value would be a windows refusal nobody saw on a Mac.
			v, i = want(i, "--acl")
			if v != "" && v != "tool" && v != "caller" {
				f.bad = append(f.bad, sandbox.Refusal{Reason: "no_command",
					Text: "--acl wants tool or caller and got " + oneline.Escape(v) + ": --acl <tool|caller>"})
			}
			f.acl = v
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
	// Rule 16: "a refusal names the flag and the form it wants", and a flag that belongs
	// to another verb was accepted here and then ignored — the opposite. --secret is
	// probe's and --max is probe's and check's; the spec's verb table has neither on the
	// bare form.
	f.bad = append(f.bad, notForThisVerb("the bare form", map[string]bool{"--secret": f.secret != "", "--max": f.maxSet},
		map[string]string{"--secret": "probe", "--max": "probe and check"})...)
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
		if f.acl != "" {
			fmt.Fprintf(stderr, "SANDBOX NOTE --acl %s is accepted and ignored on %s; the ACEs of rule 22 are the windows body's\n",
				oneline.Field(f.acl), oneline.Field(runtime.GOOS))
		}
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

// notForThisVerb turns "this flag is another verb's" into one refusal per flag, named and
// in a fixed order so that two problems print the same way twice.
func notForThisVerb(verb string, given map[string]bool, owner map[string]string) []sandbox.Refusal {
	var out []sandbox.Refusal
	for _, flag := range []string{"--secret", "--max", "--acl", "--name", "--cwd", "--tmp"} {
		if !given[flag] {
			continue
		}
		out = append(out, sandbox.Refusal{Reason: "no_command",
			Text: flag + " is not a flag of " + verb + "; it is " + owner[flag] + "'s: run nova-sandbox help"})
	}
	return out
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
	// Rule 10: the probe re-executes THIS binary under the policy it just generates, with
	// an internal verb, never a shell. os.Executable() is the resolved command of that
	// wrapped run, so its directory is the root "the directory of the resolved command"
	// by construction: read_root then exercises the one root the generator computes at run
	// time, instead of /bin, which is in the profile verbatim. There is no PATH lookup and
	// no machine on which the probe cannot find its own child.
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=check: this binary cannot name its own path: %s\n", oneline.Err(err))
		return sandbox.ExitCannotRun
	}
	// --secret is a caller path like every other, so rule 5 resolves it: absolute,
	// existing, symlinks followed, and refused for absence rather than passing a probe
	// against a file that is not there.
	secret, refusal := sandbox.ResolveCallerFile("--secret", f.secret)
	if refusal != nil {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=%s: %s\n", oneline.Field(refusal.Reason), oneline.Escape(refusal.Text))
		return sandbox.ExitCannotRun
	}

	p, bad := sandbox.Build(sandbox.Input{
		Reads: f.reads, Writes: f.writes, NetDeny: f.netDeny, NetListen: f.netListen,
		Argv: []string{self, probeStepVerbName}, Home: homeOf(env),
	})
	if len(bad) > 0 {
		for _, r := range bad {
			fmt.Fprintf(stderr, "PROBE REFUSED reason=check: %s\n", oneline.Escape(r.Text))
		}
		return sandbox.ExitCannotRun
	}
	// rule 6: a --secret inside a named path is a misconfiguration, not a failed probe.
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

	// Rule 10's child is this binary, and NOTHING ELSE may be: the verb opens, truncates
	// and reads paths it is handed, so a caller who types it by hand truncates a file with
	// no wall around it (measured at 1922f9d: `nova-sandbox probe-step write_outside
	// <path>` emptied an ordinary file from an ordinary shell, exit 0).
	//
	// The first form of this guard put the one 128-bit value in the argv AND in the
	// environment and had the child compare the two. Both halves are the CALLER'S to set,
	// so the guard was a check that a caller had agreed with itself — measured on
	// 29646c1: `NOVA_SANDBOX_PROBE_NONCE=<x> nova-sandbox probe-step <x> write_outside
	// <file>` ran by hand, exit 0, the file truncated. The value now travels on an
	// INHERITED PIPE (fd 3), which a caller cannot conjure by typing: the parent mints the
	// value, writes the 16 raw bytes into the pipe, closes its end, and the child must read
	// exactly those bytes from fd 3 and find them equal to the argv copy. The argv copy
	// stays so that a mismatch still refuses; the environment copy stays because
	// darwin-check.sh and the step bodies read it, and it is stripped from anything the
	// child in turn starts. The child then asks the OS who its parent is and refuses
	// unless that process is this same binary.
	rawNonce, err := probeNonce()
	if err != nil {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=check: this machine has no random source for the probe's one-time value: %s\n", oneline.Err(err))
		return sandbox.ExitCannotRun
	}
	nonce := hex.EncodeToString(rawNonce[:])

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
			got = walled(p, env, rawNonce, nonce, s.name, s.path)
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

// walled runs one probe check INSIDE the wall and reports allow or deny. The child is
// this same binary with the internal verb (rule 10), so the step name and the path are
// argv ELEMENTS: nothing the caller handed the tool is ever re-parsed by an interpreter,
// which is rule 12's "never through a shell" applied to the tool's own child. The
// previous form built a shell script by concatenation, and a --secret holding a quote and
// a semicolon ran a command inside the wall and flipped read_secret to allow.
func walled(p *sandbox.Policy, env []string, raw [probeNonceLen]byte, nonce, name, path string) string {
	run := *p
	run.Argv = []string{p.Command, probeStepVerbName, nonce, name, path}
	// fd 3: the half of the guard a caller cannot type. The read end is handed to the
	// child (Policy.Extra -> cmd.ExtraFiles, so it IS fd 3 there), the parent writes the
	// raw value and closes its end at once — 16 bytes never fill a pipe buffer, so this
	// cannot block, and the close gives the child an EOF after exactly those bytes.
	pr, pw, err := os.Pipe()
	if err != nil {
		return "deny"
	}
	defer pr.Close()
	if _, err := pw.Write(raw[:]); err != nil {
		pw.Close()
		return "deny"
	}
	if err := pw.Close(); err != nil {
		return "deny"
	}
	run.Extra = []*os.File{pr}
	// An inherited NOVA_SANDBOX_PROBE_NONCE would be the value os.Getenv returns in the
	// child (Go keeps the FIRST of a duplicated name), so it is dropped before ours is
	// appended: the guard must answer to this probe and no earlier one.
	childEnv := make([]string, 0, len(env)+4)
	for _, kv := range sandbox.ChildEnv(env, p.Tmp) {
		if name, _, _ := strings.Cut(kv, "="); name == probeNonceVar {
			continue
		}
		childEnv = append(childEnv, kv)
	}
	childEnv = append(childEnv, probeNonceVar+"="+nonce)
	code, err := sandbox.Run(&run, childEnv, nil, io.Discard, io.Discard, nil)
	if err != nil || code != 0 {
		return "deny"
	}
	return "allow"
}

// probeStepVerbName is the internal verb rule 10 names. It is not in the usage banner and
// no caller runs it: it is the child of every walled probe step, and it exists so that the
// probe's child is the tool itself rather than a shell — the smaller surface, and the only
// shape under which read_root reads the probe's own executable.
const probeStepVerbName = "probe-step"

// probeNonceVar carries the same one-time value the child gets as its first argv element.
// It is set by the parent on the child's environment only, never on a wrapped command's.
const probeNonceVar = "NOVA_SANDBOX_PROBE_NONCE"

// probeNonceLen is the width of that value in bytes: 128 bits, and the exact number of
// bytes the child reads from fd 3. It is a constant on both sides so that "short" is a
// thing the child can tell from "enough".
const probeNonceLen = 16

// probeNonceFD is the descriptor the parent hands the child the value on. 0, 1 and 2 are
// the command's own, so the first one above them is the first the parent can choose.
const probeNonceFD = 3

// probeNonce is 128 bits from the OS, per probe, held only in the parent's memory.
func probeNonce() ([probeNonceLen]byte, error) {
	var b [probeNonceLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		return b, err
	}
	return b, nil
}

// notTheProbesChild is the whole of the internal verb's safety, and it returns the empty
// string only for a process the probe itself started. It answers in three parts, and each
// one is a thing a caller typing a command line cannot supply:
//
//  1. fd 3 is an inherited PIPE carrying exactly probeNonceLen bytes. The parent writes
//     them and closes; anything else — no fd 3, an fd 3 that is a file or a terminal, a
//     short read, or more bytes than were promised — is a refusal. This is the part that
//     the argv-against-environment form did not have: both of those are the caller's own
//     to set, so that form checked only that a caller agreed with itself (measured on
//     29646c1: `NOVA_SANDBOX_PROBE_NONCE=<x> nova-sandbox probe-step <x> write_outside
//     <file>` truncated the file, exit 0).
//  2. The bytes on the pipe, hex-encoded, equal the argv copy — compared in constant
//     time. The argv copy is kept so that a mismatched pair still refuses.
//  3. The parent process is THIS binary. sandbox-exec execs in place, so the probe's
//     child has the tool for a parent; a child started by anything else does not.
//
// The honest bound on all three is in docs/SPEC-SANDBOX.md's probe section: none of this
// grants a same-user caller anything they lack, because a same-user caller can already
// open and truncate the file themselves. What it buys is that no path DRIVEN BY CONTENT
// — a script, a Makefile, a repository's own hook, a job inside another wall — can reach
// this verb's O_TRUNC by guessing a word, so the spec's "nothing else runs it" is held by
// a mechanism rather than by a sentence.
func notTheProbesChild(nonce string, env []string) string {
	got, err := probeNonceOnFD()
	if err != nil {
		return err.Error()
	}
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(got[:])), []byte(nonce)) != 1 {
		return "the value on fd 3 is not the one in the argv"
	}
	// The environment copy is the one the step bodies and darwin-check.sh read; it is
	// checked too, so that the three copies cannot disagree.
	want := ""
	for _, kv := range env {
		if n, v, _ := strings.Cut(kv, "="); n == probeNonceVar {
			want = v
			break
		}
	}
	if want == "" || subtle.ConstantTimeCompare([]byte(nonce), []byte(want)) != 1 {
		return "the environment does not carry the same one-time value"
	}
	self, err := os.Executable()
	if err != nil {
		return "this process cannot name its own path"
	}
	parent, err := parentExecutable(os.Getppid())
	if err != nil {
		return "the parent process cannot be named"
	}
	if resolve(self) != resolve(parent) {
		return "the parent process is not this binary"
	}
	return ""
}

// resolve is EvalSymlinks with the unresolved path as its own answer: the two paths
// compared above come from different syscalls (os.Executable and the OS's per-pid path),
// and one of them may still be a symlink while the other is not.
func resolve(path string) string {
	if got, err := filepath.EvalSymlinks(path); err == nil {
		return got
	}
	return path
}

// probeStepVerb is that child: one step, done in Go, exit 0 for allow and 1 for deny. Each
// case is the smallest syscall that answers its question, and read_secret opens the file
// and closes it without reading a byte (rule 6).
func probeStepVerb(args []string, stderr io.Writer, env []string) int {
	if len(args) != 3 {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_step_not_a_child: %s is internal and takes <nonce> <name> <path>; it is the child of a probe this binary started and nothing else runs it\n", probeStepVerbName)
		return sandbox.ExitCannotRun
	}
	nonce, name, path := args[0], args[1], args[2]
	// The guard, BEFORE anything is opened. A refusal here opens no file, truncates no
	// file and creates no file — the line is the whole of the answer.
	if r := notTheProbesChild(nonce, env); r != "" {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_step_not_a_child: %s runs only as the child of a probe this binary started, and this invocation is not one (%s); nothing was opened. Run: nova-sandbox probe --write <dir> --secret <path>\n", probeStepVerbName, r)
		return sandbox.ExitCannotRun
	}
	// Rule 5's shape for the one path this verb is handed: absolute, never relative. The
	// parent builds every step path absolute from a resolved directory, so a relative one
	// is not the parent's and would be resolved against a cwd the parent did not choose.
	if !filepath.IsAbs(path) {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_step_not_a_child: %s wants an absolute path and got %s\n", probeStepVerbName, oneline.Escape(path))
		return sandbox.ExitCannotRun
	}
	switch name {
	case "write_outside", "write_inside":
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return sandbox.ExitProbeFailed
		}
		_ = f.Close()
		if name == "write_inside" {
			_ = os.Remove(path)
		}
		return 0
	case "read_secret":
		// Opened and closed. Nothing is read, so the tool never holds a credential's
		// bytes even for the length of one syscall (rule 6).
		f, err := os.Open(path)
		if err != nil {
			return sandbox.ExitProbeFailed
		}
		_ = f.Close()
		return 0
	case "read_root":
		f, err := os.Open(path)
		if err != nil {
			return sandbox.ExitProbeFailed
		}
		var one [1]byte
		n, err := f.Read(one[:])
		_ = f.Close()
		if err != nil || n != 1 {
			return sandbox.ExitProbeFailed
		}
		return 0
	}
	fmt.Fprintf(stderr, "PROBE REFUSED reason=check: %s is not a probe step\n", oneline.Field(name))
	return sandbox.ExitCannotRun
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
	// Rule 15: "policy prints exactly what a wrapped run would apply". One root is
	// computed from the COMMAND — "the directory of the resolved command" — so a policy
	// built around /bin/sh could not show it for any real command, and the one root a
	// reader most needs to see was the one root this verb could not print. The command is
	// therefore optional and comes after -- exactly as it does on the bare form; with no
	// --, /bin/sh is the floor every wrapped shell command already stands on.
	argv := f.argv
	if len(argv) == 0 {
		shell, err := exec.LookPath("sh")
		if err != nil {
			fmt.Fprintf(stderr, "POLICY REFUSED reason=bad_read: sh is on no PATH entry: %s\n", oneline.Err(err))
			return sandbox.ExitCannotRun
		}
		argv = []string{shell, "-c", "true"}
	}
	p, bad := sandbox.Build(sandbox.Input{
		Reads: f.reads, Writes: f.writes, Cwd: f.cwd, Tmp: f.tmp, Name: f.name,
		NetDeny: f.netDeny, NetListen: f.netListen, Argv: argv, Home: homeOf(env),
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
