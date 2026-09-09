package bus

import (
	"strings"
	"testing"
)

// The tolerances are enumerated in address.go and in SPEC.md. Every one of them is pinned
// here, and so is the refusal, because a matcher that accepts everything is not a check.
func TestResolveListToleratesTheShapesTheTableActuallyWrites(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, line, want string
	}{
		{"one name", "Bo", "Bo"},
		{"semicolons", "Ada; Bo", "Ada; Bo"},
		{"commas", "Ada, Bo", "Ada; Bo"},
		{"an alias", "Bo Quill", "Bo"},
		{"case", "bO", "Bo"},
		{"an instance qualifier", "Ada a1b2c3d4", "Ada"},
		{"a parenthetical", "Ada (day shift, the west host, the shared account)", "Ada"},
		{"a qualifier and a parenthetical", "Ada a1b2c3d4 (active line)", "Ada"},
		{"stacked parentheticals", "Ada (day shift) (the west host)", "Ada"},
		{"a group", "Everybody at the table", "Ada; Bo; Dana"},
		{"a group and a name after an em dash", "Everybody at the table — Dana", "Ada; Bo; Dana"},
		{"a for- phrase", "Bo; for Dana when they arrive", "Bo; Dana"},
		{"a name twice, once through a group", "Everybody at the table; Ada", "Ada; Bo; Dana"},
		{"empty pieces", "Ada;; , ;Bo", "Ada; Bo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, unknown := c.ResolveList(tc.line)
			if len(unknown) > 0 {
				t.Fatalf("unresolved %v", unknown)
			}
			if joined := strings.Join(got, "; "); joined != tc.want {
				t.Fatalf("ResolveList(%q) = %q, want %q", tc.line, joined, tc.want)
			}
		})
	}
}

func TestResolveListRefusesAMisspelling(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"Boe", "Adda a1b2c3d4", "Ada; Boe", "Everybody", "the archivists"} {
		got, unknown := c.ResolveList(line)
		if len(unknown) == 0 {
			t.Fatalf("ResolveList(%q) = %v with nothing unresolved; a near miss must be refused, never guessed at", line, got)
		}
	}
}

// The prefix rule takes the LONGEST known name, so a roster holding both "Bo" and
// "Bo Quill" does not resolve "Bo Quill Two" to the shorter one.
func TestLongestKnownNameWins(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, unknown := c.ResolveList("Bo Quill on the Air")
	if len(unknown) > 0 || len(got) != 1 || got[0] != "Bo" {
		t.Fatalf("got %v unresolved %v, want [Bo]", got, unknown)
	}
}

// A From line names exactly one sender. Two names is not a sender.
func TestResolveOne(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := c.ResolveOne("Ada (day shift, the west host, the shared account)"); !ok || p.Name != "Ada" {
		t.Fatalf("ResolveOne = %+v %v, want Ada", p, ok)
	}
	if _, ok := c.ResolveOne("Ada; Bo"); ok {
		t.Fatal("a From line naming two people resolved to a sender")
	}
	if _, ok := c.ResolveOne("Everybody at the table"); ok {
		t.Fatal("a group resolved to a sender; a group never writes")
	}
	if _, ok := c.ResolveOne(""); ok {
		t.Fatal("an empty From line resolved to a sender")
	}
}

// "To: Ada and Bo" is two readers. The prefix rule resolved it to Ada ALONE --
// "Ada and Bo" begins with "Ada " -- and the note reached one of the two people it
// was written to, with nothing anywhere saying the other had been dropped. That is the
// silent wrong-reader failure this whole file exists to prevent, and it is pinned here in
// every spelling the table uses.
func TestAndIsASeparatorNotAQualifier(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, line, want string }{
		{"and", "Ada and Bo", "Ada; Bo"},
		{"ampersand", "Ada & Bo", "Ada; Bo"},
		{"comma and", "Ada, Bo, and Dana", "Ada; Bo; Dana"},
		{"and after a semicolon list", "Ada; Bo and Dana", "Ada; Bo; Dana"},
		{"capitalised", "Ada AND Bo", "Ada; Bo"},
		{"an alias on the far side", "Ada and Bo Quill", "Ada; Bo"},
		{"a group and a name", "Everybody at the table and Dana", "Ada; Bo; Dana"},
		{"inside a parenthetical, which is not a separator", "Ada (day shift and archivist)", "Ada"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, unknown := c.ResolveList(tc.line)
			if len(unknown) > 0 {
				t.Fatalf("ResolveList(%q) left %v unresolved", tc.line, unknown)
			}
			if joined := strings.Join(got, "; "); joined != tc.want {
				t.Fatalf("ResolveList(%q) = %q, want %q", tc.line, joined, tc.want)
			}
		})
	}
	// And the qualifier still works: what follows a known name is a qualifier only when it
	// is not itself a name this table knows.
	got, unknown := c.ResolveList("Ada reads in place")
	if len(unknown) > 0 || strings.Join(got, "; ") != "Ada" {
		t.Fatalf(`ResolveList("Ada reads in place") = %v %v, want Ada: an instance qualifier is not a second reader`, got, unknown)
	}
}

// Two known names in ONE token, with no separator between them, is refused rather than
// resolved to the first. Whatever "Ada Bo" is, it is not a note to Ada, and a tool
// that picked one of the two would be guessing about a reader.
func TestAKnownNameFollowedByAKnownNameIsRefused(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"Ada Bo", "Ada Bo Quill", "Bo Dana", "Ada the archivist"} {
		got, unknown := c.ResolveList(line)
		if len(unknown) == 0 {
			t.Fatalf("ResolveList(%q) = %v with nothing unresolved; two names in one token is a refusal", line, got)
		}
	}
	// A From line naming two people is not a sender, in either spelling.
	for _, line := range []string{"Ada and Bo", "Ada Bo"} {
		if p, ok := c.ResolveOne(line); ok {
			t.Fatalf("ResolveOne(%q) = %q; a note has one writer", line, p.Name)
		}
	}
}
