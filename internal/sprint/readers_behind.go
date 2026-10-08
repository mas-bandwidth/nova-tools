package sprint

import (
	"cmp"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// The readers behind (docs/SPEC-SPRINT.md, "the readers are behind"; the owner, 2026-10-03:
// "This is another type of thing that should be escalated to you mechanically"). One night
// review held 75 cards while five readers read 44: the readers kept the widths they started
// with after their machines were widened, and nobody was told. The decision is one pure
// function over the snapshot, ReadersBehind, beside Overloaded: reads have sat asked and not
// begun on readers up for ReadersWindow, so the readers read less than review holds. A
// reader's width is its machine row's (Snapshot.ReaderWidth, readers.go), and the ask gives a
// reader no more than its width: when reads wait on a reader reading under its width, its loop
// lags its row (it kept the width it started with) and is restarted; when it reads its width,
// the machine is the bound.

// ReadersWindow is how long reads sit asked and not begun on readers up before the readers
// are behind.
const ReadersWindow = 10 * time.Minute

// NReadersBehind is the tick's judgment that the readers are behind, one for the sprint.
const NReadersBehind = "the readers are behind"

// ReaderLoad is one reader's reads: reading now, asked and not begun past the window (a
// read asked in the step being planned is never past it, so the fact holds across the
// step), its width (Snapshot.ReaderWidth; 0 when its name is no machine's or the machine
// has no row), and its state.
type ReaderLoad struct {
	Reader  string
	Reading int
	Late    int
	Width   int
	State   string
}

// Room says the reader had room for a read it did not begin: it reads under its width, or
// its width is not known (0: no machine row bounds it).
func (l ReaderLoad) Room() bool { return l.Width == 0 || l.Reading < l.Width }

// Lags says the reader's loop lags its width: reads wait on it past the window, up, while
// it reads below its width.
func (l ReaderLoad) Lags() bool {
	return l.State == ReaderUp && l.Late > 0 && l.Width > 0 && l.Reading < l.Width
}

// ReadersLag is the readers behind: review's count and every reader that holds reads.
type ReadersLag struct {
	Review  int
	Readers []ReaderLoad
	Window  time.Duration // the window (readers_window); 0 is ReadersWindow
}

// ReadersBehind is whether the readers are behind, and the facts: a read has sat asked and
// not begun for ReadersWindow or longer on a reader up with room for it (ReaderLoad.Room).
// A reader that is not up is taken back by the ask, so its reads never count toward the
// window; it is named when it holds reads, for reader up. A reader reading its whole width
// is busy, not behind: reads waiting on it raise nothing (ReadersFull; on the night of
// 2026-10-05 "the readers are behind" rose while every reader was busy).
func ReadersBehind(s *Snapshot) (ReadersLag, bool) {
	b, late, _ := readersLoad(s)
	return b, late > 0
}

// ReadersFull is why the readers are not behind though reads wait past the window: every
// reader up they wait on reads its whole width. ok is false when no read waits past the
// window on a reader up, or one waits on a reader with room (the readers are behind).
func ReadersFull(s *Snapshot) (string, bool) {
	b, late, full := readersLoad(s)
	if late > 0 || full == 0 {
		return "", false
	}
	var busy []string
	for _, l := range b.Readers {
		if l.State == ReaderUp && l.Late > 0 {
			busy = append(busy, fmt.Sprintf("%s reads %d of width %d", l.Reader, l.Reading, l.Width))
		}
	}
	return fmt.Sprintf("%d reads asked past %s wait on readers reading their whole width (%s): busy, not behind", full, cmp.Or(b.Window, ReadersWindow), strings.Join(busy, "; ")), true
}

// readersLoad is the readers' loads and the reads asked and not begun past the window on
// readers up: late on a reader with room, full on one reading its whole width. The loads
// are the readers', with review's count, whenever a read waits past the window on one up.
func readersLoad(s *Snapshot) (b ReadersLag, late, full int) {
	if s.Readers == nil || s.Work == nil {
		return ReadersLag{}, 0, 0
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
		if w := s.ReaderWidth(r); w != math.MaxInt {
			l.Width = w
		}
		loads[r] = l
		return l
	}
	for _, c := range s.Readers.Column(Asked, Reading) {
		l := load(c.Row)
		if c.Col == Reading {
			l.Reading++
			continue
		}
		if t, err := time.Parse(time.RFC3339, c.F("asked")); err == nil && s.Now.Sub(t) >= s.PolicyDuration(PolicyReadersWindow) {
			l.Late++
		}
	}
	// room is read once every read is counted: a reader's reads begun after its late one
	// in the column still fill its width
	for _, l := range loads {
		switch {
		case l.State != ReaderUp:
		case l.Room():
			late += l.Late
		default:
			full += l.Late
		}
	}
	if late+full == 0 {
		return ReadersLag{}, 0, 0
	}
	b = ReadersLag{Review: len(s.Work.Column(Review)), Window: s.PolicyDuration(PolicyReadersWindow)}
	for _, l := range loads {
		if l.Reading+l.Late > 0 {
			b.Readers = append(b.Readers, *l)
		}
	}
	sort.Slice(b.Readers, func(i, j int) bool { return b.Readers[i].Reader < b.Readers[j].Reader })
	return b, late, full
}

// What is the judgment's line: review, the readers' reading beside their widths, and what
// each needs.
func (b ReadersLag) What() string {
	reading, width := 0, 0
	var parts []string
	for _, l := range b.Readers {
		reading += l.Reading
		width += l.Width
		p := fmt.Sprintf("%s reads %d of width %d, %d waiting past the window", l.Reader, l.Reading, l.Width, l.Late)
		switch {
		case l.State != ReaderUp:
			p += ": " + l.State + ", run: nova-sprint reader up " + l.Reader
		case l.Lags():
			p += ": it reads under its width, restart its loop (nova-config loop show " + l.Reader + ")"
		}
		parts = append(parts, p)
	}
	// the window, never a wait: the text changes with the facts, not the clock, so the tick
	// rewrites it only when the facts change (notify, update)
	return fmt.Sprintf("the readers are behind: review %d, reads asked and not begun past %s; the readers read %d of width %d (%s)",
		b.Review, cmp.Or(b.Window, ReadersWindow), reading, width, strings.Join(parts, "; "))
}

// Decisions are the judgment's: reader up for each reader not up that holds reads, a
// restart for each that reads under its width, and wait 10m.
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
