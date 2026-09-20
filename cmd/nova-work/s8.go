package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// s8VerbFlags registers the query verb's flags. The query verb is the S8
// slice: "nova-work query friends and escalations over the socket". The
// spec (docs/SPEC-WORK.md, "The verbs") pins the flags and constraints;
// this client sends them as the caller spelled them, because the session
// validates and a thin client never second-guesses the engine's bounds.
func init() {
	verbFlags["query"] = []flagSpec{
		{name: "session"},
		{name: "snapshot"},
		{name: "max-bytes"},
		{name: "max-depth"},
		{name: "max-nodes"},
		{name: "cache"},
		{name: "ask"},
		{name: "branch"},
		{name: "node"},
		{name: "repo"},
		{name: "owner"},
		{name: "category"},
		{name: "axis"},
		{name: "for"},
		{name: "class"},
		{name: "since"},
		{name: "at"},
		{name: "from"},
		{name: "to"},
		{name: "after"},
		{name: "page-budget"},
		{name: "max"},
		{name: "order"},
		{name: "window"},
	}
}

// validAskKinds is the spec's own closed list of --ask values
// (docs/SPEC-WORK.md, the query verb line).
var validAskKinds = map[string]bool{
	"done":      true,
	"remaining": true,
	"who":       true,
	"percent":   true,
	"size":      true,
	"stream":    true,
	"under":     true,
	"stale":     true,
	"handoffs":  true,
	"roadmap":   true,
	"friends":   true,
	"models":    true,
	"ready":     true,
	"fleet":     true,
	"routes":    true,
	// reports stands on the spec's own --ask line and is marked SPEC-AHEAD
	// (nova-tools#854) there. The client carries it because help prints that
	// line verbatim, and a help naming an ask the client itself refuses is
	// worse than a session refusing one it does not answer yet.
	"reports": true,
}

// openOnlyAsks are the asks the spec admits under --branch open and nowhere
// else: "who, stale, handoffs and reports: --branch open only, the other two
// exit 2".
var openOnlyAsks = map[string]bool{
	"who":      true,
	"stale":    true,
	"handoffs": true,
	"reports":  true,
}

func queryVerb(args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("query", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	strs := map[string]*string{}
	for _, s := range verbFlags["query"] {
		strs[s.name] = f.String(s.name, "", "")
	}
	if err := f.Parse(args); err != nil {
		return refused(stderr, "query: "+err.Error())
	}
	if f.NArg() != 0 {
		return refused(stderr, fmt.Sprintf("query takes no positional arguments (got %q)", f.Arg(0)))
	}

	session := *strs["session"]
	snapshot := *strs["snapshot"]
	if session == "" && snapshot == "" {
		return refused(stderr, "--session or --snapshot is required; refusing to guess")
	}
	if session != "" && snapshot != "" {
		return refused(stderr, "--session and --snapshot are mutually exclusive")
	}

	// Snapshot requires the three bounds and --cache.
	if snapshot != "" {
		if *strs["max-bytes"] == "" {
			return refused(stderr, "--snapshot requires --max-bytes")
		}
		if *strs["max-depth"] == "" {
			return refused(stderr, "--snapshot requires --max-depth")
		}
		if *strs["max-nodes"] == "" {
			return refused(stderr, "--snapshot requires --max-nodes")
		}
		if *strs["cache"] == "" {
			return refused(stderr, "--snapshot requires --cache")
		}
	}

	askKind := *strs["ask"]
	if askKind == "" {
		return refused(stderr, "--ask is required")
	}
	if !validAskKinds[askKind] {
		return refused(stderr, fmt.Sprintf("--ask %q is not one of the known kinds (done, remaining, who, percent, size, stream, under, stale, handoffs, roadmap, friends, models, ready, fleet, routes, reports)", askKind))
	}

	branch := *strs["branch"]
	if branch == "" {
		return refused(stderr, "--branch is required")
	}
	if branch != "open" && branch != "closed" && branch != "root" {
		return refused(stderr, fmt.Sprintf("--branch %q is not one of open, closed, root", branch))
	}

	// Constraint: who, stale, handoffs and reports are --branch open only
	// ("who, stale, handoffs and reports: --branch open only, the other two
	// exit 2").
	if openOnlyAsks[askKind] && branch != "open" {
		return refused(stderr, "--ask "+askKind+" is --branch open only; --branch "+branch+" is refused")
	}

	// Constraint: reports requires --since ("reports: --since <revision>,
	// required (SPEC-AHEAD: #854)").
	if askKind == "reports" && *strs["since"] == "" {
		return refused(stderr, "--ask reports requires --since <revision>")
	}

	// Constraint: --class is the routes ask's own ("routes: --class optional,
	// the card class whose ordered route list the projection emits"), and
	// names nothing on any other ask.
	if *strs["class"] != "" && askKind != "routes" {
		return refused(stderr, "--class is refused on --ask "+askKind+" (only --ask routes admits it)")
	}

	// Constraint: --branch closed and --branch root require --from and --to.
	if (branch == "closed" || branch == "root") && !openOnlyAsks[askKind] {
		if *strs["from"] == "" || *strs["to"] == "" {
			return refused(stderr, "--branch "+branch+" requires --from and --to")
		}
	}

	// Constraint: --from and --to are refused under --branch open.
	if branch == "open" && (*strs["from"] != "" || *strs["to"] != "") {
		return refused(stderr, "--from and --to are refused under --branch open")
	}

	// Constraint: who and stale require --window.
	if (askKind == "who" || askKind == "stale") && *strs["window"] == "" {
		return refused(stderr, "--ask "+askKind+" requires --window")
	}

	// Constraint: --order priority on any ask other than ready is exit 2.
	if *strs["order"] == "priority" && askKind != "ready" {
		return refused(stderr, "--order priority is refused on --ask "+askKind+" (only --ask ready admits it)")
	}

	// Constraint: percent with --axis is required on a matrix and refused on a zero- or one-axis roadmap.
	// The client cannot know the roadmap shape, so it only enforces that --axis is not given for non-percent asks.
	if askKind != "percent" && *strs["axis"] != "" {
		return refused(stderr, "--axis is refused on --ask "+askKind)
	}

	// Constraint: fleet --for with --node on a member that excludes the kind is refused.
	// The client cannot know member exclusions, so it passes both to the session.

	// --snapshot names a published snapshot FILE the offline reader opens under
	// its own three bounds and its own --cache; it is not a session and there is
	// no socket to dial. This client does not carry that reader yet, so it
	// refuses here rather than handing the file to the socket dialler -- which
	// sent a reader after a session that was never there, as
	// `cannot reach <file>: ... connect: socket operation on non-socket`
	// (nova-tools#1787).
	if snapshot != "" {
		return refused(stderr, "query --snapshot reads a published snapshot file with the offline reader, which this client does not carry yet; use --session <path> for the resident session")
	}

	socket := session

	var b strings.Builder
	fmt.Fprintf(&b, "%s", "query")
	for _, s := range verbFlags["query"] {
		if v := *strs[s.name]; v != "" {
			fmt.Fprintf(&b, " --%s %s", oneline.Field(s.name), oneline.Field(v))
		}
	}
	return ask(socket, b.String(), askTimeout, stdout, stderr)
}
