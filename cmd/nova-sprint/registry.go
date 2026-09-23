package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// Verb files register themselves in init. Adding a verb never edits main.go.
type Verb struct {
	Name    string
	Summary string
	Run     func(context.Context, []string, io.Writer, io.Writer) int
}

var verbs = map[string]Verb{}

func register(v Verb) {
	if v.Name == "" || strings.ContainsAny(v.Name, " \t\n") || v.Run == nil {
		panic("invalid nova-sprint verb")
	}
	if _, exists := verbs[v.Name]; exists {
		panic(fmt.Sprintf("duplicate nova-sprint verb %q", v.Name))
	}
	verbs[v.Name] = v
}

func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "nova-sprint: a verb is required; run: nova-sprint help")
		return 2
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintln(out, buildinfo.Line("nova-sprint", version))
		return 0
	}
	if args[0] == "help" {
		fmt.Fprintln(out, "nova-sprint: Redis sprint coordination")
		fmt.Fprintln(out, "usage: nova-sprint <verb> [args]")
		fmt.Fprintln(out, "verbs:")
		names := make([]string, 0, len(verbs))
		for name := range verbs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(out, "  %s  %s\n", name, verbs[name].Summary)
		}
		fmt.Fprintln(out, "example:")
		fmt.Fprintln(out, "nova-sprint help")
		fmt.Fprintln(out, "nova-sprint version")
		return 0
	}
	v, ok := verbs[args[0]]
	if !ok {
		fmt.Fprintf(errOut, "nova-sprint: unknown verb %q; run: nova-sprint help\n", args[0])
		return 2
	}
	return v.Run(ctx, args[1:], out, errOut)
}
