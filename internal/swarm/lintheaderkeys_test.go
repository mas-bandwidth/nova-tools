package swarm

import (
	"sort"
	"strings"
	"testing"
)

// THE HEADER BLOCK IS THE LEADING RUN OF `KEY: value` LINES, IN ANY CASE (#2605).
//
// The two lines every card the darwin launchers stage must carry are lower case --
// `~/rowan-working/rowan-tools/bin/launchers/*-native-darwin.sh:30-31` read `base-repo:`
// and `base-sha:` out of the card's first 40 lines with a case-sensitive `sed` and
// refuse to launch without both. Under the old `^[A-Z][A-Z-]*:` those two lines ENDED
// the header block, so on all 59 cards of the 2026-09-22 sprint set `PATHS:`, `TEST:`
// and every other key below them was stranded outside the header the gate reads, and
// `bin/sprint-stage:37` refuses a whole stage on one such card.
//
// The fixture below is a real card of that set, first 20 lines, verbatim:
// `~/rowan-working/tmp/session-0919b/sprint-tools/cards/card-tools-03-stale-base-false-positive.md`
// at sha af6a9fccf331. The negative fixtures beside it are the other half of the rule:
// a prose line still ends the block where it sits, and a typed key written in lower case
// is still not that typed key (SPEC-TOOLWORK.md §5 rule 1 and WORKER-CARDS.md write the
// five names in upper case and neither says anything about case).

// sprintCard0922 is that card's first 20 lines. Line 1 is its contract line; lines 2-20
// are nineteen keys, two of them lower case.
var sprintCard0922 = []string{
	"RESULT tools-03-stale-base-false-positive sha=af6a9fccf331 — harvest stale-base is a false positive when the branch's parent IS the current target tip and the diff equals PATHS (14 DONE cards stranded)",
	"KIND: fix",
	"SCHEMA: v2",
	"ATTEMPT: 1",
	"DEADLINE: 2700",
	"LEG: go",
	"REPO: mas-bandwidth/nova-tools",
	"BASE: dev",
	"base-repo: https://example.com/mas-bandwidth/nova-tools.git",
	"base-sha: af6a9fccf33199b4edfc586ab3c7f0c5e1a2d71d",
	"PATHS: internal/pulse/harveststale.go internal/pulse/harveststale_test.go",
	"FILES: 2",
	"TEST: ./internal/pulse/ TestStaleBaseAcceptsBranchWhoseParentIsTargetTip",
	"RUN: go test ./internal/pulse/ -run TestStaleBase -count=1",
	"SYMBOL: pulse.staleBaseRefusal (internal/pulse/harveststale.go:23) and the offender walk it calls, pulse.staleBaseOffenders (internal/pulse/harveststale.go:97)",
	"RED-WHEN: staleBaseRefusal returns a non-nil error for a fixture repository whose branch's first parent IS the pinned target OID and whose `git diff --name-only <oid>..<head>` is exactly the declared PATHS",
	"DONE-WHEN: `go test ./internal/pulse/ -run TestStaleBase -count=1` contains a case whose fixture branch is cut from the pinned target tip and whose two-dot diff equals the declared globs, asserts staleBaseRefusal returns nil, and passes at head while failing on base-sha with the current `stale-base files=... range=...` error; the existing refusal case (a branch cut from an OLDER base, so the diff shows a later landing) still refuses, in the same test.",
	"NO-SUBAGENTS: work in this session only; do not spawn an Explore, Task or child agent (Glenn 2026-09-21, standard: a spawned agent multiplies exploration tokens, hides its work from the log, and on a darwin bench goes silent under the wall and the card is killed as idle).",
	"UNATTENDED: you are unattended. Never ask a question, never offer to proceed. Decide, and record the decision in RESULT.md. A turn that ends in a question ends the card as NO-RESULT, indistinguishable from a crash, and the work is thrown away (nova-tools #2548; the runtime dogfood on 2026-09-22 measured the ending at ~5 s, not at the deadline).",
	"MODE: fix",
}

// card renders lines followed by the prose a card carries under its header.
func card(lines ...string) []byte {
	return []byte(strings.Join(append(append([]string{}, lines...),
		"",
		"You are a Go engineer.",
		"STEP 1. cd repo",
		"", // a trailing newline, as every card on disk has
	), "\n"))
}

func TestCardHeaderBlockRunsToTheFirstLineThatIsNotAKeyLine(t *testing.T) {
	// The nineteen keys of the real card, with the line each sits on.
	realKeys := map[string]int{
		"KIND": 2, "SCHEMA": 3, "ATTEMPT": 4, "DEADLINE": 5, "LEG": 6, "REPO": 7,
		"BASE": 8, "base-repo": 9, "base-sha": 10, "PATHS": 11, "FILES": 12,
		"TEST": 13, "RUN": 14, "SYMBOL": 15, "RED-WHEN": 16, "DONE-WHEN": 17,
		"NO-SUBAGENTS": 18, "UNATTENDED": 19, "MODE": 20,
	}
	cases := []struct {
		name string
		card []byte
		// inBlock is key -> the line it must be read on, and is EXACTLY the block.
		inBlock map[string]int
		// stranded is key -> line, and is exactly what the lint hands back as below
		// the block; a nil map means the block swallowed the whole header.
		stranded map[string]int
	}{
		{
			name:     "a real sprint card of 2026-09-22: every key is inside the block",
			card:     card(sprintCard0922...),
			inBlock:  realKeys,
			stranded: nil,
		},
		{
			name: "the launcher's two lower-case lines alone do not end the block",
			card: card(
				"RESULT: CARD-2605 do the thing",
				"KIND: fix-red",
				"base-repo: https://example.com/mas-bandwidth/nova-tools.git",
				"base-sha: af6a9fccf33199b4edfc586ab3c7f0c5e1a2d71d",
				"PATHS: internal/swarm/lintheader.go",
				"TEST: internal/swarm TestCardHeaderBlockRunsToTheFirstLineThatIsNotAKeyLine",
				"LEGS: go",
				"SOURCE: mas-bandwidth/nova-tools#2605",
			),
			inBlock: map[string]int{
				"KIND": 2, "base-repo": 3, "base-sha": 4, "PATHS": 5, "TEST": 6,
				"LEGS": 7, "SOURCE": 8,
			},
			stranded: nil,
		},
		{
			name: "a prose line ends the block where it sits, and the keys under it are stranded",
			card: card(
				"RESULT: CARD-2605 do the thing",
				"KIND: fix-red",
				"base-sha: af6a9fccf33199b4edfc586ab3c7f0c5e1a2d71d",
				"This card is about the header block, which is what this sentence ends.",
				"PATHS: internal/swarm/lintheader.go",
				"TEST: internal/swarm TestCardHeaderBlockRunsToTheFirstLineThatIsNotAKeyLine",
				"LEGS: go",
				"SOURCE: mas-bandwidth/nova-tools#2605",
			),
			inBlock:  map[string]int{"KIND": 2, "base-sha": 3},
			stranded: map[string]int{"PATHS": 5, "TEST": 6, "LEGS": 7, "SOURCE": 8},
		},
		{
			name: "a blank line does not end the block, as it never did",
			card: card(
				"RESULT: CARD-2605 do the thing",
				"KIND: fix-red",
				"",
				"base-sha: af6a9fccf33199b4edfc586ab3c7f0c5e1a2d71d",
				"PATHS: none",
			),
			inBlock:  map[string]int{"KIND": 2, "base-sha": 4, "PATHS": 5},
			stranded: nil,
		},
		{
			name: "an indented key line is prose and ends the block, as it never was a key line",
			card: card(
				"RESULT: CARD-2605 do the thing",
				"KIND: fix-red",
				"  PATHS: internal/swarm/lintheader.go",
				"TEST: internal/swarm TestCardHeaderBlockRunsToTheFirstLineThatIsNotAKeyLine",
			),
			inBlock:  map[string]int{"KIND": 2},
			stranded: map[string]int{"TEST": 4},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block, stranded := cardHeaderBlock(tc.card)
			for key, line := range tc.inBlock {
				f, ok := block[key]
				if !ok || !f.found {
					t.Errorf("%s: not in the header block; the block holds %v", key, blockKeys(block))
					continue
				}
				if f.line != line {
					t.Errorf("%s: read on line %d, want line %d", key, f.line, line)
				}
			}
			if len(block) != len(tc.inBlock) {
				t.Errorf("block holds %v, want exactly %v", blockKeys(block), sortedOf(tc.inBlock))
			}
			for key, line := range tc.stranded {
				if got := stranded[key]; got != line {
					t.Errorf("%s: stranded on line %d, want line %d", key, got, line)
				}
			}
			if len(stranded) != len(tc.stranded) {
				t.Errorf("stranded %v, want exactly %v", sortedOf(stranded), sortedOf(tc.stranded))
			}
			// The lint's own answer, which is what `bin/sprint-stage` refuses on: a key
			// inside the block draws no `below the header block` drift, and a key under
			// the prose draws one naming the line it sits on.
			var below []string
			for _, f := range LintCardHeader(tc.card, nil, true) {
				if strings.Contains(f.Excerpt, "below the header block") {
					below = append(below, f.Excerpt)
				}
			}
			if len(below) != len(tc.stranded) {
				t.Errorf("%d `below the header block` drifts, want %d: %v", len(below), len(tc.stranded), below)
			}
		})
	}
}

// A TYPED KEY IS ITS SPELLING (#2605). Widening what CONTINUES the block is not widening
// what a typed key IS: SPEC-TOOLWORK.md §5 rule 1 and WORKER-CARDS.md write the five
// names in upper case and say nothing about case anywhere, so a lower-case `paths:` is
// read past as an unknown key -- the card has no PATHS: line and the lint says so, which
// is the answer the gate will give it.
func TestCardHeaderLowerCaseTypedKeyIsNotTheTypedKey(t *testing.T) {
	raw := card(
		"RESULT: CARD-2605 do the thing",
		"KIND: fix-red",
		"paths: internal/swarm/lintheader.go",
		"TEST: internal/swarm TestCardHeaderLowerCaseTypedKeyIsNotTheTypedKey",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#2605",
	)
	block, stranded := cardHeaderBlock(raw)
	if f, ok := block["paths"]; !ok || f.line != 3 {
		t.Fatalf("`paths:` is a key line of the block on line 3; block holds %v", blockKeys(block))
	}
	if len(stranded) != 0 {
		t.Errorf("nothing is stranded, got %v", sortedOf(stranded))
	}
	f := drew(t, LintCardHeader(raw, nil, true), "paths-declared")
	if !strings.Contains(f.Excerpt, "no PATHS: line") {
		t.Errorf("paths-declared says the card has no PATHS: line, got %q", f.Excerpt)
	}
}

func blockKeys(m map[string]headerField) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
