package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Verb files register themselves in init. Adding a verb never edits main.go.
type Verb struct {
	Name    string
	Summary string
	Run     func(context.Context, []string, io.Writer, io.Writer) int
}

var verbs = map[string]Verb{}

func register(v Verb) {
	if v.Name == "" || strings.ContainsAny(v.Name, " \t\n") || strings.HasPrefix(v.Name, "-") || v.Run == nil {
		panic("invalid nova-sprint verb")
	}
	switch v.Name {
	case "help", "version", "table", "refresh":
		panic(fmt.Sprintf("reserved nova-sprint verb %q", v.Name))
	}
	if _, exists := verbs[v.Name]; exists {
		panic(fmt.Sprintf("duplicate nova-sprint verb %q", v.Name))
	}
	verbs[v.Name] = v
}

func runRegistered(name string, args []string, out, errOut io.Writer) (int, bool) {
	v, ok := verbs[name]
	if !ok {
		return 0, false
	}
	return v.Run(context.Background(), args, out, errOut), true
}

func usageWithRegisteredVerbs() string {
	if len(verbs) == 0 {
		return usage
	}
	names := make([]string, 0, len(verbs))
	for name := range verbs {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("\nregistered verbs:\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  %s  %s\n", name, verbs[name].Summary)
	}
	return strings.Replace(usage, "\nexample:\n", b.String()+"\nexample:\n", 1)
}
