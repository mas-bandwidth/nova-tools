package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The readers behind (docs/SPEC-SPRINT.md, "the readers are behind"; the owner, 2026-10-03:
// "This is another type of thing that should be escalated to you mechanically"). One night
// review held 75 cards while five readers read 44: the readers kept the widths they started
// with after their machines were widened, and nobody was told. The decision is one pure
// function over the snapshot, ReadersBehind, beside Overloaded: reads have sat asked and not
// begun on readers up for ReadersWindow, so the readers read less than review holds. The
// sprint knows no reader's own width (a reader runs at its machine's row, readers.go), so a
// reader's width here is what it reads while reads wait on it: when that is under its
// machine's width, its width lags its machine and its loop is restarted; when it equals it,
// the machine is the bound.

// ReadersWindow is how long reads sit asked and not begun on readers up before the readers
// are behind.
const ReadersWindow = 10 * time.Minute

// NReadersBehind is the tick's judgment that the readers are behind, one for the sprint.
const NReadersBehind = "the readers are behind"

// ReaderLoad is one reader's reads: reading now, asked and not begun past the window (a
// read asked in the step being planned is never past it, so the fact holds across the
// step), its machine's width (0 when its name is no machine's or the machine has no row),
// and its state.
type ReaderLoad struct {
	Reader       string
	Reading      int
	Late         int
	MachineWidth int
	State        string
}

// Width is the reader's width as the sprint sees it: what it reads while reads wait on it.
func (l ReaderLoad) Width() int { return l.Reading }

// Lags says the reader's width lags its machine's: reads wait on it past the window, up,
// below its machine's width.
func (l ReaderLoad) Lags() bool {
	return l.State == ReaderUp && l.Late > 0 && l.MachineWidth > 0 && l.Reading < l.MachineWidth
}

// ReadersLag is the readers behind: review's count and every reader that holds reads.
type ReadersLag struct {
	Review  int
	Readers []ReaderLoad
}

// ReadersBehind is whether the readers are behind, and the facts: a read has sat asked and
// not begun on a reader up for ReadersWindow or longer. A reader that is not up is taken
// back by the ask, so its reads never count toward the window; it is named when it holds
// reads, for reader up.
func ReadersBehind(s *Snapshot) (ReadersLag, bool) {
	if s.Readers == nil || s.Work == nil {
		return ReadersLag{}, false
	}
	loads := map[string]*ReaderLoad{}
	load := func(r string) *ReaderLoad {
		if l, ok := loads[r]; ok {
			return l
		}
		l := &ReaderLoad{Reader: r, State: ReaderDown}
		if s.ReaderIsUp(r) {
			l.State = ReaderUp
		} else if s.ReaderStates != nil {
			l.State = s.ReaderStates[r]
		}
		if m, ok := ReaderMachine(r); ok && s.Fleet != nil && s.MemberCtl(m) != nil {
			l.MachineWidth = s.Width(m)
		}
		loads[r] = l
		return l
	}
	late := 0
	for _, c := range s.Readers.Column(Asked, Reading) {
		l := load(c.Row)
		if c.Col == Reading {
			l.Reading++
			continue
		}
		if t, err := time.Parse(time.RFC3339, c.F("asked")); err == nil && s.Now.Sub(t) >= ReadersWindow {
			l.Late++
			if l.State == ReaderUp {
				late++
			}
		}
	}
	if late == 0 {
		return ReadersLag{}, false
	}
	b := ReadersLag{Review: len(s.Work.Column(Review))}
	for _, l := range loads {
		if l.Reading+l.Late > 0 {
			b.Readers = append(b.Readers, *l)
		}
	}
	sort.Slice(b.Readers, func(i, j int) bool { return b.Readers[i].Reader < b.Readers[j].Reader })
	return b, true
}

// What is the judgment's line: review, the readers' reading and widths beside their
// machines', and what each needs.
func (b ReadersLag) What() string {
	reading, width := 0, 0
	var parts []string
	for _, l := range b.Readers {
		reading += l.Reading
		width += l.Width()
		p := fmt.Sprintf("%s reads %d of width %d on a machine of width %d, %d waiting past the window", l.Reader, l.Reading, l.Width(), l.MachineWidth, l.Late)
		switch {
		case l.State != ReaderUp:
			p += ": " + l.State + ", run: nova-sprint reader up " + l.Reader
		case l.Lags():
			p += ": its width lags its machine, restart its loop (nova-config loop show " + l.Reader + ")"
		}
		parts = append(parts, p)
	}
	// the window, never a wait: the text changes with the facts, not the clock, so the tick
	// rewrites it only when the facts change (notify, update)
	return fmt.Sprintf("the readers are behind: review %d, reads asked and not begun past %s; the readers read %d of width %d (%s)",
		b.Review, ReadersWindow, reading, width, strings.Join(parts, "; "))
}

// Decisions are the judgment's: reader up for each reader not up that holds reads, a
// restart for each whose width lags its machine, and wait 10m.
func (b ReadersLag) Decisions() []string {
	var out []string
	for _, l := range b.Readers {
		switch {
		case l.State != ReaderUp:
			out = append(out, "reader up "+l.Reader)
		case l.Lags():
			out = append(out, "restart "+l.Reader)
		}
	}
	return append(out, "wait 10m")
}

// readersBehindCond is the tick's condition when the readers are behind (NReadersBehind).
func readersBehindCond(s *Snapshot) []cond {
	b, ok := ReadersBehind(s)
	if !ok {
		return nil
	}
	return []cond{{typ: NReadersBehind, streamLevel: true, what: b.What(), decisions: b.Decisions()}}
}
