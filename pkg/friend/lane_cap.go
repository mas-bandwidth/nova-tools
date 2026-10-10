package friend

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A lane's wall time is capped by its card's tier (a-lane-is-capped-by-its-tier.w1). On
// 2026-10-05 night seven one-shot lanes ran 24 to 73 minutes each and produced nothing, and
// several lanes ran past an hour on cards whose fix was one line: a lane ran until its model
// stopped, and wall time is the fleet's budget. Each card a lane takes is capped by its
// tier's wall (DefaultLaneCaps, or the row's lane_caps): when the card's wall since the lane
// began it reaches the cap, the daemon ends the turn (its process group signalled, as a
// silent stop is) and the card's end is a HOLD naming `capped at <cap> (tier <t>, overrun
// <d>)` with the last CapTailLines lines of the lane's output (EndReport). The sprint reads
// those words off the failed finish and deals the card once more, at the next tier up,
// before the cap counts as a failure (internal/sprint lane_cap.go). The model is
// pkg/friend/tla/LaneEnd.tla: Capped ends the run with a lane report, and CapRedeal
// re-deals once (RedealOnce, and its reversed witness MCLaneEndBrokenCapAlways.cfg).

// DefaultLaneCaps is the wall cap of a lane's card by its tier, where the row names none.
var DefaultLaneCaps = map[string]time.Duration{
	"flash":    15 * time.Minute,
	"pro":      45 * time.Minute,
	"heavy":    90 * time.Minute,
	"frontier": 150 * time.Minute,
}

// CapTailLines is how many of the lane's last output lines a capped card's report quotes.
const CapTailLines = 40

// TailKept bounds the output a lane's turn keeps for its tail: its last TailKept bytes.
const TailKept = 64 << 10

// LaneCap is the wall cap of a card of tier: the row's (caps) when it names one above zero,
// else DefaultLaneCaps'; a tier neither names (none was read for the card) is capped at the
// longest default, frontier's, so a card whose tier is unknown is never cut shorter than it could be owed.
func LaneCap(tier string, caps map[string]time.Duration) time.Duration {
	if d := caps[tier]; d > 0 {
		return d
	}
	if d, ok := DefaultLaneCaps[tier]; ok {
		return d
	}
	return DefaultLaneCaps["frontier"]
}

// ParseLaneCaps reads the row's lane caps off her beat's answer
// (row_lane_caps=flash:15m,pro:45m,heavy:1h30m,frontier:2h30m, beside row_mode and
// row_width); ok is false when the answer carries none, or one with a pair that is no
// tier:duration above zero.
func ParseLaneCaps(answer string) (caps map[string]time.Duration, ok bool) {
	for _, w := range strings.Fields(answer) {
		v, found := strings.CutPrefix(w, "row_lane_caps=")
		if !found {
			continue
		}
		caps = map[string]time.Duration{}
		for _, pair := range strings.Split(v, ",") {
			tier, dur, cut := strings.Cut(pair, ":")
			d, err := time.ParseDuration(dur)
			if !cut || tier == "" || err != nil || d <= 0 {
				return nil, false
			}
			caps[tier] = d
		}
		return caps, true
	}
	return nil, false
}

// CappedWords is how a capped card's end names the cap, the words the sprint reads off its
// failed finish (internal/sprint ParseLaneCap): `capped at <cap> (tier <tier>, overrun <d>)`,
// overrun the card's wall past the cap when the lane ended it.
func CappedWords(limit time.Duration, tier string, overrun time.Duration) string {
	return fmt.Sprintf("capped at %s (tier %s, overrun %s)", limit, dash(tier), max(overrun, 0).Round(time.Second))
}

// outputTail is the last TailKept bytes a lane's turn printed, written by the turn's
// command and read by the loop when the turn ends.
type outputTail struct {
	mu sync.Mutex
	b  []byte
}

func (o *outputTail) add(p []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.b = append(o.b, p...)
	if len(o.b) > TailKept {
		o.b = append([]byte(nil), o.b[len(o.b)-TailKept:]...)
	}
}

func (o *outputTail) String() string {
	if o == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.b)
}

// LastLines is the last n lines of s, without a trailing newline.
func LastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// cardTier is the tier of card c as her row last said it (Held: the packet's tier of the
// card whose job is c's), "" when the row has not said it.
func (d *Daemon) cardTier(c Card) string {
	job := filepath.Base(c.Outbox)
	for _, h := range d.heldCards {
		if h.Job == job {
			return h.Tier
		}
	}
	return ""
}

// laneCap is the cap of a card of tier for this daemon: the row's caps (LaneCaps) over the
// defaults.
func (d *Daemon) laneCap(tier string) time.Duration {
	var caps map[string]time.Duration
	if d.LaneCaps != nil {
		caps = d.LaneCaps()
	}
	return LaneCap(tier, caps)
}

// capWatch is the wall watch on the lanes' running turns: a card whose wall since its lane
// began it has reached its cap is ended, the turn's process group signalled, said on the
// record; laneDone ends the card capped.
func (l *loop) capWatch(now time.Time) {
	s := l.lanes
	for _, ln := range s.lanes {
		t := ln.t
		if t == nil || !t.running || ln.card == nil || ln.cap <= 0 || t.capped || t.stopped {
			continue
		}
		began := s.state.Started[filepath.Base(ln.card.Outbox)].At
		if began.IsZero() || now.Sub(began) < ln.cap {
			continue
		}
		t.capped = true
		t.cancel()
		l.d.Record(fmt.Sprintf("%s lane %d: card %s capped at %s (tier %s): its wall since %s reached the cap; its process group is signalled",
			now.UTC().Format(time.RFC3339), ln.n, ln.card.ID, ln.cap, dash(ln.tier), began.UTC().Format(time.RFC3339)))
	}
}
