package tokens

import (
	"os"
	"path/filepath"
	"testing"
)

// The unit rule, proved over the three things a transcript can name and over the
// boundaries each of them is held to. An unbounded substring is the defect this rule
// exists to avoid: `#141` inside `#1412` would put one lane's spend on another's unit, and
// nobody reading the ledger would ever see it.

const unitsSetText = `(work-set "lane-three"
  :title "three small PRs"
  :units ((unit "tokens" :pr 1412 :branch "rowan/tokens-per-unit" :lane "one")
          (unit "bus" :pr 141 :branch "rowan/bus-host-header" :lane "two")
          (unit "ci" :branch "rowan/ci" :lane "three")
          (unit "nothing-to-match-on" :owner "Stella")))
`

func loadTestUnits(t *testing.T) *Units {
	t.Helper()
	path := filepath.Join(t.TempDir(), "set.lisp")
	if err := os.WriteFile(path, []byte(unitsSetText), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := LoadUnits(path)
	if err != nil {
		t.Fatalf("LoadUnits: %v", err)
	}
	if u.Set != "lane-three" {
		t.Fatalf("set id = %q", u.Set)
	}
	if u.Len() != 4 {
		t.Fatalf("loaded %d units, want 4; a unit with nothing to match on is still a unit", u.Len())
	}
	return u
}

func TestUnitsMatchTheThreeThingsATranscriptNames(t *testing.T) {
	u := loadTestUnits(t)
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"a pull request url", "mas-bandwidth/nova-tools/pull/1412", "tokens"},
		{"a hash", "the class behind #1412", "tokens"},
		{"a branch", "git push -u origin rowan/tokens-per-unit", "tokens"},
		{"a remote-qualified branch", "origin/rowan/bus-host-header", "bus"},
		{"a lane clone directory", "/Users/glenn/rowan-working/tmp/lane-three/repo/x.go", "ci"},
		{"a lane clone directory on a bench", "~/lane-three/repo/internal/ci", "ci"},
		{"nothing at all", "/x/schema/a.go", NoUnit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := u.Match([]string{c.input}); got != c.want {
				t.Fatalf("Match(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// THE BOUNDARY, in both directions and on all three keys.
func TestUnitsAreMatchedAtABoundaryAndNotBySubstring(t *testing.T) {
	u := loadTestUnits(t)
	cases := []struct {
		name  string
		input string
		want  string
	}{
		// `#1412` must not be read as `#141` with a 2 after it. The shorter PR is a real
		// unit in this set, so getting this wrong is a wrong answer and not a miss.
		{"the longer PR is not the shorter one", "#1412", "tokens"},
		{"the shorter PR alone", "#141 ", "bus"},
		{"a longer branch is not the shorter one", "rowan/ci-no-unchecked-fields-index", NoUnit},
		{"the branch exactly", "rowan/ci", "ci"},
		{"a branch inside a longer name", "their-rowan/ci", NoUnit},
		{"a longer lane name", "/tmp/lane-threes/repo", NoUnit},
		{"a lane name inside a longer element", "/tmp/xlane-three/repo", NoUnit},
		// A PR number that is part of a bigger number is not that PR.
		{"a PR digit run", "#14120", NoUnit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := u.Match([]string{c.input}); got != c.want {
				t.Fatalf("Match(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// FIRST MATCH WINS, over the tokens and then over the units: a child that opened its own
// PR and then read a sibling's is still working on its own, because the first thing it
// named is the piece of work it was given.
func TestTheFirstTokenThatNamesAUnitWins(t *testing.T) {
	u := loadTestUnits(t)
	got := u.Match([]string{"/x/a.go", "#1412", "mas-bandwidth/nova-tools/pull/141"})
	if got != "tokens" {
		t.Fatalf("Match = %q, want %q; the first token that named a unit decides", got, "tokens")
	}
	// And the other way round, so the test is about the ORDER and not about the two ids.
	got = u.Match([]string{"/x/a.go", "mas-bandwidth/nova-tools/pull/141", "#1412"})
	if got != "bus" {
		t.Fatalf("Match = %q, want %q", got, "bus")
	}
}

// A nil set is a fold run without --units: every message is `-`, and nothing panics on the
// way there.
func TestNoUnitsFileIsEveryRowOnTheDash(t *testing.T) {
	var u *Units
	if u.Len() != 0 {
		t.Fatal("a nil set has units")
	}
	if got := u.Match([]string{"#1412"}); got != NoUnit {
		t.Fatalf("Match with no set = %q, want %q", got, NoUnit)
	}
}

func TestAWorkSetWithNoUnitIdIsARefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "set.lisp")
	if err := os.WriteFile(path, []byte(`(work-set "s" :units ())`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUnits(path); err == nil {
		t.Fatal("a work set with no unit loaded")
	}
}

func TestAPRValueIsReadWhicheverWayItIsWritten(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"1412", "1412"},
		{"#1412", "1412"},
		{"0412", "412"},
		{"pr", ""},
		{"", ""},
		{"0", ""},
	} {
		if got := digitsOnly(c.in); got != c.want {
			t.Errorf("digitsOnly(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
