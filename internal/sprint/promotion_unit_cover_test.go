package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSprintPromotionCoverPromotedShaValid(t *testing.T) {
	t.Parallel()
	sha, err := PromotedSha(" ABCDEF1 ")
	require.Empty(t, err)
	assert.Equal(t, "abcdef1", sha)
}

func TestSprintPromotionCoverPromotedShaSevenDigits(t *testing.T) {
	t.Parallel()
	sha, err := PromotedSha("1234567")
	require.Empty(t, err)
	assert.Equal(t, "1234567", sha)
}

func TestSprintPromotionCoverPromotedShaFortyDigits(t *testing.T) {
	t.Parallel()
	sha, err := PromotedSha("1234567890abcdef1234567890abcdef12345678")
	require.Empty(t, err)
	assert.Equal(t, "1234567890abcdef1234567890abcdef12345678", sha)
}

func TestSprintPromotionCoverPromotedShaSixDigits(t *testing.T) {
	t.Parallel()
	sha, err := PromotedSha("123456")
	assert.NotEmpty(t, err)
	assert.Empty(t, sha)
}

func TestSprintPromotionCoverPromotedShaFortyOneDigits(t *testing.T) {
	t.Parallel()
	sha, err := PromotedSha("1234567890abcdef1234567890abcdef123456789")
	assert.NotEmpty(t, err)
	assert.Empty(t, sha)
}

func TestSprintPromotionCoverPromotedShaNonHex(t *testing.T) {
	t.Parallel()
	sha, err := PromotedSha("1234567890abcdef1234567890abcdef1234567g")
	assert.NotEmpty(t, err)
	assert.Empty(t, sha)
}

func TestSprintPromotionCoverPromotedShaEmpty(t *testing.T) {
	t.Parallel()
	sha, err := PromotedSha("")
	assert.NotEmpty(t, err)
	assert.Empty(t, sha)
}

func TestSprintPromotionCoverPromotedShaWhitespace(t *testing.T) {
	t.Parallel()
	sha, err := PromotedSha("   ")
	assert.NotEmpty(t, err)
	assert.Empty(t, sha)
}

func TestSprintPromotionCoverPromotionNoProp(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: NewTable(Work)}
	_, _, ok := Promotion(s)
	assert.False(t, ok)
}

func TestSprintPromotionCoverPromotionUnparsable(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: NewTable(Work)}
	s.Work.props = map[string]string{PropPromotedAt: "not-a-date"}
	_, _, ok := Promotion(s)
	assert.False(t, ok)
}

func TestSprintPromotionCoverPromotionValid(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: NewTable(Work)}
	s.Work.props = map[string]string{
		PropPromotedAt:  t0.Format(time.RFC3339),
		PropPromotedSha: "abcdef123456",
	}
	at, sha, ok := Promotion(s)
	require.True(t, ok)
	assert.Equal(t, t0, at)
	assert.Equal(t, "abcdef123456", sha)
}

func TestSprintPromotionCoverPromotedMainPath(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Coordinator: "coordinator"}
	c1 := &Card{ID: "c1", Row: "work", Col: Landed, Fields: map[string]string{"landed": t0.Add(-2 * time.Hour).Format(time.RFC3339)}}
	c2 := &Card{ID: "c2", Row: "work", Col: Landed, Fields: map[string]string{"landed": t0.Add(-30 * time.Minute).Format(time.RFC3339)}}
	s.Work.Put(c1)
	s.Work.Put(c2)
	s.Open = []Open{{Key: "devbehind", Note: Note{Type: NDevBehind}}}
	p := Promoted(s, PromotedReq{Sha: "ABCDEF1", Who: "coordinator"})
	require.Empty(t, p.Refused)
	require.Len(t, p.Props, 2)
	hasProp := map[string]bool{}
	for _, pw := range p.Props {
		hasProp[pw.Name] = true
		if pw.Name == PropPromotedSha {
			assert.Equal(t, "abcdef1", pw.Value)
		}
	}
	require.True(t, hasProp[PropPromotedAt])
	require.True(t, hasProp[PropPromotedSha])
	require.Len(t, p.Units, 1)
	require.Len(t, p.Units[0].Closes, 1)
	assert.Equal(t, "dev is behind", p.Units[0].Closes[0].Note.Type)
}

func TestSprintPromotionCoverPromotedSecondCall(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Coordinator: "coordinator"}
	s.Work.props = map[string]string{
		PropPromotedAt:  t0.Format(time.RFC3339),
		PropPromotedSha: "abcdef1",
	}
	p := Promoted(s, PromotedReq{Sha: "ABCDEF1", Who: "coordinator"})
	require.Empty(t, p.Refused)
	require.Len(t, p.Props, 2)
	for _, pw := range p.Props {
		if pw.Name == PropPromotedAt || pw.Name == PropPromotedSha {
			assert.False(t, pw.WasAbsent)
		}
	}
}

func TestSprintPromotionCoverPromotedNotCoordinator(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Coordinator: "coordinator"}
	p := Promoted(s, PromotedReq{Sha: "abcdef1", Who: "other"})
	require.NotEmpty(t, p.Refused)
}

func TestSprintPromotionCoverPromotedBadSha(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Coordinator: "coordinator"}
	p := Promoted(s, PromotedReq{Sha: "123", Who: "coordinator"})
	require.NotEmpty(t, p.Refused)
}

func TestSprintPromotionCoverReturnedCard(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Coordinator: "coordinator"}
	c1 := &Card{ID: "c1", Row: "work", Col: Landed, Fields: map[string]string{"landed": t0.Add(-2 * time.Hour).Format(time.RFC3339)}}
	s.Work.Put(c1)
	p := Promoted(s, PromotedReq{Sha: "abcdef1", Who: "coordinator", Returned: []string{"c1"}})
	require.Empty(t, p.Refused)
	require.Len(t, p.Units, 1)
	require.NotEmpty(t, p.Units[0].Changes)
}

func TestSprintPromotionCoverReturnedNotFound(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Coordinator: "coordinator"}
	p := Promoted(s, PromotedReq{Sha: "abcdef1", Who: "coordinator", Returned: []string{"unknown"}})
	require.NotEmpty(t, p.Refused)
}

func TestSprintPromotionCoverReturnedNotLanded(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Coordinator: "coordinator"}
	c1 := &Card{ID: "c1", Row: "work", Col: Ready, Fields: map[string]string{}}
	s.Work.Put(c1)
	p := Promoted(s, PromotedReq{Sha: "abcdef1", Who: "coordinator", Returned: []string{"c1"}})
	require.NotEmpty(t, p.Refused)
}
