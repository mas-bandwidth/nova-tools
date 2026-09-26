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
		{"after the harvest lands", "", "", `DEPENDS-ON: "after the harvest lands" is not a card id, owner/repo#n, <slug>:sentinel or task:<id>`},
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

	f := IssueFields("intro\n- **DONE-WHEN:** `go test ./x` fails red\nBASE-SHA: `0123`\nSTREAM: a\nSTREAM: b\nWHY: none of it\n")
	for k, v := range map[string]string{"DONE-WHEN": "`go test ./x` fails red", "BASE-SHA": "0123", "STREAM": "a", "WHY": "none of it"} {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
	if _, ok := f["PATHS"]; ok {
		t.Error("PATHS read from an issue that has none")
	}
}

// TestRetiredHeaderKeyIsRefusedNamingTheNewOne (nova-tools#4352 A): header
// keys are one case; the lowercase base-sha: of the launcher era is refused
// naming BASE-SHA:, on a plain line and as a list item.
func TestRetiredHeaderKeyIsRefusedNamingTheNewOne(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"BASE: dev\nbase-sha: 0123\n", "- **base-sha:** 0123\n", "> base-sha: 0123\n"} {
		err := RetiredHeaderKey(body)
		if err == nil || err.Error() != "base-sha: is spelled BASE-SHA:" {
			t.Errorf("%q: got %v; want base-sha: is spelled BASE-SHA:", body, err)
		}
	}
	if err := RetiredHeaderKey("BASE: dev\nBASE-SHA: 0123\nbase-repo-ish: no\n"); err != nil {
		t.Errorf("the one-case header is refused: %v", err)
	}
	if err := RetiredHeaderKey("base-repo: https://example.com/o/r.git\n"); err == nil || err.Error() != "base-repo: is spelled BASE-REPO:" {
		t.Errorf("base-repo: got %v", err)
	}
}
