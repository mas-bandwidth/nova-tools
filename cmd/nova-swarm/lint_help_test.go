package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `lint -h` explains every flag it lists: the third cold rating found four (--base-check,
// --legs, --p95, --repo) with a name and nothing else. A flag line is `--name <type>  text`;
// a line with no text is the defect.
func TestLintHelpExplainsEveryFlag(t *testing.T) {
	t.Parallel()
	exit, stdout, _ := runSwarm(t, "lint", "-h")
	require.Equal(t, 0, exit, "lint -h: exit %d", exit)
	in, flags := false, 0
	for _, line := range strings.Split(stdout, "\n") {
		if line == "flags:" {
			in = true
			continue
		}
		if !in || !strings.HasPrefix(line, "  --") {
			if in && strings.HasPrefix(line, "exit codes") {
				break
			}
			continue
		}
		flags++
		name, rest, _ := strings.Cut(strings.TrimSpace(line), "  ")
		assert.NotEqual(t, "", strings.TrimSpace(rest), "lint -h lists %s and does not say what it does", name)
	}
	assert.GreaterOrEqual(t, flags, 12, "lint -h lists %d flags, want at least the twelve lint has:\n%s", flags, stdout)
	// a flag's placeholder is its type word, not the first backquoted words of its text
	assert.Contains(t, stdout, "--child-rules-file <file>  ", "--child-rules-file's placeholder is not <file>:\n%s", stdout)
	assert.NotContains(t, stdout, "<[name]", "--child-rules-file's placeholder is not <file>:\n%s", stdout)
	for _, want := range []string{"--base-check", "--legs", "--p95", "--repo", "--child-rules-file"} {
		assert.Contains(t, stdout, want, "lint -h does not list %s", want)
	}
	// the banner line lint -h quotes offers the base check with its three inputs
	for _, want := range []string{"[--base-check [--repo <dir>] [--legs <file>] [--p95 <file>]]", "[--child-rules | --child-rules-file <file>]", "the four checks of a coding card"} {
		assert.Contains(t, stdout, want, "lint -h does not quote %q", want)
	}
}

// `lint -h` says what a bare `lint --card` holds the card to: nova-swarm's own card
// contract, the same for every adopter, with the place an adopter's own rules go. A cold
// reader took the bare lint for one repository's private rule set (tool ledger W6).
func TestLintHelpSaysWhatABareCardIsHeldTo(t *testing.T) {
	t.Parallel()
	exit, stdout, _ := runSwarm(t, "lint", "-h")
	require.Equal(t, 0, exit, "lint -h: exit %d", exit)
	for _, want := range []string{"a bare --card holds the card to nova-swarm's own card contract", "the same for every adopter", "--rules lists every check", "an adopter's own rules go in --child-rules-file"} {
		assert.Contains(t, stdout, want, "lint -h does not say %q", want)
	}
}
