package driver

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// The dogfood finding of 2026-09-30, "ticking halves" (the owner's words:
// "we need to not do this. whole fleet table, one update."): the driver reads
// the members' queues one verb at a time, and the machine's deal commits
// between two of those reads, so the members read before it show nothing
// ready and the ones after show their cards. The take is the whole fleet
// in the one step all the same: every member up takes, by selection, what is
// ready when the take runs, and no part of the fleet falls a tick behind the
// rest.
func TestADealBetweenTheQueueReadsStillTakesTheWholeFleet(t *testing.T) {
	t.Parallel()
	var members []string
	fleet := []string{}
	for i := 1; i <= 8; i++ {
		m := fmt.Sprintf("m%d", i)
		members = append(members, m)
		fleet = append(fleet, fmt.Sprintf(`"%s":{"status":"up"}`, m))
	}
	where := `{"landed":0,"all":2,"summary":"0/2","machine":"machine: running","tables":{"fleet":{` + strings.Join(fleet, ",") + `}},"streams":[]}`
	reads := 0
	var takes []string
	run := func(args []string, stdout, stderr io.Writer) int {
		switch {
		case args[0] == "where":
			fmt.Fprintln(stdout, where)
		case args[0] == "queue":
			reads++
			// the deal commits after the first four members' queues are read:
			// m1..m4 show nothing ready, m5..m8 one card each
			if reads <= 4 {
				fmt.Fprintln(stdout, `{"cards":[]}`)
			} else {
				fmt.Fprintf(stdout, `{"cards":[{"id":"%s-1.w1","col":"ready","gen":1}]}`+"\n", args[2])
			}
		case args[0] == "take":
			takes = append(takes, args[2])
			fmt.Fprintln(stdout, "TAKE OK moved=8 refused=0")
		default:
			fmt.Fprintf(stdout, "%s OK moved=0 refused=0\n", strings.ToUpper(args[0]))
		}
		return 0
	}
	var out bytes.Buffer
	d := &Driver{Run: run, Facts: NewSeeded(1), Clock: &fakeClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)},
		Out: &out, Config: Config{Every: time.Second, Ticks: 1}}
	if _, err := d.Loop(); err != nil {
		t.Fatalf("loop: %v\n%s", err, out.String())
	}
	if want := strings.Join(members, ","); len(takes) != 1 || takes[0] != want {
		t.Fatalf("the takes named %q, want one take of the whole fleet %q:\n%s", takes, want, out.String())
	}
}

// A tick with no member's queue showing a ready card runs no take.
func TestNoReadyCardNoTake(t *testing.T) {
	t.Parallel()
	where := `{"landed":0,"all":2,"summary":"0/2","machine":"machine: running","tables":{"fleet":{"m1":{"status":"up"},"m2":{"status":"up"}}},"streams":[]}`
	var ran []string
	run := func(args []string, stdout, stderr io.Writer) int {
		ran = append(ran, args[0])
		switch args[0] {
		case "where":
			fmt.Fprintln(stdout, where)
		case "queue":
			fmt.Fprintln(stdout, `{"cards":[]}`)
		default:
			fmt.Fprintf(stdout, "%s OK moved=0 refused=0\n", strings.ToUpper(args[0]))
		}
		return 0
	}
	d := &Driver{Run: run, Facts: NewSeeded(1), Clock: &fakeClock{now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)},
		Out: &bytes.Buffer{}, Config: Config{Every: time.Second, Ticks: 1}}
	if _, err := d.Loop(); err != nil {
		t.Fatal(err)
	}
	for _, v := range ran {
		if v == "take" {
			t.Fatalf("a take with nothing ready: %v", ran)
		}
	}
}
