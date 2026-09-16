// nova-work is the thin client of the resident work session, described in
// docs/SPEC-WORK.md ("The engine and its client"). The session -- a Common
// Lisp process owned by the account that runs it -- listens on one
// Unix-domain socket and owns every fact: the state, the journal, the indexes,
// the mutation ordering. This client owns none of them. It sends ONE request
// line over the socket --session names, newline-terminated, and prints the
// ONE line the session answers, byte for byte: OK, ROW, NOTE and MORE to
// stdout and exit 0, FAIL, RACED and REFUSED to stderr and exit 1. What
// cannot run at all -- no verb, no --session, no socket that answers -- is
// one WORK REFUSED line on stderr, exit 2, ending "run: nova-work help". A
// fresh CLI process is never a fresh parse: the session stays up and serves,
// and this process is gone.
//
// The values travel as the caller spelled them and the session validates every
// one, refusing with its own naming, because a client that second-guessed the
// engine's bounds would drift from it. The S1 wire is one line each way,
// newline-terminated; the framed JSON protocol the spec pins is the later
// lock gate's artifact, and nothing here prejudges it.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// version is empty in ordinary builds and is filled only by a release stamp.
var version string

// usage is what help prints and what docs/CLI.md's nova-work section carries
// byte for byte. The three session verb lines are the spec's own verbs block
// (docs/SPEC-WORK.md, the usage fence) with its alignment kept, so the flag
// surface a caller reads here is the surface the spec names there.
const usage = `nova-work: the thin client of the resident work session (see docs/SPEC-WORK.md)

usage:
  nova-work session start  --session <path> --as <name> --file <path-in-repo> --journal <path> --cache <path> --repo <path> --remote <name> --branch <name>
                           --max-bytes <n> --max-depth <n> --max-nodes <n> --every <duration> --skew <duration> --clip-every <duration> --clip-after <n> --retain <duration>
                           --savepoint-every <duration> --savepoint-after <n> --max-frame-bytes <n> --silence-ping <duration>
                           --index-cache <n> --page-bytes <n> --page-records <n> [--closed-window <duration>] [--render-root <root-id>=<owner/name>:<directory> ...]
                           [--resolver <scheme>=<command> ...] --git-timeout <seconds> [--attempts <n>] [--repair] [--foreground] [--max <n>] [--now <stamp>]
  nova-work session status --session <path>
  nova-work session stop   --session <path> --git-timeout <seconds> [--attempts <n>] [--no-clip]
  nova-work version        print this build identity (--version also accepted)
  nova-work help

wire:
  one line in, one line out over the Unix socket --session names. The request
  line is the verb and its flags in the order above, each as --name <value>,
  values escaped through internal/oneline's field form (one token per value:
  a space is \x20, an equals is \x3d), bools as --name true, the whole line
  newline-terminated. The reply is the session's own answer line, printed byte
  for byte: OK, ROW, NOTE and MORE to stdout, exit 0; FAIL, RACED and REFUSED
  to stderr, exit 1. What cannot run at all is one WORK REFUSED line on
  stderr, exit 2, ending "run: nova-work help". Values travel as given: the
  session validates every one and refuses with its own naming.

example:
  nova-work session status --session ./sessions/alpha.sock
  nova-work session stop --session ./sessions/alpha.sock --git-timeout 30
  nova-work version
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, version)) }

func run(args []string, stdout, stderr io.Writer, stamp string) int {
	if len(args) == 0 {
		return refused(stderr, "a verb is required")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "help", "--help", "-h":
		if len(rest) != 0 {
			return refused(stderr, "help takes no arguments")
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		if len(rest) != 0 {
			return refused(stderr, "version takes no arguments")
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-work", stamp))
		return 0
	case "session":
		if len(rest) == 0 {
			return refused(stderr, "session needs one of start, status or stop")
		}
		sub, rest := rest[0], rest[1:]
		switch sub {
		case "start", "status", "stop":
			return sessionVerb("session "+sub, rest, stdout, stderr)
		default:
			return refused(stderr, fmt.Sprintf("unknown session verb %q", sub))
		}
	default:
		return refused(stderr, fmt.Sprintf("unknown verb %q", verb))
	}
}

// refused is what could not run costs: ONE line on stderr naming what was
// wrong and the door to the usage, exit 2 -- the spec's own remedy spelling
// ("which costs one line ending `run: nova-work help`").
func refused(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "WORK REFUSED: %s; run: nova-work help\n", oneline.Escape(what))
	return 2
}

// flagSpec is one flag of one socket verb. A bool flag is a switch serialized
// as --name true when set; a multi flag is repeatable and serializes one
// --name per value in the order the caller gave them; every other value
// travels as the caller spelled it, a string, because the session validates
// and a thin client never second-guesses the engine's bounds.
type flagSpec struct {
	name  string
	multi bool
	bool  bool
}

// verbFlags is each socket verb's flag surface and its canonical order, the
// spec's verbs block in the same order it prints them, so the request line a
// session reads is spelled the way its own spec names the flags.
var verbFlags = map[string][]flagSpec{
	"session start": {
		{name: "session"},
		{name: "as"},
		{name: "file"},
		{name: "journal"},
		{name: "cache"},
		{name: "repo"},
		{name: "remote"},
		{name: "branch"},
		{name: "max-bytes"},
		{name: "max-depth"},
		{name: "max-nodes"},
		{name: "every"},
		{name: "skew"},
		{name: "clip-every"},
		{name: "clip-after"},
		{name: "retain"},
		{name: "savepoint-every"},
		{name: "savepoint-after"},
		{name: "max-frame-bytes"},
		{name: "silence-ping"},
		{name: "index-cache"},
		{name: "page-bytes"},
		{name: "page-records"},
		{name: "closed-window"},
		{name: "render-root", multi: true},
		{name: "resolver", multi: true},
		{name: "git-timeout"},
		{name: "attempts"},
		{name: "repair", bool: true},
		{name: "foreground", bool: true},
		{name: "max"},
		{name: "now"},
	},
	"session status": {
		{name: "session"},
	},
	"session stop": {
		{name: "session"},
		{name: "git-timeout"},
		{name: "attempts"},
		{name: "no-clip", bool: true},
	},
}

// repeatFlag is a --flag that may be given more than once.
type repeatFlag []string

func (r *repeatFlag) String() string { return strings.Join(*r, ",") }
func (r *repeatFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func sessionVerb(verb string, args []string, stdout, stderr io.Writer) int {
	specs := verbFlags[verb]
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	strs := map[string]*string{}
	bools := map[string]*bool{}
	mults := map[string]*repeatFlag{}
	for _, s := range specs {
		switch {
		case s.bool:
			bools[s.name] = f.Bool(s.name, false, "")
		case s.multi:
			m := &repeatFlag{}
			f.Var(m, s.name, "")
			mults[s.name] = m
		default:
			strs[s.name] = f.String(s.name, "", "")
		}
	}
	if err := f.Parse(args); err != nil {
		return refused(stderr, fmt.Sprintf("%s: %v", verb, err))
	}
	if f.NArg() != 0 {
		return refused(stderr, fmt.Sprintf("%s takes no positional arguments (got %q)", verb, f.Arg(0)))
	}
	socket := *strs["session"]
	if socket == "" {
		return refused(stderr, "--session is required; refusing to guess (the socket has no default path)")
	}
	var b strings.Builder
	b.WriteString(verb)
	for _, s := range specs {
		switch {
		case s.bool:
			if *bools[s.name] {
				fmt.Fprintf(&b, " --%s true", s.name)
			}
		case s.multi:
			for _, v := range *mults[s.name] {
				fmt.Fprintf(&b, " --%s %s", s.name, oneline.Field(v))
			}
		default:
			if v := *strs[s.name]; v != "" {
				fmt.Fprintf(&b, " --%s %s", s.name, oneline.Field(v))
			}
		}
	}
	return ask(socket, b.String(), stdout, stderr)
}

// replyCap bounds the one reply line, refused whole rather than truncated,
// the family's own law in miniature. One MiB is the S1 wire's standing bound;
// the framed protocol the spec pins carries its own.
const replyCap = 1 << 20

// ask is the whole wire: one line in, one line out, newline-terminated both
// ways -- the S1 endpoint's contract, landed parallel with it.
func ask(socket, request string, stdout, stderr io.Writer) int {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return refused(stderr, fmt.Sprintf("no such session: cannot reach %s: %v", socket, err))
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(request + "\n")); err != nil {
		return refused(stderr, fmt.Sprintf("cannot reach %s: %v", socket, err))
	}
	line, err := readReply(conn)
	if err != nil {
		return refused(stderr, fmt.Sprintf("no answer from %s: %v", socket, err))
	}
	return printReply(line, stdout, stderr)
}

func readReply(conn net.Conn) (string, error) {
	buf := make([]byte, 0, 1024)
	chunk := make([]byte, 1024)
	for {
		n, err := conn.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			return string(buf[:i]), nil
		}
		if len(buf) > replyCap {
			return "", fmt.Errorf("reply line past %d bytes", replyCap)
		}
		if err != nil {
			return "", errors.New("the session closed before one line")
		}
	}
}

// printReply splits the session's line by the second token and by nothing
// else, the spec's own client rule: OK, ROW, NOTE and MORE to stdout; FAIL
// and RACED to stderr; and a refusal -- the session answered, and what it
// answered was no, the spec's exit 1 ("it ran and said no", a refused take
// among them) -- to stderr the same way. Anything else is not a line the
// grammar spells, and the client refuses rather than guessing a verdict.
func printReply(line string, stdout, stderr io.Writer) int {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return refused(stderr, "the session's reply is not an answer line: "+oneline.Escape(line))
	}
	switch fields[1] {
	case "OK", "ROW", "NOTE", "MORE":
		fmt.Fprintln(stdout, line)
		return 0
	case "FAIL", "RACED", "REFUSED":
		fmt.Fprintln(stderr, line)
		return 1
	default:
		return refused(stderr, "the session's reply carries no verdict the grammar spells: "+oneline.Escape(line))
	}
}
