package sprint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A frontier card's read is asked of a friend whose tiers include frontier
// (docs/SPEC-SPRINT.md, a friend's card; friend_read.go). The brief lands in
// her inbox. LAND closes the read ok. HOLD with a finding that names a file
// closes it broken. No friend up leaves the read waiting. The clock is the
// snapshot's, never the wall.
func TestAFrontierCardsReadIsAskedAsAFriendCard(t *testing.T) {
	t.Parallel()
	const brief = "tier: frontier\n\nAS A READ\nLook at main.go.\n"
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seat := func(status string) FriendSeat {
		return FriendSeat{Name: "amy", Width: 2, Status: status, Tiers: []string{"frontier"}}
	}
	ready := func(t *testing.T) (*world, string) {
		t.Helper()
		w := newWorld(t, "reader-a", "reader-b")
		w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: brief}))
		pr := w.s.Work.Card("s1-1")
		pr.Col = Review
		pr.Fields["attempt"] = "1"
		pr.Fields["head"] = head
		pr.Fields["branch"] = "sprint/example"
		return w, t.TempDir()
	}

	t.Run("brief", func(t *testing.T) {
		t.Parallel()
		w, dir := ready(t)
		p, err := FriendReadAsk(w.s, []FriendSeat{seat(Up)}, dir)
		require.NoError(t, err)
		w.must(p)
		job := ReadCardID("s1-1", 1, "amy")
		b, err := os.ReadFile(filepath.Join(dir, "inbox", job, "BRIEF.md"))
		require.NoError(t, err)
		text := string(b)
		assert.Contains(t, text, "WHO: friend amy")
		assert.Contains(t, text, "deadline: 2030-01-02T05:04:05Z")
		assert.Contains(t, text, "branch: sprint/example")
		assert.Contains(t, text, "start: "+head)
		assert.Contains(t, text, "Look at main.go.")
		assert.Equal(t, []string{"reader-a", "reader-b"}, w.s.Readers.Rows())
		rc := w.s.Fleet.Card(job)
		require.NotNil(t, rc)
		assert.Equal(t, Working, rc.Col)
		assert.Equal(t, FriendRow("amy"), rc.Row)
	})

	t.Run("land", func(t *testing.T) {
		t.Parallel()
		w, dir := ready(t)
		p, err := FriendReadAsk(w.s, []FriendSeat{seat(Up)}, dir)
		require.NoError(t, err)
		w.must(p)
		w.must(FriendReadClose(w.s, "amy", "s1-1", "Verdict: LAND\n\nThe read is clean.\n"))
		rc := w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy"))
		require.NotNil(t, rc)
		assert.Equal(t, OK, rc.Col)
		assert.Equal(t, Review, w.s.Work.Card("s1-1").Col)
	})

	t.Run("hold", func(t *testing.T) {
		t.Parallel()
		w, dir := ready(t)
		p, err := FriendReadAsk(w.s, []FriendSeat{seat(Up)}, dir)
		require.NoError(t, err)
		w.must(p)
		w.must(FriendReadClose(w.s, "amy", "s1-1", "Verdict: HOLD\n\nmain.go: the empty branch\n"))
		rc := w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy"))
		require.NotNil(t, rc)
		assert.Equal(t, Broken, rc.Col)
		assert.Contains(t, rc.F("finding"), "main.go")
	})

	t.Run("none up", func(t *testing.T) {
		t.Parallel()
		w, dir := ready(t)
		p, err := FriendReadAsk(w.s, []FriendSeat{seat("down")}, dir)
		require.NoError(t, err)
		w.must(p)
		_, err = os.Stat(filepath.Join(dir, "inbox"))
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.Equal(t, Review, w.s.Work.Card("s1-1").Col)
		assert.Nil(t, w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy")))
		require.NotEmpty(t, w.notesOf(NFewReaders))
	})
}
