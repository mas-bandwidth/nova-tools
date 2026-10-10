package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSprintReleasecheckAcceptanceCoverNilSnapshot tests that a nil snapshot returns an empty Acceptance.
func TestSprintReleasecheckAcceptanceCoverNilSnapshot(t *testing.T) {
	t.Parallel()
	a := AcceptanceOf(nil, nil, "")
	assert.Empty(t, a.Streams)
	assert.Empty(t, a.Cards)
	assert.Empty(t, a.Reads)
	assert.Empty(t, a.Judgments)
	assert.Empty(t, a.Dropped)
	assert.False(t, a.Promote.Queued)
}

// newSnapshotForTest creates a minimal snapshot for testing AcceptanceOf.
func newSnapshotForTest() *Snapshot {
	return &Snapshot{
		Work:  &Table{cards: map[string]*Card{}, props: map[string]string{}},
		Fleet: &Table{cards: map[string]*Card{}, props: map[string]string{}},
	}
}

// TestSprintReleasecheckAcceptanceCoverGlob tests stream filtering by glob.
func TestSprintReleasecheckAcceptanceCoverGlob(t *testing.T) {
	t.Parallel()

	t.Run("glob s1 keeps only s1", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		s.Work.rows = []string{"s1", "s2"}
		s.Work.cards = map[string]*Card{
			"s1": {ID: "s1", Row: "s1", Col: Landed, Fields: map[string]string{"head": "h1", "tier_now": "flash"}},
			"s2": {ID: "s2", Row: "s2", Col: Landed, Fields: map[string]string{"head": "h2", "tier_now": "flash"}},
		}
		a := AcceptanceOf(s, nil, "s1")
		assert.Equal(t, []string{"s1"}, a.Streams)
		assert.Len(t, a.Cards, 1)
		assert.Equal(t, "s1", a.Cards[0].Stream)
	})

	t.Run("empty glob keeps both", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		s.Work.rows = []string{"s1", "s2"}
		s.Work.cards = map[string]*Card{
			"s1": {ID: "s1", Row: "s1", Col: Landed, Fields: map[string]string{"head": "h1", "tier_now": "flash"}},
			"s2": {ID: "s2", Row: "s2", Col: Landed, Fields: map[string]string{"head": "h2", "tier_now": "flash"}},
		}
		a := AcceptanceOf(s, nil, "")
		assert.Equal(t, []string{"s1", "s2"}, a.Streams)
		assert.Len(t, a.Cards, 2)
	})
}

// TestSprintReleasecheckAcceptanceCoverCard tests landed card fields.
func TestSprintReleasecheckAcceptanceCoverCard(t *testing.T) {
	t.Parallel()
	s := newSnapshotForTest()
	s.Work.rows = []string{"s1"}
	s.Work.cards = map[string]*Card{
		"c1": {ID: "c1", Row: "s1", Col: Landed, Fields: map[string]string{"head": "h1", "tier_now": "flash"}},
	}
	a := AcceptanceOf(s, nil, "")
	require.Len(t, a.Cards, 1)
	assert.Equal(t, "c1", a.Cards[0].ID)
	assert.Equal(t, "s1", a.Cards[0].Stream)
	assert.Equal(t, Landed, a.Cards[0].Col)
	assert.Equal(t, "flash", a.Cards[0].Tier)
	assert.Equal(t, "h1", a.Cards[0].Head)
}

// TestSprintReleasecheckAcceptanceCoverReader tests reader record stream inheritance.
func TestSprintReleasecheckAcceptanceCoverReader(t *testing.T) {
	t.Parallel()

	t.Run("reader with no stream takes primary's stream", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		s.Work.rows = []string{"s1"}
		s.Work.cards = map[string]*Card{
			"s1": {ID: "p1", Row: "s1", Col: Landed, Fields: map[string]string{"head": "h1", "tier_now": "flash"}},
		}
		s.Readers = &Table{cards: map[string]*Card{
			"r1": {ID: "r1", Fields: map[string]string{"primary": "p1", "head": "h1", "reader": "rd", "verdict": "ok"}},
		}}
		a := AcceptanceOf(s, nil, "")
		require.Len(t, a.Reads, 1)
		assert.Equal(t, "p1", a.Reads[0].Primary)
		assert.Equal(t, "s1", a.Reads[0].Stream)
		assert.Equal(t, "h1", a.Reads[0].Head)
		assert.Equal(t, "rd", a.Reads[0].Reader)
		assert.Equal(t, "ok", a.Reads[0].Verdict)
	})

	t.Run("reader with explicit stream uses it", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		s.Work.rows = []string{"s1"}
		s.Work.cards = map[string]*Card{
			"s1": {ID: "p1", Row: "s1", Col: Landed, Fields: map[string]string{"head": "h1", "tier_now": "flash"}},
		}
		s.Readers = &Table{cards: map[string]*Card{
			"r1": {ID: "r1", Fields: map[string]string{"primary": "p1", "stream": "s1", "head": "h1", "reader": "rd", "verdict": "ok"}},
		}}
		a := AcceptanceOf(s, nil, "")
		require.Len(t, a.Reads, 1)
		assert.Equal(t, "s1", a.Reads[0].Stream)
	})
}

// TestSprintReleasecheckAcceptanceCoverJudgment tests open judgment stream resolution.
func TestSprintReleasecheckAcceptanceCoverJudgment(t *testing.T) {
	t.Parallel()

	t.Run("judgment by note.Stream", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		s.Work.rows = []string{"s1"}
		s.Work.cards = map[string]*Card{
			"s1": {ID: "p1", Row: "s1", Col: Landed, Fields: map[string]string{"tier_now": "flash"}},
		}
		s.Open = []Open{
			{Key: OpenKey("n1", "p1"), Note: Note{ID: "n1", Stream: "s1", Type: NWorkFailed}},
		}
		a := AcceptanceOf(s, nil, "")
		require.Len(t, a.Judgments, 1)
		assert.Equal(t, "s1", a.Judgments[0].Stream)
	})

	t.Run("judgment by stream: subject", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		s.Work.rows = []string{"s1"}
		s.Work.cards = map[string]*Card{
			"s1": {ID: "p1", Row: "s1", Col: Landed, Fields: map[string]string{"tier_now": "flash"}},
		}
		s.Open = []Open{
			{Key: OpenKey("n1", StreamSubject("s1")), Note: Note{ID: "n1", Type: NWorkFailed}},
		}
		a := AcceptanceOf(s, nil, "")
		require.Len(t, a.Judgments, 1)
		assert.Equal(t, "s1", a.Judgments[0].Stream)
	})

	t.Run("judgment by first primary's stream", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		s.Work.rows = []string{"s1"}
		s.Work.cards = map[string]*Card{
			"s1": {ID: "p1", Row: "s1", Col: Landed, Fields: map[string]string{"tier_now": "flash"}},
		}
		s.Open = []Open{
			{Key: OpenKey("n1", "p1"), Note: Note{ID: "n1", Primaries: []string{"p1"}, Type: NWorkFailed}},
		}
		a := AcceptanceOf(s, nil, "")
		require.Len(t, a.Judgments, 1)
		assert.Equal(t, "s1", a.Judgments[0].Stream)
	})

	t.Run("judgment resolving to no stream is skipped", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		s.Work.rows = []string{"s1"}
		s.Work.cards = map[string]*Card{
			"s1": {ID: "p1", Row: "s1", Col: Landed, Fields: map[string]string{"tier_now": "flash"}},
		}
		s.Open = []Open{
			{Key: OpenKey("n1", "unknown"), Note: Note{ID: "n1", Primaries: []string{"unknown"}, Type: NWorkFailed}},
		}
		a := AcceptanceOf(s, nil, "")
		assert.Empty(t, a.Judgments)
	})
}

// TestSprintReleasecheckAcceptanceCoverDrop tests removed work line handling.
func TestSprintReleasecheckAcceptanceCoverDrop(t *testing.T) {
	t.Parallel()

	t.Run("removed work line with Cause", func(t *testing.T) {
		t.Parallel()
		lines := []Line{
			{Kind: LineMove, Table: Work, Removed: true, Card: "c1", Stream: "s1", Cause: "dropped"},
		}
		s := newSnapshotForTest()
		a := AcceptanceOf(s, lines, "")
		require.Len(t, a.Dropped, 1)
		assert.Equal(t, "c1", a.Dropped[0].ID)
		assert.Equal(t, "dropped", a.Dropped[0].Reason)
	})

	t.Run("removed work line with Text[reason]", func(t *testing.T) {
		t.Parallel()
		lines := []Line{
			{Kind: LineMove, Table: Work, Removed: true, Card: "c1", Stream: "s1", Text: map[string]string{"reason": "text reason"}},
		}
		s := newSnapshotForTest()
		a := AcceptanceOf(s, lines, "")
		require.Len(t, a.Dropped, 1)
		assert.Equal(t, "text reason", a.Dropped[0].Reason)
	})

	t.Run("removed line of another table is skipped", func(t *testing.T) {
		t.Parallel()
		lines := []Line{
			{Kind: LineMove, Table: Fleet, Removed: true, Card: "c1", Cause: "dropped"},
		}
		s := newSnapshotForTest()
		a := AcceptanceOf(s, lines, "")
		assert.Empty(t, a.Dropped)
	})

	t.Run("removed line with no Card is skipped", func(t *testing.T) {
		t.Parallel()
		lines := []Line{
			{Kind: LineMove, Table: Work, Removed: true, Stream: "s1", Cause: "dropped"},
		}
		s := newSnapshotForTest()
		a := AcceptanceOf(s, lines, "")
		assert.Empty(t, a.Dropped)
	})
}

// TestSprintReleasecheckAcceptanceCoverPromotion tests promotion fields.
func TestSprintReleasecheckAcceptanceCoverPromotion(t *testing.T) {
	t.Parallel()

	t.Run("no PropPromotedSha gives Queued false", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		a := AcceptanceOf(s, nil, "")
		assert.Equal(t, "", a.Promote.Sha)
		assert.False(t, a.Promote.Queued)
	})

	t.Run("sha with no landing gives AfterLastLanding true", func(t *testing.T) {
		t.Parallel()
		s := newSnapshotForTest()
		s.Work.props = map[string]string{PropPromotedSha: "sha123"}
		a := AcceptanceOf(s, []Line{}, "")
		assert.Equal(t, "sha123", a.Promote.Sha)
		assert.True(t, a.Promote.AfterLastLanding)
	})

	t.Run("PropPromotedAt after last landing gives true", func(t *testing.T) {
		t.Parallel()
		landingTime := t0.Add(2 * time.Hour)
		atTime := t0.Add(3 * time.Hour)
		lines := []Line{
			{Kind: LineMove, Table: Work, Card: "c1", Stream: "s1", To: "s1:landed", At: landingTime},
		}
		s := newSnapshotForTest()
		s.Work.props = map[string]string{
			PropPromotedSha: "sha123",
			PropPromotedAt:  atTime.Format(time.RFC3339),
		}
		a := AcceptanceOf(s, lines, "")
		assert.True(t, a.Promote.AfterLastLanding)
	})

	t.Run("PropPromotedAt before last landing gives false", func(t *testing.T) {
		t.Parallel()
		landingTime := t0.Add(3 * time.Hour)
		atTime := t0.Add(2 * time.Hour)
		lines := []Line{
			{Kind: LineMove, Table: Work, Card: "c1", Stream: "s1", To: "s1:landed", At: landingTime},
		}
		s := newSnapshotForTest()
		s.Work.props = map[string]string{
			PropPromotedSha: "sha123",
			PropPromotedAt:  atTime.Format(time.RFC3339),
		}
		a := AcceptanceOf(s, lines, "")
		assert.False(t, a.Promote.AfterLastLanding)
	})

	t.Run("unparsable PropPromotedAt leaves AfterLastLanding false", func(t *testing.T) {
		t.Parallel()
		landingTime := t0.Add(2 * time.Hour)
		lines := []Line{
			{Kind: LineMove, Table: Work, Card: "c1", Stream: "s1", To: "s1:landed", At: landingTime},
		}
		s := newSnapshotForTest()
		s.Work.props = map[string]string{
			PropPromotedSha: "sha123",
			PropPromotedAt:  "invalid-date",
		}
		a := AcceptanceOf(s, lines, "")
		assert.False(t, a.Promote.AfterLastLanding)
	})
}

// TestSprintReleasecheckAcceptanceCoverEndToEnd tests end-to-end integration with CardsSettled.
func TestSprintReleasecheckAcceptanceCoverEndToEnd(t *testing.T) {
	t.Parallel()

	// Build a fake release that feeds AcceptanceOf result to CardsSettled
	landedTime := t0.Add(1 * time.Hour)
	lines := []Line{
		{Kind: LineMove, Table: Work, Card: "s1-1.w1", Stream: "s1", To: "s1:landed", At: landedTime},
	}
	s := newSnapshotForTest()
	s.Work.rows = []string{"s1"}
	s.Work.cards = map[string]*Card{
		"s1": {ID: "s1-1.w1", Row: "s1", Col: Ready, Fields: map[string]string{"head": "h1", "tier_now": "flash"}},
	}
	a := AcceptanceOf(s, lines, "")
	f := fakeRelease{
		now:      t0.Add(2 * time.Hour),
		accept:   a,
		dealtMax: DealtMaxDefault,
	}
	r := CardsSettled(f)
	assert.False(t, r.OK)
	assert.Contains(t, r.Evidence, "s1-1.w1")
	assert.Contains(t, r.Evidence, "ready")
}
