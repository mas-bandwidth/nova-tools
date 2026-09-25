package card

import "testing"

// TestCutDependsVocabulary: an issue's DEPENDS-ON becomes the card
// vocabulary (#3623): #n and name#n are owner/name#n, a (WHY: ...) is the
// why, other parentheticals drop, and prose is refused.
func TestCutDependsVocabulary(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ in, deps, why, err string }{
		{"none", "none", "", ""},
		{"none (WHY: nothing)", "none", "nothing", ""},
		{"mas-bandwidth/nova-tools#3502 (WHY: the card record shape)", "mas-bandwidth/nova-tools#3502", "the card record shape", ""},
		{"#3502, rowan-tools#12 (landed), stream/swarm-cards, task:t1, card-7", "mas-bandwidth/nova-tools#3502,mas-bandwidth/rowan-tools#12,stream/swarm-cards,task:t1,card-7", "", ""},
		{"after the harvest lands", "", "", `DEPENDS-ON: "after the harvest lands" is not a card id, owner/repo#n, stream/<slug> or task:<id>`},
		{"(WHY: only a why)", "", "", "DEPENDS-ON names nothing; write none"},
	} {
		deps, why, err := CutDepends("mas-bandwidth/nova-tools", c.in)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if deps != c.deps || why != c.why || got != c.err {
			t.Errorf("CutDepends(%q) = %q, %q, %q; want %q, %q, %q", c.in, deps, why, got, c.deps, c.why, c.err)
		}
	}
}

// TestIssueFieldsFirstLineWins: the first line of each key wins, list
// markers and bold keys are read, and a value that is one code span loses its
// backticks while one that contains code keeps them.
func TestIssueFieldsFirstLineWins(t *testing.T) {
	t.Parallel()

	f := IssueFields("intro\n- **DONE-WHEN:** `go test ./x` fails red\nbase-sha: `0123`\nSTREAM: a\nSTREAM: b\nWHY: none of it\n")
	for k, v := range map[string]string{"DONE-WHEN": "`go test ./x` fails red", "base-sha": "0123", "STREAM": "a", "WHY": "none of it"} {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
	if _, ok := f["PATHS"]; ok {
		t.Error("PATHS read from an issue that has none")
	}
}
