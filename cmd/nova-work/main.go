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
// Every path comes from a flag. There is no default file and no discovery: a missing
// flag is a refusal, never a guess. Output is one line per verb. Exit 0 ran and passed;
// exit 2 could not run -- a missing flag, an unreadable graph or plan, a :deps cycle, an
// unknown node, a refusal.
//
// nova-work is also the work layer's event bridge. This binary's shipped verb, events,
// turns the cards:done stream and the gh fallback poll into the pub/sub messages the
// merge layer reacts to (docs/SPEC-JOBS.md, "Events, not ticks"). It makes no model call
// and writes no record: every message is a signal, and git stays the record.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/jobs"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

var version string

const usage = `nova-work: the job graph and the bounded .work reader (see docs/SPEC-JOBS.md, docs/SPEC-WORKLANG.md)

usage:
  nova-work version
  nova-work dependencies --graph <file> [--node <id> --needs <id>[,<id>...]]
  nova-work ready --node X --graph <file>
  nova-work clip --worktree <dir> --branch <name> --base <ref> --harvest <dir> [--result <file>] [--message <text>]
  nova-work plan check --file <path.work> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work plan expand --file <path.work> --out <dir> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work events --redis <addr> [--repo <owner>/<name>] [--base <branch>] [--gh-poll 60s] (--once | --deadline <duration>)

verbs:
  nova-work dependencies   owns the graph (:deps, refused acyclic at seed by validator rule 3)
  nova-work ready --node X is the ready set
  nova-work clip           commits the card's branch, harvests its result, resets the worktree to base
  nova-work plan check     reads a .work plan as data and closes its needs/blocks graph, never as a program
  nova-work plan expand    writes one card directory per hand-written :node, refusing a cycle or an absent need
  nova-work events         bridges the events, not ticks (cards:done stream + gh fallback poll)

A node is ready only when every need is terminal accepted, and every row that cannot
proceed prints its exact blocker and its resolver. A :deps cycle is refused before
publication, so the ready set is finite and the graph can never deadlock.

A plan is read as data, never as a program: a ` + "`#.`" + ` dispatch macro anywhere in code
position is refused at exit 2 naming its byte offset, string and comment text is opaque,
and an unknown :kind is refused naming the field. :needs is the reference edge and
:blocks its inverse, so the kernel derives whichever a node did not give; an absent
need is refused naming the field and the id, and a :needs cycle is refused by validator
rule 3, both at load before the graph is published.

events publishes the family's three event channels from two sources: the cards:done
stream (consumer group events) becomes card-done, and a poll of gh every --gh-poll
becomes pr-checks-done on a changed check-suite conclusion and dev-moved on a changed
base head. The poll is the fallback heartbeat until the forge pushes a webhook; a quiet
poll publishes nothing. Without --repo only the stream is bridged.

--once reads the stream and polls the forge once, then exits. The loop form requires
--deadline and returns when it is reached.

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

exit codes: 0 ran and passed; 2 could not run (bad invocation, an unreadable graph or
plan, a :deps cycle, an unknown node, a refusal).

example:
  nova-work dependencies --graph ./deps.json --node b
  nova-work dependencies --graph ./deps.json --node a --needs b
  nova-work ready --node a --graph ./deps.json
  nova-work plan check --file ./work.work --max-bytes 65536
  nova-work events --redis 127.0.0.1:6379 --once
`

// refuse is what an unusable invocation or an unreadable plan costs: one line naming
// what was wrong and the door to the usage, never the banner itself.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-work%s: %s; run: nova-work help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

// Deps is everything this binary reaches outside itself, injected so the tests drive a
// miniredis and a fake forge and reach no network.
type Deps struct {
	Now   func() time.Time
	Dial  func(addr string) *redis.Client
	Forge func(repo, base string, timeout time.Duration) ci.Forge
}

func production() Deps {
	return Deps{
		Now:  func() time.Time { return time.Now().UTC() },
		Dial: func(addr string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		Forge: func(repo, base string, timeout time.Duration) ci.Forge {
			return ci.NewGHForge(repo, base, timeout)
		},
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, production())) }

func run(args []string, stdout, stderr io.Writer, deps ...Deps) int {
	d := production()
	if len(deps) > 0 {
		d = deps[0]
	}
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `plan check` reads a plan and `ready --node X` only looks")
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		return cmdVersion(args[1:], stdout, stderr)
	case "dependencies":
		return cmdDependencies(args[1:], stdout, stderr)
	case "ready":
		return cmdReady(args[1:], stdout, stderr)
	case "clip":
		return cmdClip(args[1:], stdout, stderr)
	case "plan":
		return cmdPlan(args[1:], stdout, stderr)
	case "events":
		return cmdEvents(args[1:], stdout, stderr, d)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
}

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		return refuse(stderr, " version", fmt.Sprintf("takes no flags and no arguments, got %d", len(args)))
	}
	// buildinfo.Line already renders each field through oneline.Field; Escape here is
	// the source-level tripwire's proof that this print site is escaped, and it leaves
	// the line's deliberate spaces between fields intact.
	fmt.Fprintln(stdout, oneline.Escape(buildinfo.Line("nova-work", version)))
	return 0
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

// cmdEvents is the events verb. Its flags are parsed with flag's usage dump discarded, so
// a bad value is one refusal line and not a banner.
func cmdEvents(args []string, stdout, stderr io.Writer, deps Deps) int {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	addr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	base := fs.String("base", "dev", "")
	ghPoll := fs.String("gh-poll", "60s", "")
	deadline := fs.String("deadline", "", "")
	consumer := fs.String("consumer", "", "")
	once := fs.Bool("once", false, "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "nova-work events: %s; run: nova-work help\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-work events: takes no positional arguments, got %d; run: nova-work help\n", fs.NArg())
		return 2
	}
	if *addr == "" {
		fmt.Fprintf(stderr, "nova-work events: --redis is required; it wants the address of the pub/sub instance; run: nova-work help\n")
		return 2
	}
	poll, err := time.ParseDuration(*ghPoll)
	if err != nil || poll <= 0 {
		fmt.Fprintf(stderr, "nova-work events: --gh-poll is a positive duration such as 60s, got %q; run: nova-work help\n", oneline.Escape(*ghPoll))
		return 2
	}
	var bound time.Duration
	if *deadline != "" {
		if bound, err = time.ParseDuration(*deadline); err != nil || bound <= 0 {
			fmt.Fprintf(stderr, "nova-work events: --deadline is a positive duration, got %q; run: nova-work help\n", oneline.Escape(*deadline))
			return 2
		}
	}
	if !*once && bound <= 0 {
		fmt.Fprintf(stderr, "nova-work events: the loop form requires --deadline; a loop with no deadline is a process nobody can tell from a stuck one; run: nova-work help\n")
		return 2
	}

	rdb := deps.Dial(*addr)
	defer rdb.Close()

	var forge ci.Forge
	if *repo != "" {
		forge = deps.Forge(*repo, *base, poll)
	}
	p := ci.NewProducer(rdb, forge, *consumer, stderr)

	ctx := context.Background()
	if bound > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, bound)
		defer cancel()
	}

	if *once {
		cards, err := p.PublishCardsDone(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "nova-work events: %s\n", oneline.Err(err))
			return 1
		}
		polls := 0
		if forge != nil {
			polls, err = p.PollOnce(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "nova-work events: %s\n", oneline.Err(err))
				return 1
			}
		}
		fmt.Fprintf(stdout, "EVENTS OK once=true card-done=%d published=%d\n", cards, polls)
		return 0
	}
	if err := p.Run(ctx, poll); err != nil && err != context.DeadlineExceeded {
		fmt.Fprintf(stderr, "nova-work events: %s\n", oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "EVENTS OK once=false deadline=%s\n", oneline.Field(bound.String()))
	return 0
}
