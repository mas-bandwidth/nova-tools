package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1451: every nova-bus refusal stopped at `; refusing to guess`, naming what was
// wrong and never the door; a reader who typed a verb bare was left to guess where
// the answer lived. ONBOARDING point 1 promises `<tool>[ <verb>]: <what was wrong>;
// run: <tool> help` for an invocation the tool cannot run. The door is asserted per
// stderr LINE, not per run, because #1496 prints one line per missing flag. The verbs
// are read from the usage block so a verb added later is covered without editing this
// test; they are not listed here.
func TestEveryBareVerbRefusalNamesItsDoor(t *testing.T) {
	t.Setenv("NOVA_BUS_RECEIPT_MAX_WORDS", "")

	// Every verb the usage block names, run BARE. A verb that exits 0 or 1 with no
	// flags is a valid invocation and not this test's business; only exit 2 is.
	seen := map[string]bool{}
	checked := 0
	for _, v := range readSynopsis(t) {
		if seen[v.verb] {
			continue
		}
		seen[v.verb] = true
		r := invoke(t, "", v.verb)
		if r.code == 0 || r.code == 1 {
			continue
		}
		if r.code != 2 {
			assert.Failf(t, "assertion failed", "bare `nova-bus %s`: exit = %d, want 2 (could not run); stderr: %q", v.verb, r.code, r.stderr)
			continue
		}
		checked++
		requireDoorOnEveryRefusal(t, "bare `nova-bus "+v.verb+"`", r.stderr)
	}
	require.Falsef(t, checked < 8, "only %d bare verbs refused; the usage reader is broken or the list shrank, and this test checked too little to mean anything", checked)

	// The shared printers and per-verb literals a bare verb short-circuits before, one
	// minimal invocation each, so the sweep is not sampled: a site that lost its door
	// is named by its own case rather than hidden behind a count.
	busDir := t.TempDir()
	for _, tc := range []struct {
		label string
		args  []string
	}{
		{"nova-bus inbox open-max", []string{"inbox", "--bus", busDir, "--as", "Ada", "--receipt-max-words", "1", "--open-max", "0"}},
		{"nova-bus inbox receipt-max-words", []string{"inbox", "--bus", busDir, "--as", "Ada"}},
		{"nova-bus draft", []string{"draft", "--bus", busDir, "--as", "Ada"}},
		{"nova-bus prepare", []string{"prepare", "--bus", busDir, "--as", "Ada"}},
		{"nova-bus send prepared", []string{"send", "--bus", busDir, "--remote", "origin", "--branch", "main", "--prepared", "/nowhere", "--prepared-stdin"}},
		{"nova-bus send draft", []string{"send", "--bus", busDir, "--remote", "origin", "--branch", "main"}},
		{"nova-bus receipt", []string{"receipt", "--bus", busDir, "--as", "Ada", "--remote", "origin", "--branch", "main"}},
		{"nova-bus close before", []string{"close", "--bus", busDir, "--as", "Ada", "--before", "not-an-instant"}},
		{"nova-bus inbox advance", []string{"inbox", "--bus", busDir, "--as", "Ada", "--receipt-max-words", "1", "--advance"}},
		{"nova-bus wait timeout", []string{"wait", "--bus", busDir, "--as", "Ada", "--remote", "origin", "--branch", "main", "--receipt-max-words", "1"}},
		{"nova-bus check baseline", []string{"check", "--bus", busDir}},
	} {
		r := invoke(t, "", tc.args...)
		if r.code != 2 {
			assert.Failf(t, "assertion failed", "%s: exit = %d, want 2 (could not run); stderr: %q", tc.label, r.code, r.stderr)
			continue
		}
		requireDoorOnEveryRefusal(t, tc.label, r.stderr)
	}
}

// requireDoorOnEveryRefusal holds ONBOARDING point 1 to every line an exit-2 refusal
// wrote: the line ends at the literal door, and the door is there exactly once. The
// label names the verb and each failure quotes the offending line.
func requireDoorOnEveryRefusal(t *testing.T, label, stderr string) {
	t.Helper()
	lines := 0
	for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		if line == "" {
			continue
		}
		lines++
		verb, _, _ := strings.Cut(line, " REFUSED: ")
		door := "; run: nova-bus " + strings.ToLower(verb) + " -h"
		assert.Truef(t, strings.HasSuffix(line, door), "%s: refusal does not end at its verb's door %q: %q", label, door, line)
		assert.Equalf(t, 1, strings.Count(line, "; run: "), "%s: the door appears more than once: %q", label, line)
	}
	assert.NotEqualf(t, 0, lines, "%s: exit 2 with no refusal line: %q", label, stderr)
}
