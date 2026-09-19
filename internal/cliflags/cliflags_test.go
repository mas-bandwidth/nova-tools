package cliflags

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"testing"
)

// The block below is the shape every tool's usage constant has in this
// repository: one line per verb, some of them two words, some wrapped onto
// continuation lines, and prose mixed in that names no verb at all.
const block = `nova-thing: what this tool is (see docs/SPEC-THING.md)

usage:
  nova-thing harvest --id <pulse id> --root <dir> [--max <n>]
  nova-thing plan check --file <path.work> [--max-bytes <n>]
  nova-thing plan expand --file <path.work> --out <dir>
  nova-thing session start --session <path> --as <name>
                           --max-bytes <n> --max-depth <n>
                           (--ask is one of: done, remaining)
  nova-thing help

wire:
  one line in, one line out over the socket --session names.`

func TestUsagePicksOneVerb(t *testing.T) {
	got := Usage(block, "harvest")
	want := "  nova-thing harvest --id <pulse id> --root <dir> [--max <n>]"
	if got != want {
		t.Errorf("Usage(harvest) = %q, want %q", got, want)
	}
}

// A two-word verb must not answer with its sibling: `plan check` and `plan
// expand` share a first word, and a person who asked about one did not ask to
// read the other.
func TestUsageDistinguishesTwoWordVerbs(t *testing.T) {
	got := Usage(block, "plan check")
	if !strings.Contains(got, "plan check") {
		t.Fatalf("Usage(plan check) = %q, want the plan check line", got)
	}
	if strings.Contains(got, "plan expand") {
		t.Errorf("Usage(plan check) = %q, want no plan expand line", got)
	}
}

// A verb whose entry wraps keeps its wrapped lines: the flags on them are the
// verb's own, and half a usage line is worse than none.
func TestUsageCarriesContinuationLines(t *testing.T) {
	got := Usage(block, "session start")
	for _, want := range []string{"--session <path>", "--max-depth <n>", "(--ask is one of"} {
		if !strings.Contains(got, want) {
			t.Errorf("Usage(session start) = %q, want it to carry %q", got, want)
		}
	}
	if strings.Contains(got, "one line in") {
		t.Errorf("Usage(session start) = %q, want the prose below the block left out", got)
	}
}

// Prose in the block names no verb, so a word out of it never answers as one.
func TestUsageIgnoresProse(t *testing.T) {
	got := Usage(block, "line")
	if got != strings.TrimRight(block, "\n") {
		t.Errorf("Usage(line) picked %q out of the prose; want the whole block", got)
	}
}

// An unknown verb shows everything. Somebody asking is a reason to answer.
func TestUsageFallsBackToTheWholeBlock(t *testing.T) {
	if got := Usage(block, "nosuchverb"); got != strings.TrimRight(block, "\n") {
		t.Errorf("Usage(nosuchverb) = %q, want the whole block", got)
	}
	if got := Usage(block, ""); got != strings.TrimRight(block, "\n") {
		t.Errorf("Usage(empty) = %q, want the whole block", got)
	}
}

// Usage returns lines of the block it was handed and never the verb it was
// asked about, so a verb read off the command line cannot reach a stream
// through it. This is the property the oneline audit would otherwise have to
// take on trust.
func TestUsageNeverEchoesTheVerb(t *testing.T) {
	for _, verb := range []string{"$(rm -rf /)", "a\nb", "\x1b]0;title\x07", "harvest extra"} {
		got := Usage(block, verb)
		if !strings.Contains(block, got) {
			t.Errorf("Usage(%q) returned text that is not in the block: %q", verb, got)
		}
	}
}

func TestAskedForReadsTheThreeSpellings(t *testing.T) {
	yes := [][]string{{"-h"}, {"-help"}, {"--help"}, {"--root", "/tmp", "--help"}, {"--help", "--root", "/tmp"}}
	for _, args := range yes {
		if !AskedFor(args) {
			t.Errorf("AskedFor(%v) = false, want true", args)
		}
	}
	no := [][]string{nil, {}, {"--root", "/tmp"}, {"--title", "-help-me"}, {"help"}, {"--", "-h"}, {"--", "--help"}}
	for _, args := range no {
		if AskedFor(args) {
			t.Errorf("AskedFor(%v) = true, want false", args)
		}
	}
}

// AskedFor and package flag must agree on the invocations this family actually
// takes, because half the tools ask one and half ask the other and a person
// should not be able to tell which by the answer they get.
func TestAskedForAgreesWithTheFlagPackage(t *testing.T) {
	for _, args := range [][]string{
		{"-h"}, {"-help"}, {"--help"},
		{"--root", "/tmp"}, {"--root", "/tmp", "--help"}, {"--max", "5", "-h"},
		{}, {"--root", "/tmp", "--max", "5"},
	} {
		fs := flag.NewFlagSet("verb", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.String("root", "", "a directory")
		fs.Int("max", 20, "a cap")
		byFlag := errors.Is(fs.Parse(args), flag.ErrHelp)
		if got := AskedFor(args); got != byFlag {
			t.Errorf("%v: AskedFor = %v, package flag = %v", args, got, byFlag)
		}
	}
}

func TestHelpAnswersTheSentinelAndNothingElse(t *testing.T) {
	var out bytes.Buffer
	if !Help(&out, flag.ErrHelp, "  nova-thing harvest --id <pulse id>") {
		t.Fatal("Help(flag.ErrHelp) = false, want true")
	}
	if got := out.String(); got != "  nova-thing harvest --id <pulse id>\n" {
		t.Errorf("Help printed %q", got)
	}

	out.Reset()
	if Help(&out, errors.New("flag provided but not defined: -nope"), "usage") {
		t.Error("Help on a real parse failure = true, want false")
	}
	if out.Len() != 0 {
		t.Errorf("Help on a real parse failure printed %q, want nothing", out.String())
	}

	out.Reset()
	if Help(&out, nil, "usage") {
		t.Error("Help(nil) = true, want false")
	}
}

// A wrapped sentinel still answers: a call site that annotates the parse error
// before handing it over must not turn a question into a refusal.
func TestHelpSeesAWrappedSentinel(t *testing.T) {
	var out bytes.Buffer
	if !Help(&out, fmt.Errorf("parsing flags: %w", flag.ErrHelp), "usage") {
		t.Error("Help on a wrapped flag.ErrHelp = false, want true")
	}
}

// Several sections print in order, and an empty one prints nothing: a verb with
// a note after its usage line reads as one answer, and a verb without a note
// does not gain a blank line.
func TestHelpPrintsEachSectionOnce(t *testing.T) {
	var out bytes.Buffer
	Help(&out, flag.ErrHelp, "line one\n", "", "line two")
	if got := out.String(); got != "line one\nline two\n" {
		t.Errorf("Help printed %q, want %q", got, "line one\nline two\n")
	}
}

// The end to end shape: a FlagSet built the way every verb in this repository
// builds one answers --help and -h with its usage, at exit 0, and still refuses
// an undefined flag.
func TestParseThenHelpIsTheWholeContract(t *testing.T) {
	run := func(args []string) (string, int) {
		var out bytes.Buffer
		fs := flag.NewFlagSet("harvest", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var id string
		fs.StringVar(&id, "id", "", "the pulse id")
		if err := fs.Parse(args); err != nil {
			if Help(&out, err, Usage(block, "harvest")) {
				return out.String(), 0
			}
			return out.String(), 2
		}
		return out.String(), 0
	}
	for _, args := range [][]string{{"--help"}, {"-h"}, {"-help"}} {
		got, code := run(args)
		if code != 0 {
			t.Errorf("%v: exit %d, want 0", args, code)
		}
		if !strings.Contains(got, "nova-thing harvest --id") {
			t.Errorf("%v: printed %q, want the harvest usage", args, got)
		}
	}
	if got, code := run([]string{"--nope"}); code != 2 || got != "" {
		t.Errorf("--nope: exit %d output %q, want exit 2 and nothing on out", code, got)
	}
}

func TestAnswerServesOnlyVerbsTheBlockNames(t *testing.T) {
	var out bytes.Buffer
	if !Answer(&out, block, []string{"harvest", "--help"}) {
		t.Fatal("Answer(harvest --help) = false, want true")
	}
	if !strings.Contains(out.String(), "nova-thing harvest --id") {
		t.Errorf("Answer printed %q", out.String())
	}

	// A two-word verb is answered from its own line, not its sibling's.
	out.Reset()
	if !Answer(&out, block, []string{"plan", "expand", "-h"}) {
		t.Fatal("Answer(plan expand -h) = false, want true")
	}
	if got := out.String(); !strings.Contains(got, "plan expand") || strings.Contains(got, "plan check") {
		t.Errorf("Answer(plan expand) printed %q", got)
	}

	// Left to the dispatcher: an unknown verb, a verb with no help among its
	// arguments, and a bare invocation.
	for _, args := range [][]string{
		{"nosuchverb", "--help"}, {"harvest", "--id", "7"}, {"harvest"}, {"--help"}, {},
	} {
		out.Reset()
		if Answer(&out, block, args) {
			t.Errorf("Answer(%v) = true, want false (the dispatcher answers that one)", args)
		}
		if out.Len() != 0 {
			t.Errorf("Answer(%v) printed %q, want nothing", args, out.String())
		}
	}
}
