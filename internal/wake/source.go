package wake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A source is a thing with a STATE VALUE and a rule for what makes two state
// values different. That is the whole design, and it is why there is one
// comparison in one place (State.Observe) rather than one per source: a fourth
// source cannot invent its own idea of what a change is.
type Source interface {
	// Name is the source's name in fail:<source> and on WAKE BROKEN and WAKE
	// POLL: bus, entries or reports.
	Name() string
	// Every is this source's poll cadence. The bus's is --interval and an
	// entry's is --entry-interval, because an 8-minute hosted run deserves one
	// check at 8 minutes and not eight checks at one minute.
	Every() time.Duration
	// Poll observes the world once. It returns the items whose value may have
	// moved -- the comparison is the caller's -- and the lines that are shown
	// on every poll they stand and woken on once.
	Poll(ctx context.Context, now time.Time) (Result, error)
}

// Result is one poll's observation.
type Result struct {
	// StandingKeys are the state keys of the lines this poll showed as
	// standing, so their recency can be kept: shown every time, woken on once.
	StandingKeys []string

	// Items are the observations, each with a state key, a state value and a
	// delivery id. Whether one is a CHANGE is decided in State.Observe.
	Items []Item
	// Standing are already-rendered lines that print on every poll they stand
	// and are not changes: a bus line this tool has seen before. Shown every
	// time, woken on once.
	Standing []string
	// Notes are WAKE NOTE lines this poll earned, printed once per run.
	Notes []string
}

// Item is one observation.
type Item struct {
	Kind  string // bus, busline, entry, report, line
	Key   string
	Value string
	ID    string // the delivery id; empty means derive it from the key and value
}

// DeliveryID is the id of a value the window was shown: a note's own id for a
// bus note, and for every other key the first twelve hex characters of
// SHA-256 over <key>\x00<value>.
func DeliveryID(key, value string) string {
	sum := sha256.Sum256([]byte(key + "\x00" + value))
	return hex.EncodeToString(sum[:])[:12]
}

// ID answers an item's delivery id, deriving it where the source did not name
// one.
func (i Item) DeliveryID() string {
	if i.ID != "" {
		return i.ID
	}
	return DeliveryID(i.Key, i.Value)
}

// Kinds are the cap's kinds. The cap is PER KIND because a flat cap over a
// concatenated stream means the loud kind eats the quiet one, and the quiet one
// is the finding the window did not already know about: forty churning entries
// must not hide one note.
const (
	KindBus     = "bus"
	KindBusLine = "busline"
	KindEntry   = "entry"
	KindReport  = "report"
	KindLine    = "line"
)

// CapKind maps an item kind to the kind named on its WAKE MORE line. A relayed
// bus line and a relayed note are both bus lines to the cap, because they are
// both what the bus said.
func CapKind(kind string) string {
	switch kind {
	case KindBus, KindBusLine:
		return "bus"
	case KindEntry:
		return "entry"
	case KindReport:
		return "report"
	default:
		return kind
	}
}

// Render turns a stored (key, value) pair back into the line the window reads.
// It is a pure function of the state, which is what makes rule 11 possible: a
// record the cap elided is printed by the NEXT call, out of the queue, with no
// second poll of anything.
//
// now is here for the one field that is a duration rather than a value --- a
// line's silence --- and for nothing else. Every other field comes out of the
// value that was observed.
func Render(kind, key, value string, now time.Time) string {
	switch kind {
	case KindBus:
		p := fields(Decompose(value), 6)
		return fmt.Sprintf("WAKE BUS id=%s from=%s addr=%s at=%s commit=%s path=%s: %s",
			oneline.Field(strings.TrimPrefix(key, "bus:note:")),
			oneline.Field(dash(p[0])), oneline.Field(dash(p[1])), oneline.Field(dash(p[2])),
			oneline.Field(dash(p[3])), oneline.Field(dash(p[4])),
			oneline.Escape(oneline.Cap(p[5], oneline.TailBytes)))
	case KindBusLine:
		return "WAKE BUS LINE " + oneline.Escape(oneline.Cap(strings.TrimPrefix(key, "bus:line:"), oneline.TailBytes))
	case KindEntry:
		name := strings.TrimPrefix(key, "entry:")
		p := Decompose(value)
		if len(p) == 2 && p[0] == "unreadable" {
			return fmt.Sprintf("WAKE ENTRY %s unreadable: %s", oneline.Field(name), oneline.Escape(oneline.Cap(p[1], oneline.TailBytes)))
		}
		p = fields(p, 5)
		return fmt.Sprintf("WAKE ENTRY %s state=%s fail=%s pending=%s pass=%s final=%t failing=%s",
			oneline.Field(name), oneline.Field(dash(p[0])), oneline.Field(dash(p[1])),
			oneline.Field(dash(p[2])), oneline.Field(dash(p[3])), IsFinal(value),
			oneline.Field(dash(p[4])))
	case KindReport:
		p := fields(Decompose(value), 4)
		return fmt.Sprintf("WAKE REPORT path=%s lines=%s bytes=%s %s",
			oneline.Field(strings.TrimPrefix(key, "report:")),
			oneline.Field(dash(p[2])), oneline.Field(dash(p[1])), oneline.Field(dash(p[3])))
	case KindLine:
		p := fields(Decompose(value), 3)
		silent := "-"
		if stamp, err := time.Parse(time.RFC3339, p[2]); err == nil {
			silent = Dur(now.Sub(stamp))
		}
		return fmt.Sprintf("WAKE LINE name=%s state=%s last=%s silent=%s commit=%s",
			oneline.Field(strings.TrimPrefix(key, "line:")), oneline.Field(dash(p[1])),
			oneline.Field(dash(p[2])), oneline.Field(silent), oneline.Field(dash(p[0])))
	}
	return "WAKE NOTE an item of an unknown kind was stored: " + oneline.Field(kind)
}

// fields pads a decomposed value to n parts, so a value written by an older
// build renders as a line with dashes rather than panicking a watcher.
func fields(p []string, n int) []string {
	for len(p) < n {
		p = append(p, "")
	}
	return p
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Dur is the one duration spelling this tool prints: Go's own, which is
// unambiguous and parses back with time.ParseDuration.
func Dur(d time.Duration) string { return d.Round(time.Second).String() }

// escapeTail is the one rendering a relayed line gets: capped at the repo's
// tail budget with the ...+<dropped>B mark that a reader can tell from an
// author's own ellipsis, and then escaped, so a note whose subject carries
// U+2028 arrives as one line rather than two. The prototype's cut160 cut with
// no mark at all.
func escapeTail(s string) string {
	return oneline.Escape(oneline.Cap(s, oneline.TailBytes))
}
