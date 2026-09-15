package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBenchTSV(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "benches.tsv")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The seven columns parse, a "-" cores row is no pinning, and every table refusal
// above names the row that failed.
func TestBenchTableParsed(t *testing.T) {
	p := writeBenchTSV(t, "name\thost\troot\tcores\tharness\tauth\twall\n"+
		"b2\tb2\t/home/me/swarm\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone\n"+
		"mac\tmac\t/Users/me/swarm\t-\t/Users/me/bin/opencode\t/Users/me/.config/nova/auth\tsandbox\n")
	table, err := LoadBenchTable(p)
	if err != nil {
		t.Fatalf("a valid table parses: %v", err)
	}
	if len(table) != 2 {
		t.Fatalf("want 2 rows, got %d", len(table))
	}
	if table[0].Name != "b2" || table[0].Host != "b2" || table[0].Cores != "1-15" || table[0].Wall != "none" {
		t.Errorf("row 1 wrong: %+v", table[0])
	}
	if table[1].Cores != "-" || table[1].Pinned() {
		t.Errorf("row 2 is a - cores row and does not pin: %+v", table[1])
	}
	if table[0].Pinned() != true {
		t.Errorf("a list cores row pins")
	}

	refusals := []string{
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\trelative/swarm\t1-15\t/h\t/a\tnone\n",                     // relative root
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\trel-h\t/a\tnone\n",                           // relative harness
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\t/h\trel-a\tnone\n",                           // relative auth
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\tx-y\t/h\t/a\tnone\n",                               // cores list does not parse
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\t/h\t/a\twall\n",                              // wall neither word
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\t/h\t/a\tnone\nb9\tb9\t/r\t-\t/h\t/a\tnone\n", // name used once
	}
	for i, body := range refusals {
		if i == len(refusals)-1 {
			body = "name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\t/h\t/a\tnone\nb2\tb2\t/r\t-\t/h\t/a\tnone\n"
		}
		rp := writeBenchTSV(t, body)
		if _, err := LoadBenchTable(rp); err == nil {
			t.Errorf("case %d: expected a refusal", i)
		}
	}
}

// A cores column parses into sorted cores, and "-" into none.
func TestCoresList(t *testing.T) {
	got, err := CoresList("1-15")
	if err != nil || len(got) != 15 || got[0] != 1 || got[14] != 15 {
		t.Fatalf("1-15 parses to 15 cores: %v %v", got, err)
	}
	got, err = CoresList("2,4,6")
	if err != nil || len(got) != 3 || got[0] != 2 || got[2] != 6 {
		t.Fatalf("2,4,6 parses to three cores: %v %v", got, err)
	}
	got, err = CoresList("-")
	if err != nil || got != nil {
		t.Fatalf("- is no pinning: %v %v", got, err)
	}
	if _, err := CoresList("5-2"); err == nil {
		t.Error("a reversed range refuses")
	}
}

// A bench name on the bus's friends list is refused, and nothing is written to the
// bus: the check only reads the roster.
func TestBenchNameIsNotAFriend(t *testing.T) {
	busDir := t.TempDir()
	roster := busDir + "/participants.json"
	if err := os.WriteFile(roster, []byte(`{"participants":[{"name":"alice"},{"name":"bob"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	friends, err := FriendNames(busDir)
	if err != nil {
		t.Fatalf("friends read: %v", err)
	}
	if len(friends) != 2 {
		t.Fatalf("want 2 friends, got %v", friends)
	}
	if got := RefuseFriend("alice", friends); got != "ADMIT REFUSED bench=alice is a friend" {
		t.Errorf("a friend name refuses: %q", got)
	}
	if got := RefuseFriend("b2", friends); got != "" {
		t.Errorf("a non-friend name admits: %q", got)
	}
	// The bus saw no line: only the roster file exists.
	entries, err := os.ReadDir(busDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "participants.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the bus should see no new file, got %v", strings.Join(names, ","))
	}
}
