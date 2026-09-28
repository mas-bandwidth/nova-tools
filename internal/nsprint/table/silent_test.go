package table_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// TestStreamCellThatDidNotComeBackPrintsAQuestionMark: a stream ZCARD the
// tick could not read rendered as 0, fed the headline's left, pct and eta, and said
// nothing. It prints "?" in its cell and in the total, like a consumer cell
// (never a false 0), and a tick's partial failures print as ERR lines.
func TestStreamCellThatDidNotComeBackPrintsAQuestionMark(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 15, 30, 0, 0, time.UTC)
	snap := &table.SprintSnapshot{
		LastGood: now,
		Counts: ws.Sum([]ws.StreamCounts{
			{Stream: "swarm: cards", Cells: [6]int64{3, 1, 2, 0, 0, 0}, Unread: [6]bool{false, false, false, true, false, false}},
			{Stream: "nova-sprint", Cells: [6]int64{0, 0, 0, 0, 0, 4}},
		}),
		Errors: []string{"ws:log not read (eta rate unknown): NOPERM"},
	}
	got := snap.Render(now)
	for _, want := range []string{
		"swarm: cards              |       3 |     1 |       2 |      ? |       0 |      0\n",
		"total                     |       3 |     1 |       2 |      ? |       0 |      4\n",
		"ERR ws:log not read (eta rate unknown): NOPERM\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render lacks %q:\n%s", want, got)
		}
	}
	if !snap.Counts.Streams[0].AnyUnread() || snap.Counts.Streams[1].AnyUnread() {
		t.Fatalf("AnyUnread: %v %v", snap.Counts.Streams[0].AnyUnread(), snap.Counts.Streams[1].AnyUnread())
	}
	// A cell not read makes the headline "?" too, never a number built on 0.
	if !strings.Contains(got, "?/? done ?%, left ?, eta ? gh 0/h\n") {
		t.Errorf("headline is not all ?:\n%s", got)
	}
}

// TestAnAllUnreadStreamStillShows: a stream whose every cell is unread has
// total 0, which used to hide the row; it shows, all "?".
func TestAnAllUnreadStreamStillShows(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 15, 30, 0, 0, time.UTC)
	snap := &table.SprintSnapshot{LastGood: now, Counts: ws.Sum([]ws.StreamCounts{
		{Stream: "quiet", Unread: [6]bool{true, true, true, true, true, true}},
	})}
	got := snap.Render(now)
	if !strings.Contains(got, "quiet                     |       ? |     ? |       ? |      ? |       ? |      ?\n") {
		t.Fatalf("an all-unread stream is hidden:\n%s", got)
	}
}
