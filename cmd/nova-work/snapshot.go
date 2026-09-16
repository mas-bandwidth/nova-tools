// The S6 slice is the clip slice over the socket: the tree is in git, and
// others can read it. `snapshot --out <file>` asks the live session to write
// the published snapshot — the deterministic snapshot file a clip writes, the
// primary form O — to a local file, so a reader who is not the coordinator can
// point the `--snapshot <path>` forms of check and query at it. The clip verbs
// are the spec's own long-operation protocol (docs/SPEC-WORK.md, "The engine
// and its client"): `clip` acknowledges at once with one OPERATION OK line and
// exits, and it is `operation wait --id <id>` that prints the terminal
// CLIP OK / CLIP RACED / CLIP FAIL line when the transport settles;
// `operation status`, `operation list` and `operation cancel` are the bounded
// control plane around it. Every answer line is the session's own, printed byte
// for byte by the client main.go already carries.
package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// snapshotFlags is the snapshot verb's flag surface: the socket the session
// listens on and the one file the snapshot is written to. --out is required —
// no path is guessed — and the session validates everything else about the
// write with its own naming.
var snapshotFlags = []flagSpec{
	{name: "session"},
	{name: "out"},
}

// clipFlags is the clip verb's flag surface in the spec's own order
// (docs/SPEC-WORK.md, the usage fence): `--session <path> --as <name>
// --git-timeout <seconds> [--attempts <n>] [--max <n>] [--now <stamp>]`.
var clipFlags = []flagSpec{
	{name: "session"},
	{name: "as"},
	{name: "git-timeout"},
	{name: "attempts"},
	{name: "max"},
	{name: "now"},
}

// operationFlags is the operation family's flag surface in the spec's own
// order. cancel takes the write flags — `--as <name> [--request <id>]
// [--expect <rev>] [--now <stamp>] [--deadline <stamp>] [--dry-run]` — because
// a cancellation is a request with its own acknowledgement and its own final
// disposition, never an erasure.
var operationFlags = map[string][]flagSpec{
	"operation status": {
		{name: "session"},
		{name: "id"},
	},
	"operation list": {
		{name: "session"},
		{name: "max"},
	},
	"operation wait": {
		{name: "session"},
		{name: "id"},
		{name: "timeout"},
		{name: "after"},
	},
	"operation cancel": {
		{name: "session"},
		{name: "as"},
		{name: "request"},
		{name: "expect"},
		{name: "now"},
		{name: "deadline"},
		{name: "dry-run", bool: true},
		{name: "id"},
		{name: "reason"},
	},
}

func snapshotVerb(args []string, stdout, stderr io.Writer) int {
	return sliceVerb("snapshot", snapshotFlags, args, []string{"out"}, stdout, stderr)
}

func clipVerb(args []string, stdout, stderr io.Writer) int {
	return sliceVerb("clip", clipFlags, args, nil, stdout, stderr)
}

func operationVerb(sub string, args []string, stdout, stderr io.Writer) int {
	return sliceVerb("operation "+sub, operationFlags["operation "+sub], args, nil, stdout, stderr)
}

// sliceVerb is sessionVerb's shape for this slice's verbs, the flag surface
// carried in the slice's own file: parse the flags in the verb's own order,
// require --session (the socket has no default path), require each flag named
// in extra (no path is guessed), then send one request line and print the one
// reply line the session answers.
func sliceVerb(verb string, specs []flagSpec, args []string, extra []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	strs := map[string]*string{}
	bools := map[string]*bool{}
	for _, s := range specs {
		switch {
		case s.bool:
			bools[s.name] = f.Bool(s.name, false, "")
		default:
			strs[s.name] = f.String(s.name, "", "")
		}
	}
	if err := f.Parse(args); err != nil {
		return refused(stderr, verb+": "+oneline.Err(err))
	}
	if f.NArg() != 0 {
		return refused(stderr, verb+" takes no positional arguments (got "+fmt.Sprintf("%q", f.Arg(0))+")")
	}
	socket := *strs["session"]
	if socket == "" {
		return refused(stderr, "--session is required; refusing to guess (the socket has no default path)")
	}
	for _, name := range extra {
		if *strs[name] == "" {
			return refused(stderr, "--"+name+" is required; refusing to guess a path")
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s", oneline.Escape(verb))
	for _, s := range specs {
		switch {
		case s.bool:
			if *bools[s.name] {
				fmt.Fprintf(&b, " --%s true", oneline.Field(s.name))
			}
		default:
			if v := *strs[s.name]; v != "" {
				fmt.Fprintf(&b, " --%s %s", oneline.Field(s.name), oneline.Field(v))
			}
		}
	}
	return ask(socket, b.String(), stdout, stderr)
}
