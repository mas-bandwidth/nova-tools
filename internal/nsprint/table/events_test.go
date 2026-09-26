package table_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// The table's one EVENTS line (nova-tools #4319 item 4): proc:progress as the
// progress duty wrote it prints as one line only when an ask or a refusal is
// there to show; a clean record prints nothing, not an empty EVENTS line.
func TestEventsLineOnlyWhenNonZero(t *testing.T) {
	t.Parallel()
	if got := table.ParseProgressEvents(nil).Line(); got != "" {
		t.Fatalf("empty record: %q", got)
	}
	if got := table.ParseProgressEvents(map[string]string{"events": "0", "refused": "0", "event": "stale"}).Line(); got != "" {
		t.Fatalf("zero counts: %q", got)
	}
	ev := table.ParseProgressEvents(map[string]string{"events": "1", "refused": "0",
		"event": "EVENT PROGRESS STALLED cards stall blocked=30m0s window=30m0s left=3 ready=3 landed_h=0 retries_h=0 sprint=s1 at=1"})
	want := "EVENTS n=1 refused=0 last=EVENT PROGRESS STALLED cards stall blocked=30m0s window=30m0s left=3 ready=3 landed_h=0 retries_h=0 sprint=s1 at=1"
	if ev.Line() != want {
		t.Fatalf("line %q\nwant %q", ev.Line(), want)
	}
	if got := table.ParseProgressEvents(map[string]string{"refused": "2"}).Line(); got != "EVENTS n=0 refused=2" {
		t.Fatalf("refusals only: %q", got)
	}
	// A record that did not come back is said, never a quiet none.
	if got := (table.ProgressEvents{Unread: "WRONGTYPE  proc:progress\n"}).Line(); got != "EVENTS not read: WRONGTYPE proc:progress" {
		t.Fatalf("unread: %q", got)
	}
	// Rendered: the line stands under the tables, once; a clean snapshot
	// renders no EVENTS at all.
	snap := &table.SprintSnapshot{Events: ev}
	out := snap.Render(time.Unix(0, 0))
	if strings.Count(out, "EVENTS ") != 1 || !strings.Contains(out, want+"\n") {
		t.Fatalf("rendered:\n%s", out)
	}
	if out := (&table.SprintSnapshot{}).Render(time.Unix(0, 0)); strings.Contains(out, "EVENTS") {
		t.Fatalf("clean snapshot rendered EVENTS:\n%s", out)
	}
}
