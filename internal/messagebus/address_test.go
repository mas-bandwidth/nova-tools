package messagebus

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
		{"one name", "Stella", "Stella"},
		{"semicolons", "Rowan; Stella", "Rowan; Stella"},
		{"commas", "Rowan, Stella", "Rowan; Stella"},
		{"an alias", "Stella Codex", "Stella"},
		{"case", "sTeLLa", "Stella"},
		{"an instance qualifier", "Rowan a1b2c3d4", "Rowan"},
		{"a parenthetical", "Rowan (bud, the Studio, the mas account)", "Rowan"},
		{"a qualifier and a parenthetical", "Rowan a1b2c3d4 (active bud)", "Rowan"},
		{"stacked parentheticals", "Rowan (bud) (the Studio)", "Rowan"},
		{"a group", "Everybody at the table", "Rowan; Stella; Glenn"},
		{"a group and a name after an em dash", "Everybody at the table — Glenn", "Rowan; Stella; Glenn"},
		{"a for- phrase", "Stella; for Glenn when he arrives", "Stella; Glenn"},
		{"a name twice, once through a group", "Everybody at the table; Rowan", "Rowan; Stella; Glenn"},
		{"empty pieces", "Rowan;; , ;Stella", "Rowan; Stella"},
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
	for _, line := range []string{"Stela", "Rowna a1b2c3d4", "Rowan; Stela", "Everybody", "the keepers"} {
		got, unknown := c.ResolveList(line)
		if len(unknown) == 0 {
			t.Fatalf("ResolveList(%q) = %v with nothing unresolved; a near miss must be refused, never guessed at", line, got)
		}
	}
}

// The prefix rule takes the LONGEST known name, so a roster holding both "Stella" and
// "Stella Codex" does not resolve "Stella Codex Two" to the shorter one.
func TestLongestKnownNameWins(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, unknown := c.ResolveList("Stella Codex on the Air")
	if len(unknown) > 0 || len(got) != 1 || got[0] != "Stella" {
		t.Fatalf("got %v unresolved %v, want [Stella]", got, unknown)
	}
}

// A From line names exactly one sender. Two names is not a sender.
func TestResolveOne(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := c.ResolveOne("Rowan (bud, the Studio, the mas account)"); !ok || p.Name != "Rowan" {
		t.Fatalf("ResolveOne = %+v %v, want Rowan", p, ok)
	}
	if _, ok := c.ResolveOne("Rowan; Stella"); ok {
		t.Fatal("a From line naming two people resolved to a sender")
	}
	if _, ok := c.ResolveOne("Everybody at the table"); ok {
		t.Fatal("a group resolved to a sender; a group never writes")
	}
	if _, ok := c.ResolveOne(""); ok {
		t.Fatal("an empty From line resolved to a sender")
	}
}

// "To: Rowan and Stella" is two readers. The prefix rule resolved it to Rowan ALONE --
// "Rowan and Stella" begins with "Rowan " -- and the note reached one of the two people it
// was written to, with nothing anywhere saying the other had been dropped. That is the
// silent wrong-reader failure this whole file exists to prevent, and it is pinned here in
// every spelling the table uses.
func TestAndIsASeparatorNotAQualifier(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, line, want string }{
		{"and", "Rowan and Stella", "Rowan; Stella"},
		{"ampersand", "Rowan & Stella", "Rowan; Stella"},
		{"comma and", "Rowan, Stella, and Glenn", "Rowan; Stella; Glenn"},
		{"and after a semicolon list", "Rowan; Stella and Glenn", "Rowan; Stella; Glenn"},
		{"capitalised", "Rowan AND Stella", "Rowan; Stella"},
		{"an alias on the far side", "Rowan and Stella Codex", "Rowan; Stella"},
		{"a group and a name", "Everybody at the table and Glenn", "Rowan; Stella; Glenn"},
		{"inside a parenthetical, which is not a separator", "Rowan (bud and keeper)", "Rowan"},
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
	got, unknown := c.ResolveList("Rowan reads in place")
	if len(unknown) > 0 || strings.Join(got, "; ") != "Rowan" {
		t.Fatalf(`ResolveList("Rowan reads in place") = %v %v, want Rowan: an instance qualifier is not a second reader`, got, unknown)
	}
}

// Two known names in ONE token, with no separator between them, is refused rather than
// resolved to the first. Whatever "Rowan Stella" is, it is not a note to Rowan, and a tool
// that picked one of the two would be guessing about a reader.
func TestAKnownNameFollowedByAKnownNameIsRefused(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"Rowan Stella", "Rowan Stella Codex", "Stella Glenn", "Rowan the keeper"} {
		got, unknown := c.ResolveList(line)
		if len(unknown) == 0 {
			t.Fatalf("ResolveList(%q) = %v with nothing unresolved; two names in one token is a refusal", line, got)
		}
	}
	// A From line naming two people is not a sender, in either spelling.
	for _, line := range []string{"Rowan and Stella", "Rowan Stella"} {
		if p, ok := c.ResolveOne(line); ok {
			t.Fatalf("ResolveOne(%q) = %q; a note has one writer", line, p.Name)
		}
	}
}
