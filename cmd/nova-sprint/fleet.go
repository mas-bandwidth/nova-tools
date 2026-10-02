package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// fleetWords is how a fleet member's status comes about, in nova-sprint help
// and nova-sprint help fleet.
func fleetWords() string {
	return strings.TrimSpace(`
The fleet: each member says it is there with nova-sprint fleet beat <member>,
run on the machine every few seconds; a beat writes the time and the
machine's load. A beat window is `+sprint.BeatDeadline.String()+`; a member is up until it has missed `+fmt.Sprint(sprint.MissedBeatsDown)+` windows
in a row (one missed beat marks nothing; a beat resets the count) and down past
that or when it has never beaten; the tick applies each change
(a member down has its unfinished work cards dealt to the members up; a
member up levels the ready queues). fleet down holds a member down whatever
it beats (status held); fleet up releases the hold, adding a member the
sprint does not know. The load cell is the machine's CPU busy percent of all
its cores (the one-minute load average over the cores where that cannot be
measured), the highest of the last `+sprint.LoadWindow.String()+`.

fleet sync makes the fleet match nova-config's machine rows in one step (--pg,
else NOVA_PG_DSN, as nova-config takes it): a member the table lacks is added
at its width (the row's width, nova-config machine set <m> --width <n>; a row
with no width takes half the cores its machine's beat reports), a
width that differs is set, a width of 0 is no member and its row is held and
its cards are dealt to the members that stay; a row with no machine row is held
the same way and removed (its row and width out of the fleet) once no card stays
on it, and comes back when its machine row does; nothing else changes, and a
second sync writes nothing. --check
prints the drift and writes nothing: exit 0 none, 2 some, 3 the config cannot
be read.`) + "\n"
}

// readerWords is how a reader's state comes about, in nova-sprint help and
// nova-sprint help reader.
func readerWords() string {
	return strings.TrimSpace(`
The readers: a reader is a row of the readers table, which the coordinator
declares (init --readers, reader add); no beat and no loop record makes one. A
reader with its row says it is there by asking for its own queue (queue --as
<reader> is its beat); the queue of a name with no row writes none and answers
reader false, and the reader loop says MEMBER NOT A READER. A reader is up while its last beat is under `+sprint.ReaderBeatBound.String()+` old, away
when it beat and has lapsed, down when it has never beaten; reader away holds
one away whatever it beats and reader up releases the hold. The ask deals a
read to a reader up only: a read asked of a reader that is not up is asked of
another at the next tick, and with fewer than two readers up the tick asks none
and raises one judgment (fewer than two readers up). reader remove takes a row off the readers table, refused while the
reader holds a read (asked, reading, ok or broken).`) + "\n"
}

// fleetStep is the coordinator's fleet verb as a step: up releases a hold,
// counts as a beat of the member, and brings it up at once when it is alive, and sets its
// width when width is above zero; down holds it down; level evens the ready
// queues.
func (a *app) fleetStep(st *store.Store, op, member, who string, width int) store.Step {
	r := sprint.FleetReq{Op: op, Member: member, Who: who, Width: width}
	switch op {
	case "up":
		r.Op = "release"
		// a member a sync removed in this epoch comes back with its own control
		// card, placed again before the step reads it (store.RejoinMembers)
		// ignored: a rejoin that failed leaves the card off the table, and the step's create of it is refused by the store, naming the member
		_, _ = st.RejoinMembers(context.Background(), []string{member})
		// the release counts as a beat (docs/SPEC-SPRINT.md section 5): the
		// member's last beat is now, so the next tick within the beat window
		// finds it up
		// ignored: a store that keeps no beats leaves the beat as it was, and the read below says so
		_, _ = st.TouchBeat(context.Background(), member)
		if beats, err := st.Beats(context.Background(), []string{member}); err == nil {
			r.Fresh = beats[member].Alive(a.now())
		}
	case "down":
		r.Op, r.Why = "hold", "held by "+who
	}
	return store.FleetStep(r)
}

// beatReport is what fleet beat prints with --json.
type beatReport struct {
	Member string    `json:"member"`
	At     time.Time `json:"at"`
	Load   float64   `json:"load"`
	Last   float64   `json:"last"`
	How    string    `json:"how"`
	Cores  int       `json:"cores"`
}

func (a *app) cmdFleetBeat(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("fleet beat")
	load := fs.String("load", "", "the load as a percent of all the machine's cores, instead of measuring it (a test's, or another meter's)")
	cores := fs.Int("cores", 0, "the machine's logical cores the beat reports, instead of this machine's own (a test's, or another meter's); a member with the default width takes half")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "fleet beat", err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, "fleet beat", "wants one member")
	}
	var given *float64
	if *load != "" {
		v, err := strconv.ParseFloat(strings.TrimSuffix(*load, "%"), 64)
		if err != nil {
			return refuse(stderr, "fleet beat", "--load wants a percent, found "+*load)
		}
		given = &v
	}
	if *cores < 0 {
		return refuse(stderr, "fleet beat", fmt.Sprintf("--cores wants a count of logical cores of at least 1, found %d", *cores))
	}
	src := a.meter
	if *cores > 0 {
		src.NCPU = *cores
	}
	c.orActor(pos[0])
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "fleet beat", err.Error())
	}
	b, err := st.Beat(context.Background(), pos[0], given, src)
	if err != nil {
		fmt.Fprintf(stderr, "%s fleet beat: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	last := 0.0
	if n := len(b.Samples); n > 0 {
		last = b.Samples[n-1].Pct
	}
	if c.json {
		out, _ := json.Marshal(beatReport{Member: pos[0], At: b.At, Load: b.Load, Last: last, How: b.How, Cores: b.Cores})
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fmt.Fprintf(stdout, "FLEET-BEAT OK %s at=%s load=%.1f%% last=%.1f%% how=%s cores=%d\n", pos[0], b.At.Format(time.RFC3339), b.Load, last, b.How, b.Cores)
	return 0
}
