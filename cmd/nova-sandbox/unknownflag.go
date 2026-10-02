package main

import (
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// unknownArg is the refusal for one argument the verb's flag table does not match, worded
// once: `unknown flag --x; run: nova-sandbox help <verb>`. A flag this tool does not have
// may still have been given a value (`--wrte ./x`), and that value is not a second
// mistake, so the second return is how many of the following arguments it took: one when
// the next argument is not itself a flag. An argument that is no flag at all is
// `unexpected argument x`. The flags after `--` are the command's and never come here.
// verb is the verb whose help to run; "" names the bare form, whose help is the tool's.
func unknownArg(args []string, i int, verb string) (text string, took int) {
	a := args[i]
	run := helpRun(verb)
	if !strings.HasPrefix(a, "-") || a == "-" {
		return "unexpected argument " + oneline.Escape(a) + run, 0
	}
	if name, _, ok := strings.Cut(a, "="); ok {
		a = name // `--x=y` is the flag --x given a value
	} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && args[i+1] != "--" {
		took = 1
	}
	return "unknown flag " + oneline.Escape(a) + run, took
}

// helpRun is the remedy a refusal of the verb's argv names: the verb's help, or for the
// bare form ("") the tool's.
func helpRun(verb string) string {
	if verb == "" {
		return "; run: nova-sandbox help"
	}
	return "; run: nova-sandbox help " + verb
}

// setSwitch reads a as one of the switches in sw (keyed --name) the way Go's flag package
// reads a bool flag, as every skeleton tool's verbs and reap read theirs: -name or --name
// sets it, and -name=<v> or --name=<v> sets it to any v strconv.ParseBool takes, so
// --json=false is no JSON. ok is false when a is none of them; bad is the refusal of a
// value that is no boolean.
func setSwitch(a, verb string, sw map[string]*bool) (bad string, ok bool) {
	name, ok := strings.CutPrefix(a, "--")
	if !ok {
		name, ok = strings.CutPrefix(a, "-")
	}
	name, v, valued := strings.Cut(name, "=")
	p := sw["--"+name]
	if !ok || p == nil {
		return "", false
	}
	on, err := strconv.ParseBool(v)
	switch {
	case !valued:
		on = true
	case err != nil:
		return "--" + name + " wants true or false, got " + oneline.Escape(v) + helpRun(verb), true
	}
	*p = on
	return "", true
}
