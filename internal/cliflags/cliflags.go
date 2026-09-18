// Package cliflags is this family's one answer to `<tool> <verb> --help`.
//
// Every binary here parses a sub-verb's flags with a flag.FlagSet in
// flag.ContinueOnError mode whose output is io.Discard, because package flag is
// not allowed to print an argument the binary did not author (internal/oneline
// and its audit are the reason). That combination has one cost nobody chose: a
// person who types `--help` after a verb gets the flag package's sentinel back
// as a parse failure, so the tool prints `flag: help requested` -- the package's
// own internals, leaked to somebody who asked a reasonable question -- and exits
// non-zero. The darwin dogfood pass hit it on 2026-09-18 and #1336 fixed it verb
// by verb inside internal/release and internal/update; this package is that fix
// with the shape taken out, so the same two lines answer it everywhere and a
// class test in internal/ci can insist on them.
//
// Nothing here prints an argument. Usage returns lines of the block it was
// handed -- a constant in the calling package -- and never echoes the verb it
// was asked about, so a verb read off the command line cannot reach a stream
// through it.
package cliflags

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// Help answers a help request that came back out of (*flag.FlagSet).Parse.
//
// It reports whether err is the flag package's help sentinel. When it is, each
// non-empty usage section is written to out and the caller returns 0, because
// asking what a verb takes is not an error. When it is not, Help writes nothing
// and returns false, and the caller refuses the parse as it always did:
//
//	if err := f.Parse(args); err != nil {
//		if cliflags.Help(out, err, cliflags.Usage(usage, "harvest")) {
//			return 0
//		}
//		return refusal(errs, "HARVEST", ...)
//	}
//
// Help goes to out, not errs: it is the answer to the question that was asked,
// not a complaint about it, and a person redirecting stdout expects to catch it.
func Help(out io.Writer, err error, usage ...string) bool {
	if !errors.Is(err, flag.ErrHelp) {
		return false
	}
	for _, section := range usage {
		if section = strings.TrimRight(section, "\n"); section != "" {
			fmt.Fprintln(out, section)
		}
	}
	return true
}

// AskedFor reports whether one verb's raw arguments are a request for help: a
// bare -h, -help or --help among them, before the `--` terminator that ends the
// flags.
//
// Prefer Help wherever the parse error is handled with a stream to answer on,
// because package flag decides the same question knowing which flags take a
// value. AskedFor exists for the tools whose verbs share one parse helper --
// nova-bus, nova-board, nova-check, nova-fuse, nova-memory, nova-merge,
// nova-pulse, nova-cairn, nova-swarm and nova-wake each hand their flag set to a
// helper that answers "usable or not" over stderr and has no way to say
// "answered, exit 0" -- where the question must be asked at the dispatcher,
// before the flag set exists. Threading a stdout through those helpers would
// touch some eighty call sites to change one line of behaviour.
//
// The two rules part company only when a flag's VALUE is literally -h or --help.
// No flag in this family takes a value like that: they take paths, names,
// numbers and durations.
func AskedFor(args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

// Usage picks the lines of a tool's usage block that describe one verb, so that
// `--help` on a verb answers about THAT verb. A person who asked about `adopt`
// did not ask to re-read `cut`.
//
// A verb's line is one whose leading words -- the tokens before the first flag,
// bracket, parenthesis or placeholder -- are EXACTLY the tool's name followed by
// the words of verb, at any indentation. Exactly, rather than merely beginning
// with them, is what keeps prose out: `one line in, one line out over the socket
// --session names.` leads with nine words, and a looser rule answers `--help` on
// a verb named `line` with a sentence. The wrapped lines under a match are
// carried along, since a line that opens with a flag, a bracket or a
// parenthesis continues the line above rather than naming a verb of its own,
// which is how the multi-line entries in cmd/nova-work's block stay whole.
//
// When no line matches, the whole block is returned. A stale or unusual verb
// name -- or a usage line that trails prose after its flags -- is a reason to
// show somebody everything, never a reason to show them nothing.
func Usage(block, verb string) string {
	if lines, ok := pick(block, verb); ok {
		return lines
	}
	return strings.TrimRight(block, "\n")
}

// Answer is Usage and Help in one, for a dispatcher that must decide before any
// flag set exists. It prints the usage of the verb in args[0] -- or of the
// two-word verb in args[0] and args[1] -- when the arguments after it ask for
// help, and reports whether it did.
//
// It answers only for a verb the block actually names, so `help`, `version` and
// an unknown verb are left to the dispatcher, which has its own answers for
// those: a person who asks for help on a verb that does not exist should be told
// it does not exist.
func Answer(out io.Writer, block string, args []string) bool {
	if len(args) < 2 {
		return false
	}
	if len(args) > 2 && !strings.HasPrefix(args[1], "-") && AskedFor(args[2:]) {
		if lines, ok := pick(block, args[0]+" "+args[1]); ok {
			fmt.Fprintln(out, lines)
			return true
		}
	}
	if !AskedFor(args[1:]) {
		return false
	}
	lines, ok := pick(block, args[0])
	if !ok {
		return false
	}
	fmt.Fprintln(out, lines)
	return true
}

// pick is Usage without the fallback: the lines naming verb, and whether the
// block named it at all.
func pick(block, verb string) (string, bool) {
	want := strings.Fields(verb)
	if len(want) == 0 {
		return "", false
	}
	var picked []string
	carrying := false
	for _, line := range strings.Split(block, "\n") {
		head := leadingWords(line)
		if len(head) == 0 {
			// A continuation: it belongs to the line above, whichever that was.
			if carrying && strings.TrimSpace(line) != "" {
				picked = append(picked, strings.TrimRight(line, " \t"))
			}
			continue
		}
		carrying = len(head) == 1+len(want) && sameWords(head[1:], want)
		if carrying {
			picked = append(picked, strings.TrimRight(line, " \t"))
		}
	}
	if len(picked) == 0 {
		return "", false
	}
	return strings.Join(picked, "\n"), true
}

// leadingWords is the words of a line before its first flag, bracket,
// parenthesis, placeholder or comment: the part that can name a tool and a verb.
// A line that opens with one of those has no leading words at all, which is what
// marks it a wrapped continuation of the line above.
func leadingWords(line string) []string {
	var head []string
	for _, tok := range strings.Fields(line) {
		if strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "[") ||
			strings.HasPrefix(tok, "(") || strings.HasPrefix(tok, "<") ||
			strings.HasPrefix(tok, "#") {
			break
		}
		head = append(head, tok)
	}
	return head
}

func sameWords(got, want []string) bool {
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
