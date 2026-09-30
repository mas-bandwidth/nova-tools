package main

import (
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
	run := "; run: nova-sandbox help"
	if verb != "" {
		run += " " + verb
	}
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
