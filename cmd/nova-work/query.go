package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func queryVerb(verb string, args []string, stdout, stderr io.Writer) int {
	specs := verbFlags[verb]
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	strs := map[string]*string{}
	for _, s := range specs {
		strs[s.name] = f.String(s.name, "", "")
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
	fmt.Fprintf(&b, "%s", oneline.Escape(verb))
	for _, s := range specs {
		if v := *strs[s.name]; v != "" {
			fmt.Fprintf(&b, " --%s %s", oneline.Field(s.name), oneline.Field(v))
		}
	}
	return ask(socket, b.String(), stdout, stderr)
}
