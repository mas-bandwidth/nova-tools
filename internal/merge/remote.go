package merge

// THE GATE RUNS ON A BENCH; THE FORGE IS ON THIS MACHINE.
//
// 2026-09-18: every landing child that day did the same two-handed dance. The gate has to
// run on a bench -- hulk's 64 cores and the fleet toolchain, because the Studio's cores are
// Glenn's and a test suite that takes twenty minutes there takes three there -- and a bench
// holds NO forge credential and no `gh` at all, by design (secrets are sealed once, in the
// store, and never copied between machines). So `--plan`, `checks=required` and `--land` all
// refuse on a bench, and the child ran the gate over ssh by hand, read the BATCH OK line off
// its terminal, and did the forge half from the Studio.
//
// This file is the ssh half of making that ONE verb. It is deliberately small: a seam with
// two calls on it -- run a script, fetch a file -- and the script itself, built here so that
// the fake bench the tests drive runs THE SAME TEXT a real bench runs. A seam that let the
// test build its own script would prove nothing about the one that ships.
//
// IT HOLDS NO POLICY. Which machine, which root, which steps and which half runs where is
// `nova-merge batch`'s (cmd/nova-merge/batchon.go); what is here is how a command reaches a
// machine and how a file comes back.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Remote is one machine the gate's steps run on. Two calls, because two is what the gate
// needs: run a script and answer what it printed, and bring one file back.
//
// It is an interface so the tests drive a FAKE BENCH -- a thing that answers the same
// scripts on this machine, out of a bare fixture repository -- and no test of this tool
// opens an ssh connection. The unit tests here are about the argv and the script; the whole
// verb is exercised against the fake in cmd/nova-merge.
type Remote interface {
	// Name is the machine's name in the registry, which is what the progress lines carry.
	Name() string
	// Exec runs one shell script on the machine and answers its combined output. A
	// non-zero exit is an error AND the output, because a red step's output is the news.
	Exec(script string, timeout time.Duration) (string, error)
	// Get copies one file off the machine, byte for byte, to a path on this one.
	Get(remotePath, localPath string, timeout time.Duration) error
}

// SSH is the production Remote: one ssh per call, no control master, no agent of our own.
//
// BatchMode=yes is the whole of the credential story: ssh may not ask for a passphrase or a
// password, so a machine this caller cannot reach non-interactively is a refusal in seconds
// rather than a verb hanging on a prompt nobody is watching. ConnectTimeout bounds the
// connect itself, and --timeout bounds everything after it.
type SSH struct {
	Machine string // the registry name, for the progress lines
	Target  string // the registry's ssh column: an alias in ~/.ssh/config or user@host
}

// NewSSH is the Remote for one registry row.
func NewSSH(machine, target string) SSH { return SSH{Machine: machine, Target: target} }

// Name is the registry name.
func (s SSH) Name() string { return s.Machine }

// sshConnectTimeout is how long the CONNECT may take, in seconds, as ssh spells it. It is
// not the step's deadline -- that is --timeout, which bounds the whole call -- it is the
// answer to "is this machine there at all", and a machine that is asleep says so in twenty
// seconds instead of in thirty minutes.
const sshConnectTimeout = "20"

// SSHArgv is the whole command line one remote step runs under. It is a function rather
// than an inline argv so that the shape is pinned by a test: an option that went missing
// here is a verb that hangs on a password prompt or trusts a host it has never seen.
//
// `--` ends the options, so a target beginning with a dash is a target and not a flag
// (lesson 48, the same reason every git call in this package carries one).
func SSHArgv(target, script string) []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + sshConnectTimeout,
		"--", target, script,
	}
}

// SSHGetArgv is how a file comes back: `cat` on the machine, its bytes on our stdout. It is
// cat rather than scp or rsync because the seam is already one ssh, because the file is one
// bundle, and because a second tool is a second thing to be missing on a bench.
func SSHGetArgv(target, remotePath string) []string {
	return SSHArgv(target, "cat -- "+RemoteQuote(remotePath))
}

// Exec runs one script on the machine.
//
// THE OUTPUT IS NOT CAPPED HERE, unlike Exec's git calls: the step this carries is `go test
// -json ./...` over the whole tree, which is tens of megabytes on a good day, and the verdict
// line is built by reading that stream for the packages and tests that failed. A capped
// capture would turn a red run into a run whose failure nobody can name.
func (s SSH) Exec(script string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", SSHArgv(s.Target, script)...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), fmt.Errorf("%s did not answer within the %s --timeout", s.Machine, timeout)
	}
	return out.String(), err
}

// Get brings one file back. The local path is the caller's, already under a root they named.
func (s SSH) Get(remotePath, localPath string, timeout time.Duration) error {
	if err := ValidRemotePath(remotePath); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.CommandContext(ctx, "ssh", SSHGetArgv(s.Target, remotePath)...)
	var errs strings.Builder
	cmd.Stdout = f
	cmd.Stderr = &errs
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s did not send %s within the %s --timeout", s.Machine, remotePath, timeout)
		}
		return fmt.Errorf("%s could not be read off %s: %w: %s", remotePath, s.Machine, err, strings.TrimSpace(errs.String()))
	}
	return f.Close()
}

// RemotePrelude is the environment every step on a bench runs in, and it is the environment
// the LOCAL gate builds in Go (cmd/nova-merge's ciTestEnv), written as the shell:
//
//	PATH   the machine's own toolchain first. A bench's Go and sbcl live in a USER
//	       directory -- `~/sdk/go1.26.5/bin`, `~/sdk/sbcl-2.5.8-x86-64-linux/bin` -- which
//	       is the bench provisioning standard's shape and the same list the sandbox wall
//	       grants a card (internal/swarm's toolchain roots, #1364/#1419: `sdk` is the one
//	       home root carrying EXECUTE). A non-interactive ssh gets whatever the machine's
//	       rc file happens to export, and a gate that ran against the distribution's
//	       go1.22 because a login shell was not involved is a gate that refuses a tree CI
//	       builds. REVERSE SORTED, so go1.26.5 wins over a go1.22 left beside it -- the
//	       same "newest match by name" the local gate's lookInSDK uses.
//
//	       A Mac bench's toolchains are INSTALLED and already on PATH, and its ~/sdk is
//	       optional, so one prelude serves both operating systems: a directory that is not
//	       there adds nothing.
//
//	unset  GOFLAGS and the GOTEST* family, which is internal/goenv's Clean carried across
//	       the seam. CI's own `make test` exports GOFLAGS=-json, and a step that inherited
//	       it would hand this verb's parser a stream of a shape it did not ask for -- the
//	       bug that made a green nova-review run read as red on three legs of
//	       integration-4. The gate asks for -json itself, on the argv, where it belongs.
const RemotePrelude = `PATH="$(ls -d "$HOME"/sdk/*/bin 2>/dev/null | sort -r | tr '\n' ':')$PATH"; export PATH; unset GOFLAGS GOTESTFLAGS GOTESTSUM_FORMAT; `

// RemoteScript is the one script a step runs on the machine: the prelude, the caller's own
// variables, a cd into the directory, and the command.
//
// The command is this tool's own literal (the gate's step table) and every path is quoted,
// so nothing a caller typed reaches the machine's shell unquoted. env is "NAME=value", and
// the value is quoted the same way -- which is how the batch's private temp directory and
// CI's fair share of the cores get there.
func RemoteScript(dir string, env []string, command string) string {
	var b strings.Builder
	b.WriteString(RemotePrelude)
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		b.WriteString(name + "=" + RemoteQuote(value) + "; export " + name + "; ")
	}
	b.WriteString("cd " + RemoteQuote(dir) + " && " + command)
	return b.String()
}

// RemoteQuote is one value as the remote shell must read it: single quotes, which take
// every character literally, with a `~/` prefix left OUTSIDE them so the shell still
// expands the home directory.
//
// The tilde is the whole reason this is not strconv.Quote. A bench root is written
// `~/nova-bench/integration/<name>`, and the home it names is the BENCH's -- /home/gaffer,
// not the /Users/glenn this process would expand it to. So the tilde crosses the seam
// unexpanded and the machine's own shell answers it, which is exactly what every landing
// child typed by hand.
func RemoteQuote(s string) string {
	if rest, ok := strings.CutPrefix(s, "~/"); ok {
		return `"$HOME"/` + singleQuote(rest)
	}
	return singleQuote(s)
}

// singleQuote wraps s in single quotes, ending and restarting the quoting around any single
// quote inside it -- the one escape a POSIX shell has for this.
func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ValidRemotePath is the guard on every path that crosses the seam, and it is strict on
// purpose: the gate REMOVES its own working directory on the machine before it rebuilds it
// (`rm -rf <root>/<name>`), and Glenn, 2026-09-17, on exactly that: "it is just one mistake
// away from deleting the whole disk".
//
// A path here is absolute or `~/`-rooted, has at least two elements under that root, carries
// no `..` and no empty element, and is made of letters, digits, dot, dash, underscore and
// slash. That is narrower than what a filesystem allows and it is meant to be: nothing about
// a bench's integration root needs a space, a dollar or a quote in it, and a path that
// cannot be a word cannot be a command either.
func ValidRemotePath(p string) error {
	s := strings.TrimSpace(p)
	if s == "" {
		return fmt.Errorf("a path on the machine is required and this one is empty")
	}
	rest, tilde := strings.CutPrefix(s, "~/")
	if !tilde {
		var ok bool
		rest, ok = strings.CutPrefix(s, "/")
		if !ok {
			return fmt.Errorf("%s is neither absolute nor under ~/; a path on another machine is never relative to a directory this one happens to be in", p)
		}
	}
	elems := strings.Split(rest, "/")
	if len(elems) < 2 {
		return fmt.Errorf("%s names a directory directly under the root of the machine or of its home; the gate REMOVES its own working directory before it rebuilds it, so it will not be handed a path that shallow", p)
	}
	for _, e := range elems {
		if e == "" {
			return fmt.Errorf("%s holds an empty path element, which two readings of one path is one too many", p)
		}
		if e == "." || e == ".." {
			return fmt.Errorf("%s holds a %s element; a path that can climb is a path that can climb out of the root somebody named", p, e)
		}
		if !remoteElementOK(e) {
			return fmt.Errorf("%s holds the element %q, and a path crossing to another machine is letters, digits, dot, dash, underscore and slash; anything else is a word the machine's shell would read as something", p, e)
		}
	}
	return nil
}

// remoteElementOK is the one character class a remote path element may hold.
func remoteElementOK(e string) bool {
	for _, r := range e {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == '+':
		default:
			return false
		}
	}
	return true
}

// RemoteJoin joins path elements the way the MACHINE spells them -- always with a slash,
// never with this process's own separator. A gate driven from a Windows laptop would
// otherwise send a bench a path with backslashes in it.
func RemoteJoin(elems ...string) string {
	out := make([]string, 0, len(elems))
	for _, e := range elems {
		out = append(out, strings.Trim(e, "/"))
	}
	joined := strings.Join(out, "/")
	if strings.HasPrefix(elems[0], "/") {
		return "/" + joined
	}
	return joined
}
