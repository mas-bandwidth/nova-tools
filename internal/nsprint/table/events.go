package table

// events.go: the table's one EVENTS line (nova-tools #4319 item 4). The
// progress duty (internal/nsprint/reconcile/progress.go) records its asks
// and each pass's duty refusals on proc:progress; the table reads that hash
// in the same pipeline as everything else and prints one line from it only
// when there is something to show (less is more).

import (
	"fmt"
	"strconv"
	"strings"
)

// ProgressKey is proc:progress, the progress duty's record
// (reconcile.ProgressKey; the table does not import reconcile).
const ProgressKey = "proc:progress"

// ProgressEvents is proc:progress as read.
type ProgressEvents struct {
	Events  int64  // asks the system made (pit stop set with a diagnosis)
	Refused int64  // the last pass's duty refusals
	Last    string // the last EVENT line
	// Unread is the read error when proc:progress did not come back: the
	// line then says so rather than showing a quiet none.
	Unread string
}

// ParseProgressEvents reads an HGETALL of proc:progress; a missing or
// non-numeric count is 0.
func ParseProgressEvents(h map[string]string) ProgressEvents {
	n := func(f string) int64 { v, _ := strconv.ParseInt(strings.TrimSpace(h[f]), 10, 64); return v }
	return ProgressEvents{Events: n("events"), Refused: n("refused"), Last: strings.Join(strings.Fields(h["event"]), " ")}
}

// Line is `EVENTS n=<asks> refused=<n> last=<line>` when either count is
// non-zero, `EVENTS not read: <err>` when the record did not come back,
// else "" (no line).
func (e ProgressEvents) Line() string {
	if e.Unread != "" {
		return "EVENTS not read: " + strings.Join(strings.Fields(e.Unread), " ")
	}
	if e.Events == 0 && e.Refused == 0 {
		return ""
	}
	line := fmt.Sprintf("EVENTS n=%d refused=%d", e.Events, e.Refused)
	if e.Last != "" {
		line += " last=" + e.Last
	}
	return line
}
