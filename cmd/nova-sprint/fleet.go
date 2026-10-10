package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
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
member up levels the ready queues). fleet hold <member> --reason <text> (hold
<member>) holds a member whatever it beats (status held), and fleet down
<member> is hold --return in the old words; fleet unhold <member> (unhold), or
fleet up, releases the hold, fleet up adding a member the
sprint does not know; fleet up <m> --width 0 drains a member instead: no new
deal reaches it, its untaken ready cards are levelled away and its working
cards finish where they are (fleet down deals them again elsewhere); --width
<n> ends the drain. Each says where the cards went on its MOVED line: down
"moved=N to <member>(n),...; stayed=K withdrawn: <ids>" (a card no member up
has room for is withdrawn and dealt again where there is room), up
"moved=N to <member>(n) from <member>(n),..." (the level). fleet up --deadline <d> pins
the deadline every card dealt to the member gets (--deadline default takes the
pin off): a card's deadline is otherwise the larger of its own and three times
the member's median run wall over its last fifty ok attempts, so a slow
machine does not time out twice as often. The load cell is the machine's CPU busy percent of all
its cores (the one-minute load average over the cores where that cannot be
measured), the highest of the last `+sprint.LoadWindow.String()+`. A beat measures the
machine's open file descriptors beside its load, given or measured: over --fd-warn
(else NOVA_FD_WARN, else `+fmt.Sprint(hostload.FilesWarnDefault)+`) it says warn and lists the top
holders, read at most once every `+hostload.HoldersEvery.String()+`; over --fd-alarm (else
NOVA_FD_ALARM, else `+fmt.Sprint(hostload.FilesAlarmDefault)+`) it says alarm, and the tick writes
one judgment of the member ("`+sprint.NFilesAlarm+`") naming the count, the top
holders and its cards that ended on a timeout, closed with one cleared note when
the count falls under the alarm.

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
one away whatever it beats and reader up releases the hold (the old words of
hold <reader> --return and unhold <reader>: its state reads held). A flash card is
read once and a pro card twice, by two different readers, one read at a time
(the second asked once the first comes back ok), each read on a route of the
card's tier. The ask deals a read to a reader up only: a read asked of a
reader that is not up is asked of another at the next tick, and a card that
needs more readers than are up is not asked: the tick raises one judgment
(fewer than two readers up). A reader row carries the tiers it reads: reader add --tiers
flash[,pro,heavy,frontier] and reader set --tiers. Omitted, all and default
store an empty cell, which means every tier (today's behaviour). The ask
counts a reader only for a primary whose read tier it reads, and never asks
it a read outside those tiers. A card with fewer readers of its tier up than
it needs raises that same judgment, and a returned read is never asked again
in place of a reader outside the tier. where prints the tiers, all when the
cell is empty. reader remove takes a row off the readers table, refused while the
reader holds a read (asked, reading, ok or broken); reader retire keeps the
row and its read cards (the history) and takes the reader off the table for
good: never asked, its queue no beat and reader false, until reader up brings
it back.`) + "\n"
}

// fleetStep is the coordinator's fleet verb as a step: up releases a hold,
// counts as a beat of the member, and brings it up at once when it is alive, and sets its
// width when width is above zero, and drains it at a width of 0; level evens
// the ready queues (fleet down is hold --return, hold.go).
func (a *app) fleetStep(st *store.Store, op, member, who string, width int, drain bool, deadline int, deadlineOff bool) store.Step {
	r := sprint.FleetReq{Op: op, Member: member, Who: who, Width: width, Drain: drain, Deadline: deadline, DeadlineOff: deadlineOff}
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
	}
	return store.FleetStep(r)
}

// beatReport is what fleet beat prints with --json.
type beatReport struct {
	Member string          `json:"member"`
	At     time.Time       `json:"at"`
	Load   float64         `json:"load"`
	Last   float64         `json:"last"`
	How    string          `json:"how"`
	Cores  int             `json:"cores"`
	Files  *hostload.Files `json:"files,omitempty"`
	// StopReturns is how many stop-returns the member's lanes still owe (section 14).
	StopReturns int `json:"stop_returns,omitempty"`
}

// fdBound is one of fleet beat's open-files bounds: the flag's count when given, else the
// environment's, else 0 (the default); a count under 1 is refused.
func (a *app) fdBound(flagged int, given bool, flag, env string) (int, string) {
	if given {
		if flagged < 1 {
			return 0, fmt.Sprintf("--%s wants a count of open file descriptors of at least 1, found %d", flag, flagged)
		}
		return flagged, ""
	}
	v := strings.TrimSpace(a.getenv(env))
	if v == "" {
		return 0, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, env + " wants a count of open file descriptors of at least 1, found " + v
	}
	return n, ""
}

func (a *app) cmdFleetBeat(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("fleet beat")
	load := fs.String("load", "", "the load as a percent of all the machine's cores, instead of measuring it (a test's, or another meter's)")
	cores := fs.Int("cores", 0, "the machine's logical cores the beat reports, instead of this machine's own (a test's, or another meter's); a member with the default width takes half")
	stopReturns := fs.Int("stop-returns", 0, "how many stop-returns the member's lanes still owe after the machine's stop (section 14): start waits for zero")
	fdWarn := fs.Int("fd-warn", 0, fmt.Sprintf("the machine's open file descriptors above which the beat says warn and lists the top holders (else NOVA_FD_WARN, else %d)", hostload.FilesWarnDefault))
	fdAlarm := fs.Int("fd-alarm", 0, fmt.Sprintf("the machine's open file descriptors above which the beat says alarm and the tick writes one judgment of the member (else NOVA_FD_ALARM, else %d)", hostload.FilesAlarmDefault))
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
	if a.serving {
		// A served beat names a remote member: this process cannot read its files.
		src.OpenFiles, src.Holders = nil, nil
	}
	if *cores > 0 {
		src.NCPU = *cores
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var why string
	if src.FilesWarn, why = a.fdBound(*fdWarn, set["fd-warn"], "fd-warn", "NOVA_FD_WARN"); why != "" {
		return refuse(stderr, "fleet beat", why)
	}
	if src.FilesAlarm, why = a.fdBound(*fdAlarm, set["fd-alarm"], "fd-alarm", "NOVA_FD_ALARM"); why != "" {
		return refuse(stderr, "fleet beat", why)
	}
	if warn, alarm := hostload.FilesBounds(src); alarm < warn {
		return refuse(stderr, "fleet beat", fmt.Sprintf("--fd-alarm wants a count at or above the warn bound %d, found %d", warn, alarm))
	}
	if given != nil {
		// a load given is the beat's; the machine's open files are measured beside it
		// (hostload.Source.Given), so a member that gives its one-second samples still
		// reports them
		if *given < 0 || *given > hostload.MaxPercent {
			fmt.Fprintf(stderr, "%s fleet beat: %s\n", prog, oneline.Escape(fmt.Sprintf("a load is a percent from 0 to %v, found %v", hostload.MaxPercent, *given)))
			return 1
		}
		v := *given
		src.Given = func() (float64, bool) { return v, true }
	}
	c.orActor(pos[0])
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "fleet beat", err.Error())
	}
	var owing *int
	if set["stop-returns"] {
		if *stopReturns < 0 {
			return refuse(stderr, "fleet beat", "--stop-returns wants a whole number of at least 0")
		}
		v := *stopReturns
		owing = &v
	}
	b, err := st.BeatOwing(context.Background(), pos[0], nil, src, owing)
	if err != nil {
		fmt.Fprintf(stderr, "%s fleet beat: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	last := 0.0
	if n := len(b.Samples); n > 0 {
		last = b.Samples[n-1].Pct
	}
	files := b.Meter.Files
	if c.json {
		out, _ := json.Marshal(beatReport{Member: pos[0], At: b.At, Load: b.Load, Last: last, How: b.How, Cores: b.Cores, Files: files, StopReturns: b.StopReturns})
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fmt.Fprintf(stdout, "FLEET-BEAT OK %s at=%s load=%.1f%% last=%.1f%% how=%s cores=%d", pos[0], b.At.Format(time.RFC3339), b.Load, last, b.How, b.Cores)
	if files != nil {
		fmt.Fprintf(stdout, " fds=%d fds-max=%d fds-level=%s", files.Open, files.Max, files.Level())
	}
	if b.StopReturns > 0 {
		fmt.Fprintf(stdout, " stop_returns=%d", b.StopReturns)
	}
	fmt.Fprintln(stdout)
	if files != nil && files.Level() != hostload.LevelOK {
		if files.TopErr != "" {
			fmt.Fprintf(stdout, "  holders not listed: %s\n", oneline.Escape(files.TopErr))
		}
		for _, h := range files.Top {
			fmt.Fprintf(stdout, "  %s\n", oneline.Escape(h.String()))
		}
	}
	return 0
}
