package table

// land_slow.go: the table's LAND-SLOW / LAND-WALL line (nova-tools #4324, #4387).
// The reconciler's land watch (internal/nsprint/reconcile/land_watch.go) writes
// land:slow:<stream> (word, oldest, oldest_at, age_ms, stalled, at); the table
// reads that hash for each stream in its one pipeline and prints one line
// under the streams table for each slow stream.
// (reconcile.LandSlowKey; the table does not import reconcile, like events.go).

import (
	"fmt"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultLandSlow is the default age threshold for LAND-SLOW (10m).
const DefaultLandSlow = 10 * time.Minute

// DefaultLandWall is the default age threshold for LAND-WALL (30m).
const DefaultLandWall = 30 * time.Minute

// LandSlowKey is a stream's slow record
// (reconcile.LandSlowKey; the table does not import reconcile, like events.go).
func LandSlowKey(stream string) string { return "land:slow:" + stream }

// LandSlow is a stream's slow record (nova-tools #4324, #4387).
type LandSlow struct {
	Stream   string
	Word     string
	Oldest   string
	OldestAt time.Time
	Age      time.Duration
	Max      time.Duration
	Stalled  bool
	At       time.Time
}

// Line renders the LAND-SLOW or LAND-WALL line for a stream under the streams table.
func (s LandSlow) Line() string {
	return fmt.Sprintf("%s %s oldest=%s age=%s max=%s", s.Word, oneline.Field(s.Stream), s.Oldest, s.Age.Truncate(time.Second), s.Max.Truncate(time.Second))
}

// ParseLandSlow reads an HGETALL of land:slow:<stream>.
func ParseLandSlow(stream string, h map[string]string, now time.Time) (LandSlow, bool) {
	word := h["word"]
	if word != "LAND-SLOW" && word != "LAND-WALL" {
		return LandSlow{}, false
	}
	oldest := h["oldest"]
	oldestAtMS, _ := strconv.ParseInt(h["oldest_at"], 10, 64)
	ageMS, _ := strconv.ParseInt(h["age_ms"], 10, 64)
	atMS, _ := strconv.ParseInt(h["at"], 10, 64)
	stalled := h["stalled"] == "1"

	var oldestAt time.Time
	if oldestAtMS > 0 {
		oldestAt = time.UnixMilli(oldestAtMS)
	}
	var at time.Time
	if atMS > 0 {
		at = time.UnixMilli(atMS)
	}

	var age time.Duration
	if !oldestAt.IsZero() && !now.IsZero() && now.After(oldestAt) {
		age = now.Sub(oldestAt)
	} else if ageMS > 0 {
		age = time.Duration(ageMS) * time.Millisecond
	}

	maxDuration := DefaultLandSlow
	if word == "LAND-WALL" {
		maxDuration = DefaultLandWall
	}
	if maxStr := h["max_ms"]; maxStr != "" {
		if m, err := strconv.ParseInt(maxStr, 10, 64); err == nil && m > 0 {
			maxDuration = time.Duration(m) * time.Millisecond
		}
	} else if maxStr := h["max"]; maxStr != "" {
		if d, err := time.ParseDuration(maxStr); err == nil && d > 0 {
			maxDuration = d
		} else if m, err := strconv.ParseInt(maxStr, 10, 64); err == nil && m > 0 {
			maxDuration = time.Duration(m) * time.Second
		}
	}

	return LandSlow{
		Stream:   stream,
		Word:     word,
		Oldest:   oldest,
		OldestAt: oldestAt,
		Age:      age,
		Max:      maxDuration,
		Stalled:  stalled,
		At:       at,
	}, true
}
