package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestNewPathReworkGoesRoundTheFleet (the 4806 read, M3; errata 3 amendment 5,
// every placement): the coordinator's rework deals each next attempt to the
// next member round the fleet from the deal's rolling index, never the member
// that failed it while another has room, and moves the index with the step.
// Four members, eight cards failed, reworked in one verb: two next attempts a
// member. Before, the verb's read lacked the index and the widths and the
// planner panicked on the property it had not loaded.
func TestNewPathReworkGoesRoundTheFleet(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	members := []string{"m1", "m2", "m3", "m4"}
	for _, l := range []string{"init", "reader add reader-a reader-b", "fleet up " + strings.Join(members, " "),
		"fleet beat " + strings.Join(members, " "), "start", "tick", "tick", "add --stream s1 --count 8", "tick"} {
		na.ok(l)
	}
	type qcard struct {
		ID     string            `json:"id"`
		Col    string            `json:"col"`
		Gen    int               `json:"gen"`
		Fields map[string]string `json:"fields"`
	}
	queue := func(m string) []qcard {
		var q struct {
			Cards []qcard `json:"cards"`
		}
		if err := json.Unmarshal([]byte(na.ok("queue --as "+m+" --json")), &q); err != nil {
			t.Fatal(err)
		}
		return q.Cards
	}
	failedOn := map[string]string{}
	var primaries []string
	for _, m := range members {
		var words []string
		for _, c := range queue(m) {
			words = append(words, c.ID+"@"+strconv.Itoa(c.Gen))
			failedOn[c.Fields["primary"]] = m
			primaries = append(primaries, c.Fields["primary"])
		}
		if len(words) != 2 {
			t.Fatalf("the first deal gave %s %d cards, want 2", m, len(words))
		}
		na.ok("take --as " + m + " " + strings.Join(words, " "))
		na.ok("finish --as " + m + " --failed --report red " + strings.Join(words, " "))
	}
	na.ok("rework " + strings.Join(primaries, " ") + " --fix again")
	for _, m := range members {
		n := 0
		for _, c := range queue(m) {
			if c.Fields["attempt"] != "2" {
				continue
			}
			n++
			if p := c.Fields["primary"]; failedOn[p] == m {
				t.Errorf("%s's next attempt went to %s, the member that failed it", p, m)
			}
		}
		if n != 2 {
			t.Errorf("%s holds %d next attempts, want 2 (round the fleet)", m, n)
		}
	}
}
