package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestNewPathBatchedReadOkAccepts (the 4806 read, B2): one read --ok naming
// several read cards is one line of the log about their primaries, and R9
// reads that line's primaries: every primary with two ok reads is accepted,
// none is quarantined, and none is left in review. Before, the line's ids (the
// read cards) were read as primaries, the accept found no such primaries in
// review and the read cards were quarantined.
func TestNewPathBatchedReadOkAccepts(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	for _, l := range npTicking {
		na.ok(l)
	}
	na.ok("tick")
	type qcard struct {
		ID  string `json:"id"`
		Col string `json:"col"`
		Gen int    `json:"gen"`
	}
	queue := func(who string) []qcard {
		var q struct {
			Cards []qcard `json:"cards"`
		}
		if err := json.Unmarshal([]byte(na.ok("queue --as "+who+" --json")), &q); err != nil {
			t.Fatal(err)
		}
		return q.Cards
	}
	words := func(cs []qcard) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.ID+"@"+strconv.Itoa(c.Gen))
		}
		return strings.Join(out, " ")
	}
	for _, m := range []string{"m1", "m2"} {
		na.ok("take --as " + m + " " + words(queue(m)))
		na.ok("finish --as " + m + " " + words(queue(m)))
	}
	na.ok("tick")
	na.ok("tick")
	for _, r := range []string{"reader-a", "reader-b"} {
		var ids []string
		for i := 1; i <= 6; i++ {
			ids = append(ids, "s1-"+strconv.Itoa(i)+".r1."+r) // sprint.ReadCardID at attempt 1
		}
		na.ok("read --as " + r + " --begin " + strings.Join(ids, " "))
		na.ok("read --as " + r + " --ok " + strings.Join(ids, " "))
	}
	na.ok("tick")
	na.ok("tick")
	var w struct {
		Tables map[string]map[string]map[string]string `json:"tables"`
	}
	if err := json.Unmarshal([]byte(na.ok("where --json")), &w); err != nil {
		t.Fatal(err)
	}
	if got := w.Tables["work"]["s1"]; got["merging"] != "6" || got["review"] != "0" {
		t.Errorf("after two batched read --ok: work %v, want 6 merging and none in review", got)
	}
	if in := na.ok("inbox"); strings.Contains(in, "invariant") || strings.Contains(in, "quarantin") {
		t.Errorf("the inbox:\n%s", in)
	}
}

// TestNewPathAcceptReadOK (the 4806 read, M5): accept --read-ok accepts every
// primary in review with ok reads from two different readers, in every
// stream, and leaves the rest in review; before, the new path refused it.
func TestNewPathAcceptReadOK(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	for _, l := range npTicking {
		na.ok(l)
	}
	na.ok("tick")
	type qcard struct {
		ID  string `json:"id"`
		Gen int    `json:"gen"`
	}
	for _, m := range []string{"m1", "m2"} {
		var q struct {
			Cards []qcard `json:"cards"`
		}
		if err := json.Unmarshal([]byte(na.ok("queue --as "+m+" --json")), &q); err != nil {
			t.Fatal(err)
		}
		var words []string
		for _, c := range q.Cards {
			words = append(words, c.ID+"@"+strconv.Itoa(c.Gen))
		}
		na.ok("take --as " + m + " " + strings.Join(words, " "))
		na.ok("finish --as " + m + " " + strings.Join(words, " "))
	}
	na.ok("tick")
	na.ok("tick")
	// four of the six read ok by both readers; the machine is stopped, so R9
	// does not accept them before the coordinator does
	na.ok("stop")
	for _, r := range []string{"reader-a", "reader-b"} {
		var ids []string
		for i := 1; i <= 4; i++ {
			ids = append(ids, "s1-"+strconv.Itoa(i)+".r1."+r)
		}
		na.ok("read --as " + r + " --begin " + strings.Join(ids, " "))
		na.ok("read --as " + r + " --ok " + strings.Join(ids, " "))
	}
	out := na.ok("accept --read-ok")
	var w struct {
		Tables map[string]map[string]map[string]string `json:"tables"`
	}
	if err := json.Unmarshal([]byte(na.ok("where --json")), &w); err != nil {
		t.Fatal(err)
	}
	if got := w.Tables["work"]["s1"]; got["merging"] != "4" || got["review"] != "2" {
		t.Errorf("accept --read-ok: work %v, want 4 merging and 2 in review\n%s", got, out)
	}
}
