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
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// readRemedy is the one sentence that must live in the banner rather than in a NOTE: on
// linux the tool has exec'd itself away by the time the command dies, so a remedy printed
// after the fact is a promise one platform can keep and the others cannot.
const readRemedy = "A command that runs OUTSIDE the wall and dies inside it is missing a --read"

const usage = usageHead + usageExits + usageExamples

// usageHead is the banner down to its exit paragraph.
const usageHead = `nova-sandbox: run one command inside an OS-enforced wall around the directories you name

how it works: the wall is built for one run from your flags and kept nowhere:
--read directories are readable, --write directories writable, and the kernel
denies the rest (sandbox-exec on macOS, Landlock on Linux; check says which).
Paths must exist and be absolute, and HOME must sit inside a --write. The
command's own exit status comes back; 125 means the wall refused to start it.
first run: the lines under example:, in order: check the backend, make a scratch
directory, prove the wall with probe, then run a command that writes inside it.

usage:
  nova-sandbox --read <dir>... [--read-noexec <dir>...] --write <dir>... [--net-deny]
               [--net-listen] [--net-allow <host:port>] [--cwd <dir>]
               [--tmp <dir>] [--name <container>] [--acl tool|caller] -- <command> <args...>
  nova-sandbox probe --write <dir>... [--read <dir>...] [--secret <path>] [--net-deny]
  nova-sandbox policy --read <dir>... --write <dir>... [--net-deny] [--net-listen]
               [-- <command> <args...>]
  nova-sandbox check
  nova-sandbox run --name <n> --size <8g> [--timeout <30m>] [--go] [--read <dir>]...
               [--container <disk>] -- <command> <args...>          (darwin)
  nova-sandbox run --help
  nova-sandbox reap [--dry-run]                                   (darwin)
  nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id> [--base <branch>]
  nova-sandbox worktree --repo <dir> --scratch <dir> --prune
  nova-sandbox egress plan  --run <id> --policy <file> --model-host <host>
               --resolver <ip> [--bench-cidr <cidr>]... [--uid <n>] [--veth <if>]
               --out <file>
  nova-sandbox egress apply --plan <file> --run <id>                    (linux)
  nova-sandbox egress check --plan <file>
  nova-sandbox egress drop  --run <id>                                  (linux)
  nova-sandbox version
  nova-sandbox help

  --read <dir>    readable, recursively, and NOT writable. Repeatable, no default.
                  Shared inputs go here, named once, so N workers read one copy.
                  It CARRIES EXECUTE: a program under a --read runs.
  --read-noexec <dir>
                  readable, recursively, and NOT EXECUTABLE and not writable.
                  Repeatable, no default. This is the flag for a cache or a data
                  tree -- a module cache, a node_modules, a downloads directory --
                  that this user can write to: under --read the job could RUN
                  whatever lands there, and under this flag it can only read it.
                  A path in both lists is a refusal, not a merge.
  --write <dir>   readable AND writable, recursively. Repeatable, no default, and
                  REQUIRED: a command with no writable directory is a
                  misconfiguration, not a tighter sandbox. The FIRST --write is
                  where the working directory and the temp directory default to.
  --cwd <dir>     the command's working directory; must be inside a --write.
                  Default: the first --write. A cwd outside the wall denies
                  getcwd(3) and every git command dies before it reads anything.
  --tmp <dir>     TMPDIR/TMP/TEMP and zsh TMPPREFIX for the child; must be inside a --write.
                  Default: <first --write>/.nova-sandbox-tmp, the one directory
                  this tool creates.
  --net-deny      an ENFORCED network denial, or a refusal. Without it the tool
                  makes no promise about the network and the line says
                  net=nopromise.
  --net-listen    grant INBOUND ip as well; without it a job that does not
                  listen cannot be listened to. Never with --net-deny.
  --net-allow <host:port>  open the loopback host:port named, back up, by name;
                  the keyless local provider (ollama) that (remote ip) does not
                  reach. Repeatable.
  --gpu <n|m>     the explicit local GPU capability: none (default) or metal.
                  Opt-in only; metal records intent and never widens
                  mach-lookup or grants blanket device access (#230).
  --name <c>      the windows container name. Accepted and ignored on darwin, so
                  one caller builds one argv for three platforms.
  --acl <t|c>     who adds the windows ACEs. Accepted and ignored on darwin,
                  with one NOTE line, for the same reason as --name.
  --secret <path> probe only: the file a probe proves it cannot read. A path is
                  not a secret; the file's contents are never read. A probe may run
                  WITHOUT one -- a caller whose key is delivered by nova-secrets
                  exec into the environment has no key file, and the probe then
                  proves the wall's other checks (issue #881).

run gives one command a DISPOSABLE place to work and then takes it away: on darwin
an APFS volume of its own in the boot container, quota'd by --size and mounted at
/Volumes/nova-<n>. That volume is the only --write, the command runs in a process
group of its own, and on exit -- normal, error, signal or --timeout -- the group is
killed and the volume is unmounted and DELETED. Nothing of the run survives on the
boot volume, so there is no cleanup step. A delete that fails prints SANDBOX LEAK
with the one command that removes it and exits 3.

  --name <n>      run only: the volume is nova-<n>. Letters, digits, - _ and .
  --size <s>      run only: the volume's quota, e.g. 8g or 64m. REQUIRED: a
                  disposable place with no ceiling can fill the boot disk.
  --timeout <d>   run only: a Go duration after which the whole process group is
                  killed and the volume deleted anyway. Exit 124.
  --go            run only: add the Go toolchain's own roots as --read, GOROOT
                  and GOMODCACHE as go env reports them. A card that builds Go
                  wants this; nova-sandbox run --help says why.
  --container <d> run only: the APFS container to make the volume in. Default:
                  the container the boot volume is in.
  --dry-run       reap only: print what a reap would take and touch NOTHING.

reap clears what a SIGKILL left: a run killed outright has no path out to delete
its volume on, so the volume stays mounted and the command's own children are
reparented to PID 1 still holding it open. reap lists every nova-* volume, kills
what holds each one (SIGTERM, then SIGKILL) and deletes it -- except a volume a
LIVE run owns, which it reports and leaves alone. Exit 0 clean, 3 when anything
remained, so nova-sandbox reap --dry-run is a gate a card can end on.

egress is the card's OUTBOUND wall, and it lives on the BENCH rather than in the
card, because the worker is the adversary: plan resolves the names in the reviewed
allowlist (infra/image/egress.txt) ONCE, pins the addresses and writes an nftables
ruleset that denies everything the card did not name — TCP 443 to the pinned
addresses, UDP 53 to the resolver, and the metadata address, loopback and the other
benches denied outright. apply hands that ruleset to nft, check reads one back and
asserts its invariants, and drop takes the run's table away.

  --run <id>      egress: the run this wall belongs to; the table is nova_egress_<id>
  --policy <f>    egress plan: the allowlist in git. A name reaches a card only by a
                  PR to that file, never by a flag on one run.
  --model-host <h> egress plan: the ONE model host of this run, and it must already
                  be a line in the policy file.
  --resolver <ip> egress plan: the only destination UDP 53 is allowed to.
  --bench-cidr <c> egress plan: another bench, denied. Repeatable.
  --uid <n>       egress plan: the container's uid on the host (meta skuid).
  --veth <if>     egress plan: the container's interface (iifname). A plan needs
                  --uid or --veth: every rule is scoped to the card's own traffic.
  --out <file>    egress plan: where the ruleset is written.
  --plan <file>   egress apply and check: the ruleset to apply or to read back.

Every path is yours and none is guessed: a --read, a --read-noexec, a --write, a
--cwd or a --tmp that does not exist is a refusal and is NOT created. HOME must
resolve inside a --write (the caller sets it), because almost every tool derives
a path from it and an inherited HOME is denied by the wall.

` + readRemedy + `:
a toolchain in a user directory is exactly a caller-supplied read-only root.

`

// usageExits is the banner's exit paragraph, by verb. Each verb's -h quotes its label
// and then that verb's own line from verbDocs, so a probe's codes are never read as a
// wrapped command's.
const exitsLabel = "exit codes: each verb's own, by verb:"

const usageExits = exitsLabel + `
  the bare wrap and run: the command's own status, 0-124, passed through, and
    a SANDBOX DONE ... exit=<n> line on stderr after it ends says the command
    returned it (SANDBOX OK, before it starts, says only that the wall is up);
    125 nova-sandbox refused before the command ran, a usage error included
    (a SANDBOX REFUSED line on stderr says why, and no SANDBOX DONE follows);
    126 the command could not be executed; 127 it is on no PATH entry; 128+N
    it was killed by signal N; run only: 3 a volume was left (SANDBOX LEAK),
    124 its --timeout ended it. A command that itself exits 125-127 (or 71,
    sandbox-exec's own exec failure) is told from the tool by that line.
  probe, policy, check, version, worktree, egress: 0 done, 1 the verb ran and
    said NO (probe, egress), 2 could not run (a usage error). reap: 0 clean, 3
    something remained. <verb> -h gives one verb's codes and flags.
`

// usageExamples closes the banner: the first run, then two worked job lines.
const usageExamples = `
example:
  nova-sandbox check
  mkdir -p /tmp/trial/home
  HOME=/tmp/trial/home nova-sandbox probe --write /tmp/trial
  HOME=/tmp/trial/home nova-sandbox --write /tmp/trial -- /bin/sh -c 'echo inside > /tmp/trial/out'

macOS job examples (replace /path/to with your own paths):
  HOME=/path/to/pool/jobs/j1/home \
  nova-sandbox --read /opt/homebrew --write /path/to/pool/jobs/j1 \
               -- /opt/homebrew/bin/git -C /path/to/pool/jobs/j1/repo status

  mkdir -p /path/to/pool/jobs/j1/home
  HOME=/path/to/pool/jobs/j1/home \
  nova-sandbox probe --write /path/to/pool/jobs/j1 \
               --secret /path/to/.config/anthropic/env
`

// version is empty in every ordinary build and is the one override: a release
// stamps it with -ldflags "-X main.version=<tag>", the shape every tool uses.
var version string

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ())) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env []string) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0,
	// before anything is probed, wrapped or written (the CLI style's rule (b), #4505).
	// Only -h: every other exit of this tool, the bare wrap's 125 included, is
	// unchanged, and nothing after -- is ever read as help.
	defer recoverVerbHelp(stdout, &code)
	if len(args) == 0 {
		// ONBOARDING.md point 2: the banner is behind `help`, not in front of every
		// mistake. No arguments is "could not run", which is the 2 of SPEC.md's
		// grammar and not the 125 of a wrap that refused — nothing was wrapped.
		fmt.Fprint(stderr, "SANDBOX REFUSED reason=no_command: no arguments; every path is yours and none is guessed, so a run names at least one --write and a command after --; run: nova-sandbox help\n")
		return sandbox.ExitCannotRun
	}
	switch args[0] {
	case "help", "--help", "-h":
		// help <verb> for a NAMED verb only: anything else falls through to the bare
		// wrap below, and help must never reach it.
		if args[0] == "help" && len(args) > 1 && helpVerbs[args[1]] {
			return run(append(args[1:], "--help"), stdin, stdout, stderr, env)
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		helpIfAsked(args[1:], "version")
		if len(args) > 1 {
			// An ignored flag is a refusal (docs/CLI-STYLE.md (g)), never a
			// version line over an argument nobody read.
			fmt.Fprintf(stderr, "SANDBOX REFUSED reason=unknown_flag: version takes no flags and no arguments, got %d; run: nova-sandbox version\n", len(args)-1)
			return sandbox.ExitCannotRun
		}
		// The same four tokens every other binary prints, then the two facts a
		// sandbox is judged by as named extras. This line used to be a shape of its
		// own -- `SANDBOX VERSION tool=... version=...` -- and a shape of its own is
		// a shape every reader has to be taught: `nova-version snapshot` could not
		// read it at all (#1297). The backend and the platform are not lost; they
		// are now said in the grammar the whole set shares.
		fmt.Fprintln(stdout, buildinfo.Line("nova-sandbox", version,
			"backend="+sandbox.Backend, "platform="+runtime.GOOS))
		return 0
	case "check":
		helpIfAsked(args[1:], "check")
		return checkVerb(args[1:], stdout, stderr)
	case "run":
		return prodRunSeams().runVerb(args[1:], stdin, stdout, stderr, env)
	case "reap":
		return reapVerb(args[1:], stdout, stderr)
	case "worktree":
		helpIfAsked(args[1:], "worktree")
		return prodWorktreeSeams().worktreeVerb(args[1:], stdout, stderr, env)
	case "egress":
		if len(args) > 1 {
			if egressVerbs[args[1]] {
				helpIfAsked(args[2:], "egress "+args[1])
			}
			helpIfAsked(args[1:2], "egress")
		}
		return prodEgressSeams().egressVerb(args[1:], stderr)
	case "policy":
		helpIfAsked(args[1:], "policy")
		return policyVerb(args[1:], stdout, stderr, env)
	case "probe":
		helpIfAsked(args[1:], "probe")
		return probeVerb(args[1:], stdout, stderr, env)
	case probeStepVerbName:
		return probeStepVerb(args[1:], stderr, env)
	}
	if !strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=unknown_verb: unknown verb %q; available: check, egress, policy, probe, reap, run, version, worktree; run: nova-sandbox help\n", args[0])
		return sandbox.ExitCannotRun
	}
	return execVerb(args, stdin, stdout, stderr, env)
}

// helpVerbs are the verbs `help <verb>` answers for; egressVerbs the four egress
// sub-verbs, the only words after `egress` a help line may name.
var (
	helpVerbs   = map[string]bool{"version": true, "check": true, "run": true, "reap": true, "worktree": true, "egress": true, "policy": true, "probe": true}
	egressVerbs = map[string]bool{"plan": true, "apply": true, "check": true, "drop": true}
)

// flags is the argv before --, parsed by hand because every list flag is repeatable and
// because the split at -- must be exact: everything after it is the command, verbatim.
type flags struct {
	reads, readsNoExec, writes  []string
	netAllow                    []string
	cwd, tmp, name, secret, acl string
	gpu                         string
	netDeny, netListen, json    bool
	max                         int
	maxSet                      bool
	argv                        []string
	sawDashDash                 bool
	bad                         []sandbox.Refusal
}

func parse(args []string) flags { return parseVerb("", args) }

// parseVerb is parse for the named verb: an argument it does not know is refused with that
// verb's help to run (unknownArg).
func parseVerb(verb string, args []string) flags {
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
		case "--read-noexec":
			v, i = want(i, "--read-noexec")
			f.readsNoExec = append(f.readsNoExec, v)
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
		case "--gpu":
			f.gpu, i = want(i, "--gpu")
		case "--net-deny":
			f.netDeny = true
		case "--net-listen":
			f.netListen = true
		case "--json":
			f.json = true
		case "--net-allow":
			v, i = want(i, "--net-allow")
			f.netAllow = append(f.netAllow, v)
		case "--max":
			v, i = want(i, "--max")
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 0 {
				f.bad = append(f.bad, sandbox.Refusal{Reason: "no_command", Text: "--max wants a whole number, 0 for all: --max <n>"})
			}
			f.max, f.maxSet = n, true
		default:
			text, took := unknownArg(args, i, verb)
			reason := "no_command"
			if verb == "" && strings.HasPrefix(text, "unknown flag ") {
				// the bare form names the mistake: the flag, the flags there are, the nearest
				reason, text = "bad_flag", bareFlagText(text)
			}
			f.bad = append(f.bad, sandbox.Refusal{Reason: reason, Text: text})
			i += took
		}
	}
	return f
}

// bareFlags is every flag parseVerb's table takes, in its order: what an
// unknown flag on the bare form is answered with.
var bareFlags = []string{"--read", "--read-noexec", "--write", "--cwd", "--tmp", "--name", "--acl", "--secret", "--gpu",
	"--net-deny", "--net-listen", "--json", "--net-allow", "--max"}

// bareFlagText is unknownArg's `unknown flag --x; run: ...` with the flags
// the bare form takes and the nearest of them between the two.
func bareFlagText(text string) string {
	head, run, _ := strings.Cut(text, "; run: ")
	near := verbflag.Nearest(strings.TrimPrefix(head, "unknown flag "), bareFlags)
	if near != "" {
		near = "; did you mean " + near + "?"
	}
	return head + "; the flags are " + verbflag.List(bareFlags) + near + "; run: " + run
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
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n", oneline.Field(r.Reason), oneline.Escape(oneline.WithRemedy(r.Text, "nova-sandbox help")))
		if r.Reason != "not_found" {
			allNotFound = false
		}
	}
	if allNotFound {
		return sandbox.ExitNotFound
	}
	return sandbox.ExitRefused
}

// homeRemedy gives every home_outside refusal in bad the command that answers it: make a
// data home inside the first --write and run the same invocation with HOME set there.
// HOME is never defaulted (rule 9); the caller pastes the line, and nothing is guessed.
// argv is the invocation after `nova-sandbox`, verb included.
func homeRemedy(bad []sandbox.Refusal, writes, argv []string) {
	if len(writes) == 0 || !filepath.IsAbs(writes[0]) {
		return
	}
	home := shellWord(filepath.Join(writes[0], "home"))
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = shellWord(a)
	}
	for i := range bad {
		if bad[i].Reason == "home_outside" {
			bad[i].Text += "; run: mkdir -p " + home + " && HOME=" + home + " nova-sandbox " + strings.Join(words, " ")
		}
	}
}

// shellWord is s as one POSIX shell word: as it is when it holds nothing a shell reads,
// else single-quoted.
func shellWord(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@,+%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
	f.bad = append(f.bad, notForThisVerb("the bare form", "nova-sandbox help",
		map[string]bool{"--secret": f.secret != "", "--max": f.maxSet, "--json": f.json}, ownerOf)...)
	if len(f.bad) > 0 {
		return refuseAll(stderr, f.bad)
	}
	p, bad := sandbox.Build(sandbox.Input{
		Reads: f.reads, ReadsNoExec: f.readsNoExec, Writes: f.writes, Cwd: f.cwd, Tmp: f.tmp, Name: f.name,
		NetDeny: f.netDeny, NetListen: f.netListen, NetAllow: f.netAllow, Argv: f.argv, Home: homeOf(env),
		GPU: f.gpu,
	})
	if len(bad) > 0 {
		homeRemedy(bad, f.writes, args)
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
		// used=<n> is printed ONLY when the wall was built below the ABI the kernel
		// reports -- the clamp -- so an ordinary machine's line is unchanged. The note
		// carries the sentence, because the field carries a number and a number alone
		// does not say which way it went or what to do about it.
		used := ""
		if n, clamped := sandbox.ClampedABI(); clamped {
			used = " used=" + oneline.Field(strconv.Itoa(n))
			fmt.Fprintf(stderr, "SANDBOX NOTE landlock abi %s is above this tool's table: the wall is built at abi %s (clamped), which this kernel enforces as asked; the rights the newer abi added are not handled until the table grows\n",
				oneline.Field(sandbox.ABI()), oneline.Field(strconv.Itoa(n)))
		}
		// cwd is named TWICE (SPEC-SANDBOX): cwd=<dir> is the readable rendering through
		// oneline.Field for an operator, and cwdb64=<base64url> is the machine-readable
		// receipt of the raw path bytes a reader must decode -- oneline's escape is not
		// injective, so the readable spelling cannot be reversed.
		// read= and read-noexec= are TWO counts because they are two grants: a --read
		// root carries EXECUTE and a --read-noexec root does not, so a log that folded
		// them into one number could not say what a run was allowed to run.
		// deletes= names the --write roots the wall lets the command delete beneath, so a
		// log says where an unlink was allowed and not only how many roots were written.
		fmt.Fprintf(stderr, "SANDBOX OK backend=%s abi=%s%s read=%d read-noexec=%d write=%d net=%s cwd=%s cwdb64=%s ancestors=%d cmd=%s gpu=%s deletes=%s\n",
			oneline.Field(sandbox.Backend), oneline.Field(sandbox.ABI()), used, len(p.Reads), len(p.ReadsNoExec), len(p.Writes),
			oneline.Field(p.Net()), oneline.Field(p.Cwd), base64.RawURLEncoding.EncodeToString([]byte(p.Cwd)), p.AncestorCount(), oneline.Field(p.CmdName()), oneline.Field(string(p.GPUMode)), deletesField(p))
		if flusher, ok := stderr.(interface{ Sync() error }); ok {
			// ignored: a flush of stderr before the exec; a stream that cannot sync has nothing to lose that a later write would not also lose
			_ = flusher.Sync()
		}
	}
	code, err := sandbox.Run(p, childEnv, stdin, stdout, stderr, okLine)
	if err != nil {
		var r sandbox.Refusal
		if ok := asRefusal(err, &r); ok {
			return refuseAll(stderr, []sandbox.Refusal{r})
		}
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=sandbox_failed: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-sandbox run -h"))
		return sandbox.ExitRefused
	}
	// SANDBOX OK above says the wall is up and the command is starting, never how it
	// ended; this line is the ending: the status is the command's own, so a 125 the
	// command returned is told from the tool's refusal, which prints no DONE.
	fmt.Fprintf(stderr, "SANDBOX DONE exit=%d cmd=%s\n", code, oneline.Field(p.CmdName()))
	return code
}

// notForThisVerb turns "this flag is another verb's" into one refusal per flag, named and
// in a fixed order so that two problems print the same way twice. help is the command
// that lists this verb's own flags.
func notForThisVerb(verb, help string, given map[string]bool, owner map[string]string) []sandbox.Refusal {
	var out []sandbox.Refusal
	for _, flag := range []string{"--secret", "--max", "--acl", "--name", "--cwd", "--tmp", "--net-allow", "--json"} {
		if !given[flag] {
			continue
		}
		out = append(out, sandbox.Refusal{Reason: "no_command",
			Text: flag + " is not a flag of " + verb + "; it is " + owner[flag] + "'s: run " + help})
	}
	return out
}

// ownerOf names the verbs each flag belongs to, for notForThisVerb's line.
var ownerOf = map[string]string{
	"--secret": "probe", "--max": "probe", "--acl": "the bare form", "--json": "check, policy and probe",
	"--name": "the bare form and policy", "--cwd": "the bare form and policy", "--tmp": "the bare form and policy",
	"--net-allow": "the bare form and policy",
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
func checkVerb(args []string, stdout, stderr io.Writer) int {
	v := newVerbOut("check", slices.Contains(args, "--json"), stdout, stderr)
	for i, a := range args {
		if a == "--json" {
			continue
		}
		// the first argument is the refusal: an unknown flag, or a word where none goes
		text, _ := unknownArg(args, i, "check")
		v.refuse(tool.Refused, "bad_flag", oneline.WithRemedy(text, "nova-sandbox help check"))
		return v.done(stderr, "", sandbox.ExitCannotRun)
	}
	backend, ok := sandbox.Available()
	name, note := sandbox.Backend, sandbox.Note()
	net := "unenforceable"
	if !ok {
		name = "none"
	} else if sandbox.NetEnforceable() {
		net = "enforceable"
		note = note + "; backend at " + backend
	}
	return v.done(stdout, fmt.Sprintf("CHECK OK backend=%s abi=%s net=%s hosts=none note=%s",
		oneline.Field(name), oneline.Field(sandbox.ABI()), oneline.Field(net), oneline.Escape(note)), 0,
		"backend", name, "abi", sandbox.ABI(), "net", net, "hosts", "none", "note", note)
}

// probeVerb is rule 10: four or five checks under the REAL policy for this platform, run once
// before the first task (the fifth, read_secret, runs only where a --secret file is named;
// a key delivered by nova-secrets exec has no file). A wall that denies the work too is
// broken, and a two-check probe would call it a pass.
func probeVerb(args []string, stdout, stderr io.Writer, env []string) int {
	f := parseVerb("probe", args)
	v := newVerbOut("probe", f.json, stdout, stderr)
	// a flag the probe would accept and then not build its wall with is refused: a probe
	// of a wall other than the one asked about answers the wrong question
	f.bad = append(f.bad, notForThisVerb("probe", "nova-sandbox probe -h", map[string]bool{
		"--acl": f.acl != "", "--name": f.name != "", "--cwd": f.cwd != "", "--tmp": f.tmp != "", "--net-allow": len(f.netAllow) > 0,
	}, ownerOf)...)
	// EVERY independent problem in ONE run. A friend, dogfooding v0.12.0 (nova-tools #104):
	// a bare `probe` named the missing --secret, and named the missing --write only on
	// the NEXT run, once --secret had been supplied -- a first run sequenced into as many
	// runs as it had mistakes. `nova-wake serve` names all nine of its missing flags at
	// once and that is the shape here too: the checks below are gathered and printed
	// together, and the probe runs only when none of them spoke.
	var bad []sandbox.Refusal
	bad = append(bad, f.bad...)
	// A flag this verb does not have is refused alone, at the first refusal: the flags that
	// were meant may be the ones misspelled, so a list of what else is "missing" beside it
	// is a list of consequences (`probe --wrte x` also said --write was missing).
	if len(f.bad) > 0 {
		for _, r := range f.bad {
			v.refuse(tool.Refused, "check", oneline.WithRemedy(r.Text, "nova-sandbox help probe"))
		}
		return v.done(stderr, "", sandbox.ExitCannotRun)
	}
	// Rule 10: the probe re-executes THIS binary under the policy it just generates, with
	// an internal verb, never a shell. os.Executable() is the resolved command of that
	// wrapped run, so its directory is the root "the directory of the resolved command"
	// by construction: read_root then exercises the one root the generator computes at run
	// time, instead of /bin, which is in the profile verbatim. There is no PATH lookup and
	// no machine on which the probe cannot find its own child.
	self, err := os.Executable()
	if err != nil {
		bad = append(bad, sandbox.Refusal{Reason: "check",
			Text: "this binary cannot name its own path: " + oneline.Err(err)})
	}
	// --secret is a caller path like every other, so rule 5 resolves it: absolute,
	// existing, symlinks followed, and refused for absence rather than passing a probe
	// against a file that is not there. A probe WITHOUT --secret is sound: a caller
	// whose key arrives by environment (nova-secrets exec) has no key FILE for the wall
	// to protect, and the probe then proves the wall's other four checks (issue #881).
	var secret string
	if f.secret != "" {
		got, refusal := sandbox.ResolveCallerFile("--secret", f.secret)
		if refusal != nil {
			bad = append(bad, *refusal)
		}
		secret = got
	}

	// The policy is built even when something above spoke, because it is what knows that
	// --write is missing and that HOME resolves outside it: the two refusals a first run
	// earns together belong in the same print.
	p, policyBad := sandbox.Build(sandbox.Input{
		Reads: f.reads, ReadsNoExec: f.readsNoExec, Writes: f.writes, NetDeny: f.netDeny, NetListen: f.netListen,
		GPU:  f.gpu,
		Argv: []string{self, probeStepVerbName}, Home: homeOf(env),
	})
	homeRemedy(policyBad, f.writes, append([]string{"probe"}, args...))
	bad = append(bad, policyBad...)
	if len(bad) > 0 {
		for _, r := range bad {
			// The PROBE REFUSED grammar is a FIXED set of six reasons
			// (SPEC-SANDBOX.md, Output grammar): check, secret_inside_allow,
			// probe_outside_inside, probe_outside_unwritable, no_sandbox,
			// net_unenforceable. Everything gathered above is refused BEFORE anything
			// runs, so its reason is `check` -- and the spec's own worked example is
			// exactly this line: a probe whose HOME is outside every --write is
			// `PROBE REFUSED reason=check ... home_outside` ("The probe"). The
			// refusal's own token is therefore named IN THE TEXT, where a reader
			// searching for home_outside or bad_write still finds it and the grammar
			// stays the one the spec publishes. DeepSeek's read of #108 at ab880be,
			// finding 1: the previous revision forwarded bad_write, home_outside,
			// bad_read and no_command into reason=, tokens the grammar does not list.
			// the token goes before a remedy the text carries, so the remedy stays a paste
			text, next, hasNext := strings.Cut(r.Text, "; run: ")
			if r.Reason != "" && r.Reason != "check" {
				text += " (" + r.Reason + ")"
			}
			if hasNext {
				text += "; run: " + next
			}
			v.refuse(tool.Refused, "check", oneline.WithRemedy(oneline.Escape(text), "nova-sandbox probe -h"))
		}
		return v.done(stderr, "", sandbox.ExitCannotRun)
	}
	// rule 6: a --secret inside a named path is a misconfiguration, not a failed probe.
	// A probe without --secret has no secret to place, and the check is skipped.
	if secret != "" {
		for _, d := range append(append([]string{}, p.Reads...), p.Writes...) {
			if sandbox.Inside(secret, d) {
				v.refuse(tool.Refused, "secret_inside_allow", fmt.Sprintf("--secret %s is inside %s; the secret is never inside either list; run: nova-sandbox probe -h",
					oneline.Escape(secret), oneline.Escape(d)))
				return v.done(stderr, "", sandbox.ExitCannotRun)
			}
		}
	}

	// rule 10: the outside path is NAMED, never os.TempDir(), because rule 8 points
	// TMPDIR inside the wall and a probe built on it would fail on a working wall.
	outside := filepath.Join(filepath.Dir(p.Writes[0]), fmt.Sprintf(".nova-sandbox-probe-%d", os.Getpid()))
	for _, d := range append(append([]string{}, p.Reads...), p.Writes...) {
		if sandbox.Inside(outside, d) {
			v.refuse(tool.Refused, "probe_outside_inside", fmt.Sprintf("%s is inside %s; a probe that cannot find an outside cannot answer the question; run: nova-sandbox probe -h",
				oneline.Escape(outside), oneline.Escape(d)))
			return v.done(stderr, "", sandbox.ExitCannotRun)
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
	// stays so that a mismatch still refuses; the environment copy stays as the guard's
	// second factor — a third copy required to agree — and nothing else reads it
	// (measured: no reader of NOVA_SANDBOX_PROBE_NONCE exists outside this guard;
	// tools/sandboxcheck never mentions it and the step bodies take path and name from argv).
	// The child then asks the OS who its parent is and refuses unless that process is this
	// same binary.
	rawNonce, err := probeNonce()
	if err != nil {
		v.refuse(tool.Refused, "check", "this machine has no random source for the probe's one-time value: "+oneline.WithRemedy(oneline.Err(err), "nova-sandbox probe -h"))
		return v.done(stderr, "", sandbox.ExitCannotRun)
	}
	nonce := hex.EncodeToString(rawNonce[:])

	// FIVE CHECKS, OR FOUR WHERE NO --secret was named (issue #881): a caller whose key
	// arrives by environment has no key file, so there is no read_secret to prove. The
	// order is the spec's own.
	type step struct{ name, path, expect string }
	steps := []step{
		{"write_outside_control", outside, "allow"},
		{"write_outside", outside, "deny"},
	}
	if secret != "" {
		steps = append(steps, step{"read_secret", secret, "deny"})
	}
	steps = append(steps,
		step{"write_inside", filepath.Join(p.Writes[0], ".nova-sandbox-probe-inside"), "allow"},
		step{"read_root", p.Command, "allow"},
	)
	passed, failed := 0, 0
	for _, s := range steps {
		var got string
		switch s.name {
		case "write_outside_control":
			// The control runs OUTSIDE the wall, first: a denial that was never possible
			// is not a wall. If this one says deny, the machine cannot answer the
			// question and the probe is exit 2, not a failed check.
			if err := os.WriteFile(s.path, []byte("nova"), 0o600); err != nil {
				v.item(fmt.Sprintf("PROBE STEP name=%s expect=allow got=deny path=%s", oneline.Field(s.name), oneline.Escape(s.path)),
					"step", "name", s.name, "expect", "allow", "got", "deny", "path", s.path)
				v.refuse(tool.Refused, "probe_outside_unwritable", oneline.Escape(s.path)+" is not writable by this user anyway, so a deny there proves nothing; run: nova-sandbox probe -h")
				return v.done(stderr, "", sandbox.ExitCannotRun)
			}
			// ignored: the control file this probe just wrote outside the wall; a leftover holds only the word nova in the caller's scratch path
			_ = os.Remove(s.path)
			got = "allow"
		default:
			got = walled(p, env, rawNonce, nonce, s.name, s.path)
		}
		v.item(fmt.Sprintf("PROBE STEP name=%s expect=%s got=%s path=%s",
			oneline.Field(s.name), oneline.Field(s.expect), oneline.Field(got), oneline.Escape(s.path)),
			"step", "name", s.name, "expect", s.expect, "got", got, "path", s.path)
		if got == s.expect {
			passed++
			continue
		}
		failed++
		v.refuse(tool.Failed, "check", fmt.Sprintf("%s expected %s and got %s at %s; run: nova-sandbox probe -h",
			oneline.Field(s.name), s.expect, got, oneline.Escape(s.path)))
	}
	facts := []any{"backend", sandbox.Backend, "abi", sandbox.ABI(), "steps", len(steps), "passed", passed, "net", p.Net(), "gpu", string(p.GPUMode)}
	if failed > 0 {
		return v.done(stderr, "", sandbox.ExitProbeFailed, facts...)
	}
	return v.done(stdout, fmt.Sprintf("PROBE OK backend=%s abi=%s steps=%d passed=%d net=%s gpu=%s",
		oneline.Field(sandbox.Backend), oneline.Field(sandbox.ABI()), len(steps), passed, oneline.Field(p.Net()), oneline.Field(string(p.GPUMode))), 0, facts...)
}

// walled runs one probe check INSIDE the wall and reports allow or deny. The child is
// this same binary with the internal verb, so the step name and the path are
// argv ELEMENTS: nothing the caller handed the tool is ever re-parsed by an interpreter,
// which means the tool's own child is never run through a shell either. The
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

// probeStepVerbName is the tool's one internal verb. It is not in the usage banner and
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
//  3. The parent process is running THIS FILE — the same device and inode, not the same
//     name. sandbox-exec execs in place, so the probe's child has the tool for a parent; a
//     child started by anything else does not, and a COPY of the tool is a different file
//     however it is named or wherever it is resolved from (sameImage).
//
// The honest bound on all three is in docs/SPEC-SANDBOX.md's probe section: the verb
// grants NO CAPABILITY THE CALLER LACKS, because everything it does is bounded by the wall
// it runs in. What the guard stops is an invocation from OUTSIDE that wall — by hand, or
// driven by CONTENT: a script, a Makefile, a repository's own hook — reaching this verb's
// O_TRUNC by guessing a word. A caller already INSIDE the wall gains nothing by it:
// measured on 676d432, a shell under this tool's own exec verb built a 16-byte pipe as
// fd 3, minted a value for argv and the environment, and exec'd probe-step (sandbox-exec
// and the shell both exec in place, so the parent IS the tool); all three halves passed,
// and the O_TRUNC it reached was one the wall already allowed — a file INSIDE the wall,
// which content there could empty with `: > file` anyway. The same run against a file
// OUTSIDE the wall was refused by the wall, exit 1, the file intact.
func notTheProbesChild(nonce string, env []string) string {
	got, err := probeNonceOnFD()
	if err != nil {
		return err.Error()
	}
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(got[:])), []byte(nonce)) != 1 {
		return "the value on fd 3 is not the one in the argv"
	}
	// The environment copy is the guard's second factor and nothing else reads it: it is
	// checked here so that the three copies cannot disagree.
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
	// An absolute path is what these syscalls promise; anything else is an answer this guard
	// has no way to check, and an unchecked answer is a pass.
	if !filepath.IsAbs(self) {
		return "this process is not named by an absolute path"
	}
	return parentIsThisImage(self, os.Getppid, parentExecutable)
}

// parentIsThisImage is the parent half's ORDERING, lifted away from the two syscalls that
// answer it so that the ordering itself can be tested rather than only described. It reads
// the pid, the image, the pid again and THE IMAGE AGAIN, and it refuses if anything moved
// between any two of those reads.
//
// Both re-reads are there because a pid and an image can each change under the other.
//
//   - The pid can go: os.Getppid() and the per-pid path read are two syscalls, and between
//     them the parent can exit. This process is reparented, the pid is free again, and on
//     a busy machine it is handed out again within the same second — so asking a STALE
//     number is asking about whatever process now wears it.
//   - The IMAGE can go while the pid stays. exec(2) replaces a process's image IN PLACE and
//     leaves its pid alone: a parent that is this binary when the
//     first read happens can exec something else and still be the same pid at the second
//     read, so a pid that did not move proves nothing about the image that ran. The image
//     is therefore read again, and the second read is a fresh stat: it catches both an exec
//     of a different path and a different file put at the SAME path.
//
// The window cannot be closed to zero from inside the child — there is no call that hands a
// process an atomic "my parent, now" — so what this does is make every read that the guard
// rests on a read that was still true at the end of it, and refuse otherwise. Refusing is
// the whole point: the probe's real parent does not exec anything, so the honest cost of
// the re-reads is nil and the dishonest one is a refusal.
func parentIsThisImage(self string, getppid func() int, imageOf func(pid int) (string, error)) string {
	before := getppid()
	first, err := imageOf(before)
	if err != nil {
		return "the parent process cannot be named"
	}
	if !filepath.IsAbs(first) {
		return "the parent process is not named by an absolute path"
	}
	if !sameImage(self, first) {
		return "the parent process is not this binary"
	}
	after := getppid()
	if after != before {
		return "the parent process changed while the guard was reading it"
	}
	second, err := imageOf(after)
	if err != nil {
		return "the parent process cannot be named"
	}
	if !filepath.IsAbs(second) {
		return "the parent process is not named by an absolute path"
	}
	if !sameImage(self, second) || !sameImage(first, second) {
		return "the parent process changed the image it is running while the guard was reading it"
	}
	return ""
}

// sameImage answers the question the guard actually has — "is the parent running THIS
// FILE?" — and it answers it with the file's IDENTITY, device and inode, rather than with
// its NAME.
//
// A name was the hole. The two paths compared here come from different syscalls that do
// not even agree on the spelling of the same file: measured on darwin, os.Executable()
// hands back the path as it was passed to exec (/tmp/x) while the kernel's per-pid path is
// the resolved one (/private/tmp/x). The old form papered over that with EvalSymlinks and
// a fallback — `if got, err := filepath.EvalSymlinks(p); err == nil { return got }; return p`
// — which SWALLOWED its error: EvalSymlinks walks and Lstats every component, so a single
// component it cannot read (a directory another test is removing, a step denied inside the
// wall, an interrupted call on a loaded machine) silently turned the comparison back into
// one between two spellings. Two spellings are not an identity either way round: a COPY of
// the tool at a name that resolves the same way passes a name test and fails this one,
// which is the defect this function closes.
//
// A stat that fails is a refusal, not a fallback: this is the half of the guard a caller
// cannot supply, and a half that answers "I could not tell" must answer no.
func sameImage(self, parent string) bool {
	selfInfo, err := os.Stat(self)
	if err != nil {
		return false
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return false
	}
	return os.SameFile(selfInfo, parentInfo)
}

// probeStepVerb is that child: one step, done in Go, exit 0 for allow and 1 for deny. Each
// case is the smallest syscall that answers its question, and read_secret opens the file
// and closes it without reading a byte, so the secret's bytes never enter this process.
func probeStepVerb(args []string, stderr io.Writer, env []string) int {
	if len(args) != 3 {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_step_not_a_child: %s is internal and takes <nonce> <name> <path>; it is the child of a probe this binary started and nothing else runs it; run: nova-sandbox probe -h\n", probeStepVerbName)
		return sandbox.ExitCannotRun
	}
	nonce, name, path := args[0], args[1], args[2]
	// The guard, BEFORE anything is opened. A refusal here opens no file, truncates no
	// file and creates no file — the line is the whole of the answer.
	if r := notTheProbesChild(nonce, env); r != "" {
		fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_step_not_a_child: %s runs only as the child of a probe this binary started, and this invocation is not one (%s); nothing was opened. Run: nova-sandbox probe --write <dir> --secret <path>; run: nova-sandbox probe -h\n", probeStepVerbName, r)
		return sandbox.ExitCannotRun
	}
	// The one path this verb is handed is absolute, never relative. The
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
		// ignored: the probe asks whether the open was allowed, which the open already answered; nothing was written through it
		_ = f.Close()
		if name == "write_inside" {
			// ignored: the probe's own empty file inside the wall; the wall's directory is disposable
			_ = os.Remove(path)
		}
		return 0
	case "read_secret":
		// Opened and closed. Nothing is read, so the tool never holds a credential's
		// bytes even for the length of one syscall.
		f, err := os.Open(path)
		if err != nil {
			return sandbox.ExitProbeFailed
		}
		// ignored: the probe asks whether the open was allowed, which the open already answered; nothing was read through it
		_ = f.Close()
		return 0
	case "read_root":
		f, err := os.Open(path)
		if err != nil {
			return sandbox.ExitProbeFailed
		}
		var one [1]byte
		n, err := f.Read(one[:])
		// ignored: a close after the one-byte read, whose result is judged on the next line
		_ = f.Close()
		if err != nil || n != 1 {
			return sandbox.ExitProbeFailed
		}
		return 0
	}
	fmt.Fprintf(stderr, "PROBE REFUSED reason=check: %s is not a probe step; run: nova-sandbox probe -h\n", oneline.Field(name))
	return sandbox.ExitCannotRun
}

// policyText is the generated policy for the backend this binary was built with: the darwin
// profile where the wall is sandbox-exec, the landlock ruleset where it is
// Landlock. Printing the darwin template under backend=landlock showed a reader
// seven kilobytes of policy no linux run uses.
func policyText(p *sandbox.Policy) (string, error) {
	if sandbox.Backend == "landlock" {
		return sandbox.LandlockPolicyText(p)
	}
	text, _, err := sandbox.DarwinProfile(p)
	return text, err
}

// policyVerb prints the generated policy for a read/write pair and runs NOTHING. It is
// how a reader checks the wall without trusting the document — and it is how
// tools/sandboxcheck can be run against the profile THIS TOOL generates, so that
// the check and the tool cannot drift apart: the profile is generated, never hand-edited.
func policyVerb(args []string, stdout, stderr io.Writer, env []string) int {
	f := parseVerb("policy", args)
	v := newVerbOut("policy", f.json, stdout, stderr)
	refuse := func(bad []sandbox.Refusal) int {
		for _, r := range bad {
			v.refuse(tool.Refused, r.Reason, oneline.WithRemedy(oneline.Escape(r.Text), "nova-sandbox policy -h"))
		}
		return v.done(stderr, "", sandbox.ExitCannotRun)
	}
	if len(f.bad) > 0 {
		return refuse(f.bad)
	}
	// The policy this verb prints is exactly what a wrapped run would apply. One root is
	// computed from the COMMAND — "the directory of the resolved command" — so a policy
	// built around /bin/sh could not show it for any real command, and the one root a
	// reader most needs to see was the one root this verb could not print. The command is
	// therefore optional and comes after -- exactly as it does on the bare form; with no
	// --, /bin/sh is the floor every wrapped shell command already stands on.
	argv := f.argv
	if len(argv) == 0 {
		shell, err := exec.LookPath("sh")
		if err != nil {
			return refuse([]sandbox.Refusal{{Reason: "bad_read", Text: "sh is on no PATH entry: " + err.Error()}})
		}
		argv = []string{shell, "-c", "true"}
	}
	p, bad := sandbox.Build(sandbox.Input{
		Reads: f.reads, ReadsNoExec: f.readsNoExec, Writes: f.writes, Cwd: f.cwd, Tmp: f.tmp, Name: f.name,
		NetDeny: f.netDeny, NetListen: f.netListen, NetAllow: f.netAllow, Argv: argv, Home: homeOf(env),
		GPU: f.gpu,
	})
	if len(bad) > 0 {
		homeRemedy(bad, f.writes, append([]string{"policy"}, args...))
		return refuse(bad)
	}
	text, err := policyText(p)
	if err != nil {
		return refuse([]sandbox.Refusal{{Reason: "bad_write", Text: err.Error()}})
	}
	if f.json {
		v.out.Payload = text
	} else {
		fmt.Fprint(stdout, text)
	}
	// Another verb's flag is accepted, as a caller that builds one argv for policy and
	// probe has always had it, and said aloud: it changes nothing in the policy printed.
	given := map[string]bool{"--secret": f.secret != "", "--max": f.maxSet, "--acl": f.acl != ""}
	for _, flag := range []string{"--secret", "--max", "--acl"} {
		if given[flag] {
			v.note(flag + " is " + ownerOf[flag] + "'s flag and is ignored here: the policy printed is the same without it")
		}
	}
	return v.done(stderr, fmt.Sprintf("POLICY OK backend=%s read=%d read-noexec=%d write=%d bytes=%d gpu=%s",
		oneline.Field(sandbox.Backend), len(p.Reads), len(p.ReadsNoExec), len(p.Writes), len(text), oneline.Field(string(p.GPUMode))), 0,
		"backend", sandbox.Backend, "read", len(p.Reads), "read-noexec", len(p.ReadsNoExec), "write", len(p.Writes), "bytes", len(text), "gpu", string(p.GPUMode))
}
