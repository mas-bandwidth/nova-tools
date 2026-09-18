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
// nova-work also pushes a card into the ready set (docs/SPEC-JOBS.md, "Redis ready
// set (the pull)"). A bench pulls from the ready set itself; nothing is pushed
// to a bench by hand. This verb is the producer half: it validates the card,
// appends one entry to the `cards:ready` stream, and prints its stream id.
//
// Every path comes from a flag. There is no default file and no discovery: a missing
// flag is a refusal, never a guess. Output is one line per verb. Exit 0 ran and passed;
// exit 2 could not run -- a missing flag, an unreadable graph or plan, a :deps cycle, an
// unknown node, a refusal.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/jobs"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

var version string

const usage = `nova-work: the job graph, the bounded .work reader, and the card verbs that feed the ready set (see docs/SPEC-JOBS.md, docs/SPEC-WORKLANG.md)

usage:
  nova-work version
  nova-work dependencies --graph <file> [--node <id> --needs <id>[,<id>...]]
  nova-work ready --node X --graph <file>
  nova-work clip --worktree <dir> --branch <name> --base <ref> --harvest <dir> [--result <file>] [--message <text>]
  nova-work plan check --file <path.work> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work plan expand --file <path.work> --out <dir> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work push --redis <addr> --card <file> [--priority <n>] [--needs <id,...>]

verbs:
  nova-work dependencies   owns the graph (:deps, refused acyclic at seed by validator rule 3)
  nova-work ready --node X is the ready set
  nova-work clip           commits the card's branch, harvests its result, resets the worktree to base
  nova-work plan check     reads a .work plan as data and closes its needs/blocks graph, never as a program
  nova-work plan expand    writes one card directory per hand-written :node, refusing a cycle or an absent need
  nova-work push           appends one card to the Redis ` + "`cards:ready`" + ` stream

A node is ready only when every need is terminal accepted, and every row that cannot
proceed prints its exact blocker and its resolver. A :deps cycle is refused before
publication, so the ready set is finite and the graph can never deadlock.

A plan is read as data, never as a program: a ` + "`#.`" + ` dispatch macro anywhere in code
position is refused at exit 2 naming its byte offset, string and comment text is opaque,
and an unknown :kind is refused naming the field. :needs is the reference edge and
:blocks its inverse, so the kernel derives whichever a node did not give; an absent
need is refused naming the field and the id, and a :needs cycle is refused by validator
rule 3, both at load before the graph is published.

push reads one card file and appends it to the Redis ` + "`cards:ready`" + ` stream with
the fields id, label, body, priority, needs and pushed-at. It refuses a card
whose first line is not a RESULT line or whose label is not [A-Za-z0-9._-]+.
The label is the card file's name without its extension.

NO GUESSED ANYTHING. --redis is required because there is no default instance,
and --card is required because a card in an argument is a card in the process
table. A bench pulls work from the ready set under its own lease; this verb only
makes the card available.

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
  nova-work push --redis 127.0.0.1:6379 --card ./card.md
`

// refuse is what an unusable invocation or an unreadable plan costs: one line naming
// what was wrong and the door to the usage, never the banner itself.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-work%s: %s; run: nova-work help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now().UTC()))
}

// labelRe is the label grammar: the characters a stream entry's label may use,
// so a label can also name a directory and a card file.
var labelRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `plan check` reads a plan and `ready --node X` only looks")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		return cmdVersion(rest, stdout, stderr)
	case "dependencies":
		return cmdDependencies(rest, stdout, stderr)
	case "ready":
		return cmdReady(rest, stdout, stderr)
	case "clip":
		return cmdClip(args[1:], stdout, stderr)
	case "plan":
		return cmdPlan(args[1:], stdout, stderr)
	case "push":
		return cmdPush(rest, stdout, stderr, now)
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

func cmdPush(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	redisAddr := fs.String("redis", "", "")
	cardPath := fs.String("card", "", "")
	priority := fs.Int("priority", 0, "")
	needs := fs.String("needs", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "nova-work push: %s; run: nova-work help\n", oneline.Cap(err.Error(), oneline.TailBytes))
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-work push: takes no positional arguments, got %d (flags come before arguments)\n", fs.NArg())
		return 2
	}
	if strings.TrimSpace(*redisAddr) == "" {
		fmt.Fprintln(stderr, "nova-work push: --redis is required; it wants the address of the Redis instance that holds the ready set; run: nova-work help")
		return 2
	}
	if strings.TrimSpace(*cardPath) == "" {
		fmt.Fprintln(stderr, "nova-work push: --card is required; it wants a FILE holding the card text; run: nova-work help")
		return 2
	}
	body, err := os.ReadFile(*cardPath)
	if err != nil {
		fmt.Fprintf(stderr, "nova-work push: --card wants a readable file: %s\n", oneline.Err(err))
		return 2
	}
	label := strings.TrimSuffix(filepath.Base(*cardPath), filepath.Ext(*cardPath))
	if !labelRe.MatchString(label) {
		fmt.Fprintf(stderr, "PUSH REFUSED: the label %s is not [A-Za-z0-9._-]+, so it cannot name a card\n", oneline.Field(label))
		return 2
	}
	if first := firstLine(body); !strings.HasPrefix(first, "RESULT:") {
		fmt.Fprintf(stderr, "PUSH REFUSED: the card %s does not open with a RESULT: line; its first line is %s\n",
			oneline.Field(label), oneline.Field(oneline.Cap(first, oneline.TailBytes)))
		return 2
	}
	if *priority < 0 {
		fmt.Fprintf(stderr, "PUSH REFUSED: --priority is 0 or more, got %d; a negative priority is a typo with two readings\n", *priority)
		return 2
	}
	needList := strings.TrimSpace(*needs)
	if needList == "" {
		needList = "-"
	}

	client, err := redisq.Open(*redisAddr)
	if err != nil {
		fmt.Fprintf(stderr, "nova-work push: the ready set at %s is not reachable: %s\n", oneline.Field(*redisAddr), oneline.Err(err))
		return 2
	}
	defer client.Close()
	ctx := context.Background()
	if err := client.EnsureGroup(ctx, redisq.ReadyStream, redisq.Group, "0"); err != nil {
		fmt.Fprintf(stderr, "nova-work push: the %s group could not be made on %s: %s\n",
			oneline.Field(redisq.Group), oneline.Field(redisq.ReadyStream), oneline.Err(err))
		return 2
	}
	fields := map[string]string{
		"id": newCardID(), "label": label, "body": string(body),
		"priority": fmt.Sprint(*priority), "needs": needList,
		"pushed-at": now.UTC().Format(time.RFC3339),
	}
	streamID, err := client.Add(ctx, redisq.ReadyStream, fields)
	if err != nil {
		fmt.Fprintf(stderr, "nova-work push: the card %s could not be added to %s: %s\n",
			oneline.Field(label), oneline.Field(redisq.ReadyStream), oneline.Err(err))
		return 2
	}
	fmt.Fprintf(stdout, "PUSH OK id=%s label=%s\n", oneline.Field(streamID), oneline.Field(label))
	return 0
}

// firstLine is the card's first line with a trailing CR removed, so a CRLF card
// is not refused for its line ending.
func firstLine(body []byte) string {
	line, _, _ := strings.Cut(string(body), "\n")
	return strings.TrimRight(line, "\r")
}

// newCardID is the card's own id: random hex, so two pushes of one label are
// two cards and a slot directory can carry both.
func newCardID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("card-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
