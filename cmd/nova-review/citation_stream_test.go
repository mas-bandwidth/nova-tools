package main

import (
	"reflect"
	"strings"
	"testing"
)

func citationSpec(t *testing.T, path, rules string) scopedSpec {
	t.Helper()
	s, err := parseScopedSpec(path, "## Rules\n"+rules, "Rules")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func streamCitationTargets(specs []scopedSpec, line string, chunks ...int) []specRule {
	s := newStreamingCitations(specs)
	for len(line) != 0 {
		n := len(line)
		if len(chunks) != 0 && chunks[0] < n {
			n = chunks[0]
		}
		s.Feed([]byte(line[:n]))
		line = line[n:]
		if len(chunks) != 0 {
			chunks = chunks[1:]
		}
	}
	return s.Finish()
}

func ruleIDs(rules []specRule) []string {
	if len(rules) == 0 {
		return nil
	}
	out := make([]string, len(rules))
	for i, rule := range rules {
		out[i] = patchRuleKey(rule)
	}
	return out
}

func TestStreamingCitationsMatchRuleTwoAcrossChunks(t *testing.T) {
	one := citationSpec(t, "docs/SPEC.md", "1. one\n2. two\n3. three\n")
	two := citationSpec(t, "docs/OTHER.md", "1. alpha\n2. beta\n3. gamma\n")
	for _, tc := range []struct {
		name  string
		specs []scopedSpec
		line  string
		want  []string
	}{
		{"bare singular", []scopedSpec{one}, "rule 2", []string{"docs/SPEC.md:3"}},
		{"bare plural comma and", []scopedSpec{one}, "Rules 3, 1 and 2", []string{"docs/SPEC.md:4", "docs/SPEC.md:2", "docs/SPEC.md:3"}},
		{"possessive terminal", []scopedSpec{one}, "rule 2's wording", []string{"docs/SPEC.md:3"}},
		{"range refused", []scopedSpec{one}, "rule 1 through 3", nil},
		{"to refused", []scopedSpec{one}, "rule 1 to 3", nil},
		{"unseparated plural refused", []scopedSpec{one}, "rules 1 2", nil},
		{"plural singleton refused", []scopedSpec{one}, "rules 1", nil},
		{"leading conjunction refused", []scopedSpec{one}, "rules and 1 2", nil},
		{"multiple bare citations", []scopedSpec{one}, "rule 1; rule 3", []string{"docs/SPEC.md:2", "docs/SPEC.md:4"}},
		{"bare plus explicit one spec", []scopedSpec{one}, "rule 1; SPEC.md rule 2", []string{"docs/SPEC.md:2", "docs/SPEC.md:3"}},
		{"repeated cites", []scopedSpec{one}, "SPEC.md rule 2; SPEC.md rules 1, 2", []string{"docs/SPEC.md:3", "docs/SPEC.md:2"}},
		{"two scoped only explicit", []scopedSpec{one, two}, "rule 2; SPEC.md rule 3; OTHER.md rules 2, 1", []string{"docs/SPEC.md:4", "docs/OTHER.md:3", "docs/OTHER.md:2"}},
		{"prefix ordering", []scopedSpec{two, one}, "SPEC.md rule 1; OTHER.md rule 3; SPEC.md rule 2", []string{"docs/OTHER.md:4", "docs/SPEC.md:2", "docs/SPEC.md:3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ruleIDs(citationTargets(tc.line, tc.specs)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ordinary helper got %v, want Rule 2 %v", got, tc.want)
			}
			for _, chunks := range [][]int{{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, {2, 5, 3, 8}, {7, 1, 11}} {
				if got := ruleIDs(streamCitationTargets(tc.specs, tc.line, chunks...)); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("chunks=%v got %v, want Rule 2 %v", chunks, got, tc.want)
				}
			}
		})
	}
}

func TestCitedRuleNumbersUsesClosedPluralForms(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []int
	}{
		{"rule 1; rule 2", []int{1, 2}},
		{"rules 1 and 2", []int{1, 2}},
		{"rules 1, 2, 3", []int{1, 2, 3}},
		{"rules 1, 2 and 3", []int{1, 2, 3}},
		{"rules 1", nil},
		{"rules 1, 2, 3 and 4", nil},
	} {
		if got := citedRuleNumbers(tc.line); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q got %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestStreamingCitationsFindsGiantPrefixedRuleWithoutLineRetention(t *testing.T) {
	spec := citationSpec(t, "docs/SPEC.md", "1. one\n2. two\n3. three\n")
	prefix := strings.Repeat("x", patchControlPrefix+8192)
	line := prefix + " SPEC.md rules 3, 2 and 3"
	want := ruleIDs(citationTargets(line, []scopedSpec{spec}))
	// Split the explicit prefix and its first rule token across Writes, after
	// more than the old control-prefix bound.
	chunks := []int{len(prefix) + 3, 1, 2, 4, 1, 4093}
	if got := ruleIDs(streamCitationTargets([]scopedSpec{spec}, line, chunks...)); !reflect.DeepEqual(got, want) {
		t.Fatalf("giant citation got %v, want helper %v", got, want)
	}
}

func TestStreamingCitationsDeduplicatesRepeatedScopedTargets(t *testing.T) {
	spec := citationSpec(t, "docs/SPEC.md", "1. one\n2. two\n")
	line := strings.Repeat("SPEC.md rule 1; ", 4096)
	got := streamCitationTargets([]scopedSpec{spec}, line, 1, 2, 3, 5, 8)
	if ids := ruleIDs(got); !reflect.DeepEqual(ids, []string{"docs/SPEC.md:2"}) {
		t.Fatalf("repeated citations retained duplicate targets: %v", ids)
	}
}
