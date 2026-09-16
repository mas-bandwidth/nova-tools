package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// renderFlags is the render verb's flag surface in the spec's own order
// (docs/SPEC-WORK.md, the usage fence). --fixed is repeatable; --chat, --file
// and --check are switches serialized as --name true when set. The forms are
// the session's to enforce (--chat exclusive with --file/--check); the client
// spells what the caller gave and lets the session refuse with its own naming.
var renderFlags = []flagSpec{
	{name: "session"},
	{name: "view"},
	{name: "chat", bool: true},
	{name: "projection"},
	{name: "row-axis"},
	{name: "column-axis"},
	{name: "fixed", multi: true},
	{name: "file", bool: true},
	{name: "check", bool: true},
	{name: "at"},
}

// queryFlags is the query verb's flag surface in the spec's own order. Every
// flag is a string, because the session validates the ask's own requirements
// (percent needs --axis, who and stale need --window) and refuses with its own
// naming; a thin client never second-guesses the engine's bounds.
var queryFlags = []flagSpec{
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

// renderVerb and queryVerb are the read-only socket verbs of the render slice:
// they hand the caller's flags to the session over --session's socket and print
// the one line the session answers, byte for byte.
func renderVerb(args []string, stdout, stderr io.Writer) int {
	return socketVerb("render", renderFlags, args, stdout, stderr)
}

func queryVerb(args []string, stdout, stderr io.Writer) int {
	return socketVerb("query", queryFlags, args, stdout, stderr)
}

// socketVerb is sessionVerb's shape for any verb: parse the flags in the verb's
// own order, require --session (the socket has no default path), then send one
// request line and print the one reply line the session answers.
func socketVerb(verb string, specs []flagSpec, args []string, stdout, stderr io.Writer) int {
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
