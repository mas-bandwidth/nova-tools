package main

import (
	"regexp"
	"strings"
	"testing"
)

// The synopsis is a promise, and until this file nothing kept it.
//
// `nova-bus help` offered `--legacy-now` on `wait` for as long as the verb has existed, and
// `cmdWait` never defined it, so a reader who pasted the line the tool printed got
// `flag provided but not defined: -legacy-now` and exit 2 -- from the one verb a stranger
// polls with. SPEC.md was right the whole time and the banner was wrong; what was missing
// was anything that ran the banner. The sibling check for the `example:` block
// (internal/ci/onboarding_test.go) executes a paste-able line, and this binary's banner has
// no `example:` block for it to run, so the synopsis above it went unchecked.
//
// A flag named in a synopsis and absent from its verb's flag set is mechanically checkable,
// which makes leaving it unchecked a choice rather than an accident. This is the check: the
// banner is parsed, and every long flag it offers a verb is offered BACK to that verb, which
// must not answer "not defined". It is deliberately black-box -- each verb builds its own
// flag set inside its own function, and a test that reached in would have to be kept in step
// with seven of them; `run` is the surface a reader has, so it is the surface this uses.
//
// Nothing here says what a flag MEANS. The spec says that, and a reader says it. This says
// only that a flag the tool advertises is a flag the tool accepts.

// synopsisFlag matches a long flag the way the banner writes one. The banner's alternatives
// are `|`-separated and its optionals are bracketed, so `[--legacy-before <date-or-instant>|
// --legacy-now|--carry-history]` yields three, and `(--full | --as <name> | --since <commit>)`
// three more. Placeholders are `<...>` and never match.
var synopsisFlag = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// synopsisVerb is one verb of the banner with every flag its block offers, in the order the
// banner offers them.
type synopsisVerb struct {
	verb  string
	flags []string
}

// readSynopsis parses the `usage:` block of the shipped banner: a line beginning `nova-bus
// <verb>` opens a verb, the indented lines under it continue it, and the first blank line
// ends the block -- which is what keeps `every verb that runs git also takes
// [--git-timeout <seconds>]` out of it, since that sentence is about all seven and belongs
// to none.
func readSynopsis(t *testing.T) []synopsisVerb {
	t.Helper()
	lines := strings.Split(usage, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "usage:" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatal("the banner has no `usage:` line; this check reads the block under it")
	}
	var block []synopsisVerb
	for _, line := range lines[start:] {
		text := strings.TrimSpace(line)
		if text == "" {
			break
		}
		if rest, ok := strings.CutPrefix(text, "nova-bus "); ok {
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				t.Fatalf("a `nova-bus` line with no verb on it: %q", line)
			}
			block = append(block, synopsisVerb{verb: fields[0]})
		}
		if len(block) == 0 {
			t.Fatalf("a continuation line before any verb: %q", line)
		}
		cur := &block[len(block)-1]
		for _, flag := range synopsisFlag.FindAllString(text, -1) {
			name := strings.TrimPrefix(flag, "--")
			if !contains(cur.flags, name) {
				cur.flags = append(cur.flags, name)
			}
		}
	}
	return block
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestEveryFlagInTheSynopsisIsDefinedByItsVerb is the tripwire F3 of audit packet 1 asked
// for. It fails red on the `--legacy-now` the banner offered `wait`, and it fails on the
// next one too.
func TestEveryFlagInTheSynopsisIsDefinedByItsVerb(t *testing.T) {
	t.Parallel()
	block := readSynopsis(t)
	checked := 0
	for _, v := range block {
		for _, name := range v.flags {
			// One flag, no value. An undefined flag is `flag provided but not defined`
			// before package flag looks at anything else, so the answer does not depend
			// on the flag's type: a defined one that wants a value says `needs an
			// argument` instead, and a defined boolean parses and is then refused for the
			// --bus every verb requires. All three are exit 2 and only one is a defect.
			r := invoke(t, "", v.verb, "--"+name)
			if strings.Contains(r.stderr, "flag provided but not defined: -"+name) {
				t.Errorf("`nova-bus help` offers --%s to `%s` and %s does not define it, so the line the tool printed exits 2: %s",
					name, v.verb, v.verb, strings.TrimSpace(r.stderr))
			}
			checked++
		}
	}
	// The guard on the parser itself. A regexp or a block boundary that quietly stopped
	// matching would leave this test green over nothing, which is the one way a tripwire
	// fails that nobody notices. Seven verbs and the flag count as the banner stands; a
	// deliberate change to either moves these numbers in the same commit.
	if got := len(block); got != 7 {
		t.Errorf("the synopsis parsed to %d verbs, want 7: %+v", got, block)
	}
	if checked < 30 {
		t.Errorf("only %d flags were checked; the banner offers more than that, so the parser is reading less than the banner says", checked)
	}
}

// TestEveryVerbInTheSynopsisIsDispatchable is the other half of the same promise: a banner
// may not name a verb `run` has never heard of. It is cheap and it has already been earned
// once elsewhere -- cmd/nova-bus/version_test.go exists because a verb fell out of the
// dispatch and the banner did not notice.
func TestEveryVerbInTheSynopsisIsDispatchable(t *testing.T) {
	t.Parallel()
	for _, v := range readSynopsis(t) {
		r := invoke(t, "", v.verb)
		if strings.Contains(r.stderr, "unknown subcommand") {
			t.Errorf("`nova-bus help` names the verb %q and the dispatch does not: %s", v.verb, strings.TrimSpace(r.stderr))
		}
	}
}
