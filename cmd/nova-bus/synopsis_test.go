package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.False(t, start < 0, "the banner has no `usage:` line; this check reads the block under it")
	var block []synopsisVerb
	for _, line := range lines[start:] {
		text := strings.TrimSpace(line)
		if text == "" {
			break
		}
		if rest, ok := strings.CutPrefix(text, "nova-bus "); ok {
			fields := strings.Fields(rest)
			require.NotEmptyf(t, len(fields), "a `nova-bus` line with no verb on it: %q", line)
			block = append(block, synopsisVerb{verb: fields[0]})
		}
		require.NotEmptyf(t, len(block), "a continuation line before any verb: %q", line)
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
			assert.NotContainsf(t, r.stderr, "flag provided but not defined: -"+name, "`nova-bus help` offers --%s to `%s` and %s does not define it, so the line the tool printed exits 2: %s",
				name, v.verb, v.verb, strings.TrimSpace(r.stderr))
			checked++
		}
	}
	// The guard on the parser itself. A regexp or a block boundary that quietly stopped
	// matching would leave this test green over nothing, which is the one way a tripwire
	// fails that nobody notices. Twelve LINES over ten verbs, and the flag count, as the
	// banner stands; a deliberate change to either moves these numbers in the same commit.
	// `draft` and `send` each have two forms and the banner shows both, because the reply
	// form's and the prepared form's required flags are not the released form's and one
	// line offering all of them is a line nobody can paste. `reply` is its own line.
	{
		got := len(block)
		assert.Falsef(t, got != 12, "the synopsis parsed to %d verb lines, want 12: %+v", got, block)
	}
	assert.Falsef(t, checked < 30, "only %d flags were checked; the banner offers more than that, so the parser is reading less than the banner says", checked)
}

// TestEveryVerbInTheSynopsisIsDispatchable is the other half of the same promise: a banner
// may not name a verb `run` has never heard of. It is cheap and it has already been earned
// once elsewhere -- cmd/nova-bus/version_test.go exists because a verb fell out of the
// dispatch and the banner did not notice.
func TestEveryVerbInTheSynopsisIsDispatchable(t *testing.T) {
	t.Parallel()
	for _, v := range readSynopsis(t) {
		r := invoke(t, "", v.verb)
		assert.NotContainsf(t, r.stderr, "unknown subcommand", "`nova-bus help` names the verb %q and the dispatch does not: %s", v.verb, strings.TrimSpace(r.stderr))
	}
}

// The spec states the bound `check` has, the one its -h lists: `--max <n>`, default 20, 0
// for all, then one BUS MORE line and one BUS CHECK count line. It said `check` had no
// --max, and described per-kind caps, BUS FINDING and BUS SUMMARY lines no build prints.
func TestTheSpecStatesTheCheckBoundTheCodeHas(t *testing.T) {
	t.Parallel()
	help := invoke(t, "", "check", "-h").mustCode(t, 0).stdout
	require.Contains(t, help, "--max <int>  findings to print before one BUS MORE line naming the rest (default 20, 0 = all)")
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC.md"))
	require.NoError(t, err)
	spec := string(raw)
	for _, gone := range []string{"`check` has no `--max`", "BUS FINDING kind=", "BUS SUMMARY mode=", "check --bus ~/bus --full --fail-max"} {
		assert.NotContains(t, spec, gone, "docs/SPEC.md describes a check the code does not have")
	}
	_, section, ok := strings.Cut(spec, "#### The bound — check\n")
	require.True(t, ok, "docs/SPEC.md has no `#### The bound — check` section")
	section, _, _ = strings.Cut(section, "\n#### ")
	for _, want := range []string{"`--max <n>` findings (default 20, `0` for all)", `BUS MORE shown=<n> total=<t> remedy="--max 0"`, "BUS CHECK findings=<t> fail=<x> warn=<w>"} {
		assert.Contains(t, section, want)
	}
}

// The bus's code names only tools that exist: a comment that sends a reader to a nova-*
// tool this repository does not build is a dangling reference.
func TestTheBusCodeNamesOnlyToolsThatExist(t *testing.T) {
	t.Parallel()
	known := map[string]bool{"nova-tools": true} // the repository itself
	entries, err := os.ReadDir(filepath.Join("..", "..", "cmd"))
	require.NoError(t, err)
	for _, e := range entries {
		known[e.Name()] = e.IsDir()
	}
	name := regexp.MustCompile(`nova-[a-z][a-z-]*[a-z]`)
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "bus")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			raw, err := os.ReadFile(f)
			require.NoError(t, err)
			for _, n := range name.FindAllString(string(raw), -1) {
				assert.True(t, known[n], "%s names %s, which is no tool under cmd/", f, n)
			}
		}
	}
}
