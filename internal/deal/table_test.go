package deal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBenchesReadTheirCapabilitiesFromTheRegistry: the consumers table is not a new file.
// A bench's capabilities are tokens in its registry row's notes, and a machine the registry
// refuses to call a bench is not a consumer at all.
func TestBenchesReadTheirCapabilitiesFromTheRegistry(t *testing.T) {
	got, err := Benches(filepath.Join("testdata", "machines.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Capabilities{}
	var names []string
	for _, c := range got {
		byName[c.Name] = c
		names = append(names, c.Name)
	}
	for _, refused := range []string{"studio", "mini"} {
		if _, ok := byName[refused]; ok {
			t.Fatalf("%s is not a bench in the registry and must not be a consumer (%v)", refused, names)
		}
	}
	hulk, ok := byName["hulk"]
	if !ok {
		t.Fatalf("hulk is a bench and must be a consumer (%v)", names)
	}
	if hulk.Locality != LocalityDatacenter {
		t.Fatalf("hulk's locality is %q, wants datacenter", hulk.Locality)
	}
	if hulk.MaxWallMinutes != 180 || hulk.Width != 12 {
		t.Fatalf("hulk's wall bound is %d and width %d; wants 180 and 12", hulk.MaxWallMinutes, hulk.Width)
	}
	if !has(hulk.Legs, "c+cpp") || !has(hulk.Mirrors, "mas-bandwidth/nova-tools@dev") {
		t.Fatalf("hulk's legs are %v and mirrors %v", hulk.Legs, hulk.Mirrors)
	}
	if superman := byName["superman"]; superman.Locality != LocalityHouse {
		t.Fatalf("superman's locality is %q; it is on the house uplink", superman.Locality)
	}
}

// TestBenchesRefuseAMalformedValueForATokenItKnows: an unknown token is ignored (the registry
// is shared with verbs that do not know this vocabulary), but `wall=soon` is refused by name
// rather than read as no bound at all.
func TestBenchesRefuseAMalformedValueForATokenItKnows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "machines.tsv")
	row := strings.Join([]string{"hulk", "hulk", "linux/x64", "bench", "swarm-hulk", "64", "wall=soon"}, "\t")
	if err := os.WriteFile(path, []byte(row+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Benches(path)
	if err == nil || !strings.Contains(err.Error(), "wall wants whole minutes") {
		t.Fatalf("Benches answered %v; it must refuse wall=soon by name", err)
	}
}

// TestFriendsAreConsumersOfWidthFour: a friend is a row of the same table, and their width is
// Glenn's four.
func TestFriendsAreConsumersOfWidthFour(t *testing.T) {
	got := FriendsFromNames([]string{"Johnny", " stella ", ""})
	if len(got) != 2 {
		t.Fatalf("FriendsFromNames returned %d rows, wants 2", len(got))
	}
	for _, c := range got {
		if c.Kind != KindFriend || c.Width != FriendWidth {
			t.Fatalf("%s is %q with width %d; a friend is a consumer of width four", c.Name, c.Kind, c.Width)
		}
		if c.Name != strings.ToLower(strings.TrimSpace(c.Name)) {
			t.Fatalf("the name %q is not the lowercase key presence is read by", c.Name)
		}
	}
}

// TestApplyPresenceIsTheOnlyThingThatSaysUp: a consumer is present because a key says so,
// never because it was in the table.
func TestApplyPresenceIsTheOnlyThingThatSaysUp(t *testing.T) {
	table := FriendsFromNames([]string{"johnny", "freddy"})
	got := ApplyPresence(table, map[string]bool{"johnny": true})
	for _, c := range got {
		switch c.Name {
		case "johnny":
			if !c.Present {
				t.Fatalf("johnny's heartbeat is fresh and he reads as away")
			}
		case "freddy":
			if c.Present {
				t.Fatalf("freddy has no key and reads as present")
			}
		}
	}
}

// TestLoadTableRefusesToGuess: no registry and no friends is a refusal naming both doors,
// never an empty table that silently routes nothing.
func TestLoadTableRefusesToGuess(t *testing.T) {
	_, err := LoadTable("", "", nil)
	if err == nil || !strings.Contains(err.Error(), "refuses to guess") {
		t.Fatalf("LoadTable answered %v; an empty table must be a refusal that names its doors", err)
	}
}
