package sprint

import (
	"os"
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/require"
)

func putReview(w *world, id, brief string, attempt int, score float64, head string) {
	w.s.Work.SetRows([]string{"s1"})
	w.s.Work.Put(&Card{ID: id, Row: "s1", Col: Review, Score: score, Rev: 1, Fields: map[string]string{
		"kind": "primary", "attempt": itoa(attempt), "stream": "s1", "brief": brief, "head": head}})
}

func putAttemptWork(w *world, primary string, attempt int, branch, head, ok string) {
	fields := map[string]string{"kind": "work", "primary": primary, "stream": "s1", "attempt": itoa(attempt), "branch": branch, "head": head}
	if ok != "" {
		fields["ok"] = ok
	}
	w.s.Fleet.Put(&Card{ID: WorkCardID(primary, attempt), Rev: 1, Fields: fields})
}

func placedReaderRead(w *world, primary string) *Card {
	for _, c := range w.s.Readers.Cards() {
		if c.Placed() && c.F("kind") == "read" && c.F("primary") == primary {
			return c
		}
	}
	return nil
}

func frontierSeat(name string, width int, status, dir string) FriendSeat {
	return FriendSeat{Name: name, Width: width, Status: status, Tiers: []string{cardhdr.RouteFrontier}, Dir: dir}
}

// askReaders deals the read cards the primaries in review want (readCardsAsk) to the seats,
// each a reader, and to the members that read, then runs the tick's ask part (the waiting
// marks).
func askReaders(t *testing.T, w *world, seats []FriendSeat) {
	t.Helper()
	readers := slices.Clone(seats)
	for i := range readers {
		if len(readers[i].Roles) == 0 {
			readers[i].Roles = []string{RoleReader}
		}
	}
	if readers == nil {
		readers = []FriendSeat{}
	}
	p, _ := readCardsAsk(w.s, readers, nil)
	w.do(p)
	w.part(readCardsAskPart, TickReq{Friends: readers})
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}
