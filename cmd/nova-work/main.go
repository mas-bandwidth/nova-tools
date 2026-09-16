// nova-work is the kernel's side of the work description language
// (docs/SPEC-WORKLANG.md) and the job graph of docs/SPEC-JOBS.md: it reads a `.work`
// plan file as data, never as a program, and holds it to the three bounds the kernel
// already carries.
//
// The bounded reader: `plan check` reads one plan under --max-bytes, --max-depth and
// --max-nodes, refuses a `#.` dispatch macro at the byte offset that owes it, refuses
// any input past a bound whole rather than truncated, and refuses an unknown `:kind` by
// field name. It then closes the plan's needs/blocks graph at load: `:needs` is the
// reference edge and `:blocks` its inverse, an absent need is refused naming the field
// and the id, and a `:needs` cycle is refused by validator rule 3 before publication. A
// well-formed plan prints one line; a refusal is exit 2 with one remedy line.
//
// The job graph: typed needs and blocks edges, refused acyclic at seed by validator rule
// 3, and the mechanical ready set launch reads. A node is ready only when every need is
// terminal accepted. Every row that cannot proceed prints its exact blocker and its
// resolver; a card whose need is an open PR is never on a slot. This slice owns the
// in-process graph and the ready reading only: no Redis, no network, no launch, no lease.
//
// nova-work is also the thin client of the resident work session, described in
// docs/SPEC-WORK.md ("The engine and its client"). The session -- a Common Lisp process
// owned by the account that runs it -- listens on one Unix-domain socket and owns every
// fact: the state, the journal, the indexes, the mutation ordering. This client owns none
// of them. It sends ONE request line over the socket --session names, newline-terminated,
// and prints the ONE line the session answers, byte for byte: OK, ROW, NOTE and MORE to
// stdout and exit 0, FAIL, RACED and REFUSED to stderr and exit 1. What cannot run at all
// -- no verb, no --session, no socket that answers -- is one WORK REFUSED line on stderr,
// exit 2, ending "run: nova-work help". A fresh CLI process is never a fresh parse: the
// session stays up and serves, and this process is gone.
//
// Every path comes from a flag. There is no default file and no discovery: a missing
// flag is a refusal, never a guess. Output is one line per verb. Exit 0 ran and passed;
// exit 2 could not run -- a missing flag, an unreadable graph or plan, a :deps cycle, an
// unknown node, a refusal.
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
	"github.com/mas-bandwidth/nova-tools/internal/jobs"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// version is empty in ordinary builds and is filled only by a release stamp.
var version string

// usage is what help prints and what docs/CLI.md's nova-work section carries
// byte for byte. The verb lines are the two surfaces the same binary serves: the
// job graph and the bounded .work reader of docs/SPEC-JOBS.md and
// docs/SPEC-WORKLANG.md, and the thin client of the resident work session of
// docs/SPEC-WORK.md, whose session/query verbs send one line each way over the
// socket --session names.
const usage = `nova-work: the job graph, the bounded .work reader (see docs/SPEC-JOBS.md, docs/SPEC-WORKLANG.md)
           and the thin client of the resident work session (see docs/SPEC-WORK.md)

usage:
  nova-work version
  nova-work dependencies --graph <file> [--node <id> --needs <id>[,<id>...]]
  nova-work ready --node X --graph <file>
  nova-work clip --worktree <dir> --branch <name> --base <ref> --harvest <dir> [--result <file>] [--message <text>]
  nova-work plan check --file <path.work> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work plan expand --file <path.work> --out <dir> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work session start  --session <path> --as <name> --file <path-in-repo> --journal <path> --cache <path> --repo <path> --remote <name> --branch <name>
                           --max-bytes <n> --max-depth <n> --max-nodes <n> --every <duration> --skew <duration> --clip-every <duration> --clip-after <n> --retain <duration>
                           --savepoint-every <duration> --savepoint-after <n> --max-frame-bytes <n> --silence-ping <duration>
                           --index-cache <n> --page-bytes <n> --page-records <n> [--closed-window <duration>] [--render-root <root-id>=<owner/name>:<directory> ...]
                           [--resolver <scheme>=<command> ...] --git-timeout <seconds> [--attempts <n>] [--repair] [--foreground] [--max <n>] [--now <stamp>]
  nova-work session status --session <path>
  nova-work session stop   --session <path> --git-timeout <seconds> [--attempts <n>] [--no-clip]
  nova-work query size     --session <path> [--node <id>]
  nova-work query remaining --session <path> [--node <id>]
  nova-work query ready    --session <path> [--node <id>] [--order <discovery|priority>]
  nova-work version        print this build identity (--version also accepted)
  nova-work help

verbs:
  nova-work dependencies   owns the graph (:deps, refused acyclic at seed by validator rule 3)
  nova-work ready --node X is the ready set
  nova-work clip           commits the card's branch, harvests its result, resets the worktree to base
  nova-work plan check     reads a .work plan as data and closes its needs/blocks graph, never as a program
  nova-work plan expand    writes one card directory per hand-written :node, refusing a cycle or an absent need
  nova-work session start  starts the resident work session over the socket --session names
  nova-work session status prints the session's own SESSION OK line, byte for byte
  nova-work session stop   asks the session to stop, then prints its own line
  nova-work query size     asks the session for the required-leaf count under one scope
  nova-work query remaining asks the session for the leaves that are not done
  nova-work query ready    asks the session for the work that can actually be started

A node is ready only when every need is terminal accepted, and every row that cannot
proceed prints its exact blocker and its resolver. A :deps cycle is refused before
publication, so the ready set is finite and the graph can never deadlock.

A plan is read as data, never as a program: a ` + "`#.`" + ` dispatch macro anywhere in code
position is refused at exit 2 naming its byte offset, string and comment text is opaque,
and an unknown :kind is refused naming the field. :needs is the reference edge and
:blocks its inverse, so the kernel derives whichever a node did not give; an absent
need is refused naming the field and the id, and a :needs cycle is refused by validator
rule 3, both at load before the graph is published.

flags:
  --graph <file>  the node graph, as JSON: {"nodes":[{"id":"a","needs":["b"]}, ...]}
                  Required on both graph verbs; there is no default and no discovery.
  --node <id>     dependencies: the node to write a needs edge to, creating it when the
                  graph does not hold it yet. ready: the one node to evaluate; without
                  it, ready prints one row per node in seed order.
  --needs <ids>   a comma-separated list of needs for --node. --needs needs --node;
                  --node alone creates a node needing nothing.
  --file <path>   plan check and plan expand: the plan to read. Required, always:
                  there is no default file and no discovery from the working directory.
  --out <dir>     plan expand: the directory to write one card per node into. Required;
                  a card already there is left byte-identical, so a re-expansion appends
                  only the new card and mints no id.
  --max-bytes <n> plan check: the byte ceiling (default 65536). A file past it is
                  refused before a byte is parsed, never truncated.
  --max-depth <n> plan check: the nesting ceiling (default 64). A form past it is
                  refused at its opening byte.
  --max-nodes <n> plan check: the atom ceiling (default 4096). A plan past it is refused
                  at the atom's byte.

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

exit codes: 0 ran and passed; 2 could not run (bad invocation, an unreadable graph or
plan, a :deps cycle, an unknown node, a refusal).

example:
  nova-work dependencies --graph ./deps.json --node b
  nova-work dependencies --graph ./deps.json --node a --needs b
  nova-work ready --node a --graph ./deps.json
  nova-work plan check --file ./work.work --max-bytes 65536
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is the one entry point both surfaces share. It first offers the verb to
// the job-graph and plan reader, whose dispatch lives in runTask; a verb it
// does not claim falls to the thin client's own switch, which is the switch the
// help inventory is read from. stamp is the release stamp a caller may inject
// in tests; an ordinary build leaves it empty and reads package version.
func run(args []string, stdout, stderr io.Writer, stamp ...string) int {
	if len(args) == 0 {
		return refused(stderr, "a verb is required")
	}
	if code, handled := runTask(args, stdout, stderr); handled {
		return code
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
		v := version
		for _, s := range stamp {
			if s != "" {
				v = s
			}
		}
		// buildinfo.Line already renders each field through oneline.Field; Escape here is
		// the source-level tripwire's proof that this print site is escaped, and it leaves
		// the line's deliberate spaces between fields intact.
		fmt.Fprintln(stdout, oneline.Escape(buildinfo.Line("nova-work", v)))
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
	case "query":
		if len(rest) == 0 {
			return refused(stderr, "query needs one of size, remaining or ready")
		}
		sub, rest := rest[0], rest[1:]
		switch sub {
		case "size", "remaining", "ready":
			return queryVerb("query "+sub, rest, stdout, stderr)
		default:
			return refused(stderr, fmt.Sprintf("unknown query verb %q", sub))
		}
	default:
		return refused(stderr, fmt.Sprintf("unknown verb %q", verb))
	}
}

// runTask dispatches the job-graph and bounded-reader verbs dev landed. It is a
// separate function so the thin client's own verb switch stays the first switch
// in run's body, which is where the help inventory is read from.
func runTask(args []string, stdout, stderr io.Writer) (int, bool) {
	switch args[0] {
	case "dependencies":
		return cmdDependencies(args[1:], stdout, stderr), true
	case "ready":
		return cmdReady(args[1:], stdout, stderr), true
	case "clip":
		return cmdClip(args[1:], stdout, stderr), true
	case "plan":
		return cmdPlan(args[1:], stdout, stderr), true
	}
	return 0, false
}

// refuse is what an unusable invocation or an unreadable plan costs: one line naming
// what was wrong and the door to the usage, never the banner itself.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-work%s: %s; run: nova-work help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

// refused is what could not run costs on the client surface: ONE line on stderr
// naming what was wrong and the door to the usage, exit 2 -- the spec's own
// remedy spelling ("which costs one line ending `run: nova-work help`").
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
	"query size": {
		{name: "session"},
		{name: "node"},
	},
	"query remaining": {
		{name: "session"},
		{name: "node"},
	},
	"query ready": {
		{name: "session"},
		{name: "node"},
		{name: "order"},
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
		return refused(stderr, fmt.Sprintf("%s: %v", oneline.Escape(verb), oneline.Err(err)))
	}
	if f.NArg() != 0 {
		return refused(stderr, fmt.Sprintf("%s takes no positional arguments (got %q)", oneline.Escape(verb), f.Arg(0)))
	}
	socket := *strs["session"]
	if socket == "" {
		return refused(stderr, "--session is required; refusing to guess (the socket has no default path)")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s", oneline.Field(verb))
	for _, s := range specs {
		switch {
		case s.bool:
			if *bools[s.name] {
				fmt.Fprintf(&b, " --%s true", oneline.Field(s.name))
			}
		case s.multi:
			for _, v := range *mults[s.name] {
				fmt.Fprintf(&b, " --%s %s", oneline.Field(s.name), oneline.Field(v))
			}
		default:
			if v := *strs[s.name]; v != "" {
				fmt.Fprintf(&b, " --%s %s", oneline.Field(s.name), oneline.Field(v))
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
		return refused(stderr, fmt.Sprintf("no such session: cannot reach %s: %v", oneline.Field(socket), oneline.Err(err)))
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "%s\n", oneline.Escape(request)); err != nil {
		return refused(stderr, fmt.Sprintf("cannot reach %s: %v", oneline.Field(socket), oneline.Err(err)))
	}
	line, err := readReply(conn)
	if err != nil {
		return refused(stderr, fmt.Sprintf("no answer from %s: %v", oneline.Field(socket), oneline.Err(err)))
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
//
// The reply is printed byte for byte: it travels through Escape, which leaves
// the printable field separators of the session's own grammar untouched and
// escapes only the control characters a line could not carry anyway.
func printReply(line string, stdout, stderr io.Writer) int {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return refused(stderr, "the session's reply is not an answer line: "+oneline.Escape(line))
	}
	switch fields[1] {
	case "OK", "ROW", "NOTE", "MORE":
		fmt.Fprintln(stdout, oneline.Escape(line))
		return 0
	case "FAIL", "RACED", "REFUSED":
		fmt.Fprintln(stderr, oneline.Escape(line))
		return 1
	default:
		return refused(stderr, "the session's reply carries no verdict the grammar spells: "+oneline.Escape(line))
	}
}

func cmdDependencies(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dependencies", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	graph := fs.String("graph", "", "the :deps graph file (required)")
	node := fs.String("node", "", "the node to write a needs edge to")
	needs := fs.String("needs", "", "comma-separated needs for --node")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " dependencies", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " dependencies", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*graph) == "" {
		return refuse(stderr, " dependencies", "--graph is required; refusing to guess")
	}
	if *node == "" && *needs != "" {
		return refuse(stderr, " dependencies", "--needs names the needs of a --node; give both or neither")
	}

	add := strings.TrimSpace(*node) != ""
	var nodes []jobs.Node
	raw, err := os.ReadFile(*graph)
	switch {
	case err == nil:
		nodes, err = jobs.ParseNodes(raw)
		if err != nil {
			return refuse(stderr, " dependencies", err.Error())
		}
	case !add:
		return refuse(stderr, " dependencies", oneline.Err(err))
	}
	if add {
		nodes = addNeeds(nodes, strings.TrimSpace(*node), splitNeeds(*needs))
	}
	// rule 3 refuses the cycle BEFORE anything is written: a refused graph is never
	// published.
	g, err := jobs.Seed(nodes)
	if err != nil {
		return refuse(stderr, " dependencies", err.Error())
	}
	if add {
		out, err := jobs.MarshalNodes(nodes)
		if err != nil {
			return refuse(stderr, " dependencies", err.Error())
		}
		if err := os.WriteFile(*graph, out, 0o644); err != nil {
			return refuse(stderr, " dependencies", oneline.Err(err))
		}
	}
	fmt.Fprintf(stdout, "DEPENDENCIES OK nodes=%d edges=%d\n", g.Len(), g.Edges())
	return 0
}

// addNeeds writes a needs edge to the named node, creating the node when the graph does
// not hold it yet. The blocks edge is the same insert's inverse, so it is never written
// separately.
func addNeeds(nodes []jobs.Node, id string, needs []string) []jobs.Node {
	for i := range nodes {
		if nodes[i].ID == id {
			nodes[i].Needs = append(nodes[i].Needs, needs...)
			return nodes
		}
	}
	return append(nodes, jobs.Node{ID: id, Needs: needs})
}

func splitNeeds(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cmdReady(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ready", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	graph := fs.String("graph", "", "the :deps graph file (required)")
	node := fs.String("node", "", "one node to evaluate; default all nodes")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " ready", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " ready", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*graph) == "" {
		return refuse(stderr, " ready", "--graph is required; refusing to guess")
	}
	raw, err := os.ReadFile(*graph)
	if err != nil {
		return refuse(stderr, " ready", oneline.Err(err))
	}
	g, err := jobs.ParseSeed(raw)
	if err != nil {
		return refuse(stderr, " ready", err.Error())
	}
	if strings.TrimSpace(*node) != "" {
		id := strings.TrimSpace(*node)
		n, ok := g.Node(id)
		if !ok {
			return refuse(stderr, " ready", fmt.Sprintf("no such node %q; refusing to guess", id))
		}
		writeRow(stdout, n, g)
		return 0
	}
	for _, id := range g.Order() {
		n, _ := g.Node(id)
		writeRow(stdout, n, g)
	}
	return 0
}

// writeRow prints one ready row: the node, whether it may proceed, and -- when it
// cannot -- its exact blocker and its resolver. A node that has already merged and gone
// green cannot begin because it is done, not because a need blocks it.
func writeRow(stdout io.Writer, n jobs.Node, g *jobs.Graph) {
	ready, blocker := g.Ready(n.ID)
	fmt.Fprintf(stdout, "READY node=%s ready=%t", oneline.Field(n.ID), ready)
	if blocker != nil {
		fmt.Fprintf(stdout, " blocker=%s state=%s resolver=%s",
			oneline.Field(blocker.Need), oneline.Field(blocker.State), oneline.Quote(blocker.Resolver))
	} else if !ready {
		fmt.Fprintf(stdout, " state=%s blocker=- resolver=-", oneline.Field(n.State()))
	}
	fmt.Fprintln(stdout)
}

func cmdPlan(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "check" && args[0] != "expand") {
		return refuse(stderr, " plan", "the verbs are plan check --file <path.work> and plan expand --file <path.work> --out <dir>")
	}
	if args[0] == "expand" {
		return cmdPlanExpand(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("plan check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	file := fs.String("file", "", "the plan to read (required)")
	def := worklang.DefaultLimits()
	maxBytes := fs.Int("max-bytes", def.MaxBytes, "byte ceiling")
	maxDepth := fs.Int("max-depth", def.MaxDepth, "nesting depth ceiling")
	maxNodes := fs.Int("max-nodes", def.MaxNodes, "atom ceiling")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(stderr, " plan check", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " plan check", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *file == "" {
		return refuse(stderr, " plan check", "--file is required; refusing to guess")
	}
	limits := worklang.Limits{MaxBytes: *maxBytes, MaxDepth: *maxDepth, MaxNodes: *maxNodes}
	data, err := os.ReadFile(*file)
	if err != nil {
		return refuse(stderr, " plan check", oneline.Err(err))
	}
	plan, err := worklang.ParsePlan(*file, data, limits)
	if err != nil {
		return refuse(stderr, " plan check", oneline.Err(err))
	}
	// Closing the graph at load refuses an absent need and a :needs cycle before
	// anything is published: a plan whose graph cannot be built is not a plan.
	graph, err := plan.Graph()
	if err != nil {
		return refuse(stderr, " plan check", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "PLAN OK file=%s bytes=%d version=%d nodes=%d edges=%d\n",
		oneline.Field(*file), len(data), plan.Version, len(plan.Nodes), graph.Edges())
	return 0
}

// cmdPlanExpand is the smallest first slice of SPEC-WORKLANG's expander: it
// reads hand-written :nodes, builds the needs/blocks graph, refuses a cycle or
// an absent need, and writes one card directory per node under --out. Output is
// one line; a refusal is exit 2 with one remedy line.
func cmdPlanExpand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan expand", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	file := fs.String("file", "", "the plan to expand (required)")
	out := fs.String("out", "", "the directory to write one card per node into (required)")
	def := worklang.DefaultLimits()
	maxBytes := fs.Int("max-bytes", def.MaxBytes, "byte ceiling")
	maxDepth := fs.Int("max-depth", def.MaxDepth, "nesting depth ceiling")
	maxNodes := fs.Int("max-nodes", def.MaxNodes, "atom ceiling")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " plan expand", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " plan expand", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *file == "" {
		return refuse(stderr, " plan expand", "--file is required; refusing to guess")
	}
	if *out == "" {
		return refuse(stderr, " plan expand", "--out is required; refusing to guess")
	}
	limits := worklang.Limits{MaxBytes: *maxBytes, MaxDepth: *maxDepth, MaxNodes: *maxNodes}
	data, err := os.ReadFile(*file)
	if err != nil {
		return refuse(stderr, " plan expand", oneline.Err(err))
	}
	plan, err := worklang.ParsePlan(*file, data, limits)
	if err != nil {
		return refuse(stderr, " plan expand", oneline.Err(err))
	}
	cards, err := worklang.ExpandPlan(plan)
	if err != nil {
		return refuse(stderr, " plan expand", oneline.Err(err))
	}
	written, err := worklang.ExpandDir(*out, cards)
	if err != nil {
		return refuse(stderr, " plan expand", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "PLAN EXPANDED file=%s out=%s nodes=%d cards=%d\n",
		oneline.Field(*file), oneline.Field(*out), len(cards), written)
	return 0
}
