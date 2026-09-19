package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The client's verb table is READ OUT OF THE SPEC, and this file is what makes
// that sentence true rather than a comment. docs/SPEC-WORK.md's verbs block is
// one fenced block under "## The verbs", and a thin client IS that block: the
// spec's own rule is that "The values travel as the caller spelled them and the
// session validates every one", so a verb is nothing but its name and its
// ordered flags. Parsing the block here and comparing it with verbFlags means a
// flag added to the document, a flag renamed, a flag that stops taking a value
// or starts repeating, is a red test in this package and never a silent gap in
// the client.
//
// The block's own shape, which the parser reads and nothing else:
//
//	nova-work <verb words> <flags...>      one line, continuations indented
//	--flag <value>                         a flag that takes a value
//	--flag                                 a switch, written with no <value>;
//	                                       what follows it is the end of the
//	                                       line, another flag, or one of | ) ] [
//	--flag <value> ...                     repeatable, the "..." right after it
//	<write flags>                          the block's own abbreviation, defined
//	                                       in the paragraph above it

// specVerbsBlock is where the block lives, by its fences rather than by line
// number, so the spec can grow above it.
const specVerbsHeading = "## The verbs"

var specFlagRe = regexp.MustCompile(`^--[a-z][a-z0-9-]*$`)

// writeFlagsSpelling is the block's own definition of <write flags>, quoted
// from the paragraph that opens the section: "**Every mutation verb takes
// `<write flags>` = `--as <name> [--request <id>] [--expect <rev>] [--now
// <stamp>] [--deadline <stamp>] [--dry-run]`**".
const writeFlagsSpelling = "--as <name> [--request <id>] [--expect <rev>] [--now <stamp>] [--deadline <stamp>] [--dry-run]"

// notInTheBlock are the spellings the block carries that this client does not
// send, each with the reason it does not. A line may leave the client's table
// only by standing here.
var notInTheBlock = map[string]string{
	"version":           "answered in the process, never over a socket",
	"help":              "answered in the process, never over a socket",
	"state load":        "a read-only local process: no --session, no daemon, no ownership",
	"savepoint restore": "a read-only local process: no --session, no daemon, no ownership",
	"report":            "SPEC-AHEAD (nova-tools#854): no kernel symbol answers it",
	"clip":              "the name collides with this binary's local git clip; see socketverbs.go",
}

type specVerb struct {
	name  string
	flags []flagSpec
	lines []string
}

// parseSpecVerbs reads the verbs block and returns each verb in the block's own
// order, with its flags in the order the line spells them.
func parseSpecVerbs(t *testing.T) []specVerb {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "docs", "SPEC-WORK.md"))
	if err != nil {
		t.Fatalf("read the spec: %v", err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	var (
		verbs   []specVerb
		at      = map[string]int{}
		heading bool
		fenced  bool
		closed  bool
		cur     = -1
	)
	for s.Scan() {
		line := s.Text()
		if !heading {
			heading = strings.HasPrefix(line, specVerbsHeading)
			continue
		}
		if strings.HasPrefix(line, "```") {
			if fenced {
				closed = true
				break
			}
			fenced = true
			continue
		}
		if !fenced || strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "nova-work ") {
			name := verbWords(strings.TrimPrefix(line, "nova-work "))
			if i, ok := at[name]; ok {
				cur = i
			} else {
				at[name] = len(verbs)
				cur = len(verbs)
				verbs = append(verbs, specVerb{name: name})
			}
		}
		if cur < 0 {
			continue
		}
		verbs[cur].lines = append(verbs[cur].lines, line)
		verbs[cur].flags = appendFlags(verbs[cur].flags, line)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scan the spec: %v", err)
	}
	if !closed {
		t.Fatalf("the verbs block under %q is not a closed fenced block", specVerbsHeading)
	}
	if len(verbs) < 70 {
		t.Fatalf("the verbs block yielded %d verbs; the parser has lost the block", len(verbs))
	}
	return verbs
}

// verbWords takes the verb off the front of a spec line: the words before the
// first flag, group or value.
func verbWords(rest string) string {
	var out []string
	for _, tok := range strings.Fields(rest) {
		if strings.ContainsAny(tok[:1], "-([<") {
			break
		}
		out = append(out, tok)
	}
	return strings.Join(out, " ")
}

// appendFlags reads one line's flags in order, skipping any it already holds,
// and classifies each as a value flag, a switch or a repeatable.
func appendFlags(have []flagSpec, line string) []flagSpec {
	seen := map[string]bool{}
	for _, f := range have {
		seen[f.name] = true
	}
	line = strings.Replace(line, "<write flags>", writeFlagsSpelling, 1)
	toks := strings.Fields(strings.NewReplacer(
		"(", " ( ", ")", " ) ", "[", " [ ", "]", " ] ", "|", " | ").Replace(line))
	for i, tok := range toks {
		if !specFlagRe.MatchString(tok) {
			continue
		}
		name := strings.TrimPrefix(tok, "--")
		if seen[name] {
			continue
		}
		seen[name] = true
		next, after := "", ""
		if i+1 < len(toks) {
			next = toks[i+1]
		}
		if i+2 < len(toks) {
			after = toks[i+2]
		}
		spec := flagSpec{name: name}
		switch {
		case strings.HasPrefix(next, "<") && after == "...":
			spec.multi = true
		case next == "", next == "|", next == ")", next == "]", next == "[", strings.HasPrefix(next, "--"):
			spec.bool = true
		}
		have = append(have, spec)
	}
	return have
}

func TestTheClientCarriesEveryVerbTheSpecAddressesToASession(t *testing.T) {
	var want []string
	for _, v := range parseSpecVerbs(t) {
		if _, skipped := notInTheBlock[v.name]; skipped {
			continue
		}
		want = append(want, v.name)
	}
	sort.Strings(want)
	got := allSocketVerbs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the client's socket verbs and the spec's block differ\n missing from the client: %v\n not in the spec:        %v",
			missing(want, got), missing(got, want))
	}
}

func TestEveryVerbsFlagsAreTheSpecsOwnInTheSpecsOrder(t *testing.T) {
	for _, v := range parseSpecVerbs(t) {
		if _, skipped := notInTheBlock[v.name]; skipped {
			continue
		}
		t.Run(v.name, func(t *testing.T) {
			got, ok := verbFlags[v.name]
			if !ok {
				t.Fatalf("the client has no flag surface for %q", v.name)
			}
			if !reflect.DeepEqual(got, v.flags) {
				t.Fatalf("flags differ for %q\n client: %s\n spec:   %s\n the spec's own line(s):\n%s",
					v.name, renderFlags(got), renderFlags(v.flags), strings.Join(v.lines, "\n"))
			}
		})
	}
}

// Every line the client does not send is a line somebody decided not to send,
// and the decision is written down beside the table rather than lost in a diff.
func TestEveryLineTheClientDoesNotSendSaysWhyInBothPlaces(t *testing.T) {
	inBlock := map[string]bool{}
	for _, v := range parseSpecVerbs(t) {
		inBlock[v.name] = true
	}
	for name, why := range notInTheBlock {
		if !inBlock[name] {
			t.Errorf("%q is excused from the client's table but is no longer in the spec's block", name)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("%q is excused with no reason", name)
		}
		if name == "version" || name == "help" {
			continue
		}
		if _, ok := notCarried[name]; !ok && name != "clip" {
			t.Errorf("%q is excused here but a caller who types it is told only \"unknown verb\"; give it a line in notCarried", name)
		}
	}
}

// help is where a caller looks, so every verb the client accepts is spelled
// there with the spec's own flags -- the block's line, byte for byte, but for
// the backticks a Go raw string literal cannot hold.
func TestHelpCarriesTheSpecsOwnLineForEveryVerbTheClientSends(t *testing.T) {
	for _, v := range parseSpecVerbs(t) {
		if _, skipped := notInTheBlock[v.name]; skipped {
			continue
		}
		for _, line := range v.lines {
			want := "  " + strings.ReplaceAll(line, "`", "'")
			if !strings.Contains(usage, want) {
				t.Errorf("help does not carry the spec's line for %q:\n%s", v.name, want)
			}
		}
	}
}

func renderFlags(specs []flagSpec) string {
	var b strings.Builder
	for i, s := range specs {
		if i > 0 {
			b.WriteString(" ")
		}
		switch {
		case s.multi:
			fmt.Fprintf(&b, "--%s<value>...", s.name)
		case s.bool:
			fmt.Fprintf(&b, "--%s", s.name)
		default:
			fmt.Fprintf(&b, "--%s<value>", s.name)
		}
	}
	return b.String()
}

func missing(want, got []string) []string {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	var out []string
	for _, w := range want {
		if !have[w] {
			out = append(out, w)
		}
	}
	return out
}
