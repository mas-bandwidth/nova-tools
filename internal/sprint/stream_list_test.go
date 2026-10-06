package sprint

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func placeBrief(repo, base, task, needs string) map[string]string {
	f := map[string]string{
		"kind":  "primary",
		"brief": "tier: flash\nREPO: " + repo + "\nBASE: " + base + "\n\nTHE TASK. " + task + "\n",
	}
	if needs != "" {
		f["needs"] = needs
	}
	return f
}

func putCard(s *Snapshot, id, stream, col string, fields map[string]string) {
	s.Work.Put(&Card{ID: id, Row: stream, Col: col, Rev: 1, Fields: fields})
}

// TestStreamsListKeepsEveryRepoAndTheReleaseFilter is the listing over a
// snapshot: three streams, two repositories, one stream naming both, a
// release kept only on the stream that records it (docs/SPEC-SPRINT.md
// section 11).
func TestStreamsListKeepsEveryRepoAndTheReleaseFilter(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: NewTable(Work), Merge: NewTable(Merge)}
	s.Work.SetRows([]string{"tools", "seed", "mix"})
	for _, st := range []string{"tools", "seed", "mix"} {
		s.Merge.Put(&Card{ID: CtlID(st), Row: st, Col: Ctl, Rev: 1, Fields: map[string]string{}})
	}
	s.Merge.Card(CtlID("tools")).Fields[FieldRelease] = "v1.2.0"
	putCard(s, "tools-open", "tools", Ready, placeBrief("mas-bandwidth/nova-tools", "sprint/tools", "Open the tools listing. Then stop.", ""))
	putCard(s, "tools-landed", "tools", Landed, placeBrief("mas-bandwidth/nova-tools", "sprint/tools", "Land the tools card. Then stop.", ""))
	putCard(s, "seed-open", "seed", Waiting, placeBrief("mas-bandwidth/nova", "sprint/nova", "Open the seed listing. Then stop.", "tools-open"))
	putCard(s, "mix-tools", "mix", Ready, placeBrief("mas-bandwidth/nova-tools", "sprint/tools", "Mix the tools card. Then stop.", ""))
	putCard(s, "mix-nova", "mix", Waiting, placeBrief("mas-bandwidth/nova", "sprint/nova", "Mix the seed card. Then stop.", "mix-tools"))

	got := StreamsList(s, StreamsQuery{Cards: true})
	require.Len(t, got.Streams, 3)
	assert.Equal(t, "tools", got.Streams[0].Stream)
	assert.Equal(t, []string{"mas-bandwidth/nova-tools"}, got.Streams[0].Repos)
	assert.Equal(t, []string{"sprint/tools"}, got.Streams[0].Bases)
	assert.Equal(t, "v1.2.0", got.Streams[0].Release)
	assert.Equal(t, 1, got.Streams[0].Open)
	assert.Equal(t, 1, got.Streams[0].Landed)
	require.Len(t, got.Streams[0].Cards, 2)
	assert.Equal(t, "tools-open", got.Streams[0].Cards[0].ID)
	assert.Equal(t, Ready, got.Streams[0].Cards[0].State)
	assert.Equal(t, "flash", got.Streams[0].Cards[0].Tier)
	assert.Equal(t, "Open the tools listing.", got.Streams[0].Cards[0].Title)
	assert.Empty(t, got.Streams[0].Cards[0].Needs)
	assert.Equal(t, "tools-landed", got.Streams[0].Cards[1].ID)
	assert.Equal(t, Landed, got.Streams[0].Cards[1].State)

	assert.Equal(t, []string{"mas-bandwidth/nova"}, got.Streams[1].Repos)
	assert.Equal(t, "", got.Streams[1].Release)
	require.Len(t, got.Streams[1].Cards, 1)
	assert.Equal(t, []string{"tools-open"}, got.Streams[1].Cards[0].Needs)
	assert.Equal(t, "Open the seed listing.", got.Streams[1].Cards[0].Title)

	assert.Equal(t, []string{"mas-bandwidth/nova", "mas-bandwidth/nova-tools"}, got.Streams[2].Repos)
	assert.Equal(t, []string{"sprint/nova", "sprint/tools"}, got.Streams[2].Bases)
	assert.Equal(t, []string{
		"stream mix names more than one repository: mas-bandwidth/nova, mas-bandwidth/nova-tools",
		"stream mix names more than one base: sprint/nova, sprint/tools",
	}, got.Findings)

	byRepo := StreamsList(s, StreamsQuery{Repo: "mas-bandwidth/nova-tools"})
	require.Len(t, byRepo.Streams, 2)
	assert.Equal(t, "tools", byRepo.Streams[0].Stream)
	assert.Equal(t, "mix", byRepo.Streams[1].Stream)

	byRel := StreamsList(s, StreamsQuery{Release: "v1.2.0"})
	require.Len(t, byRel.Streams, 1)
	assert.Equal(t, "tools", byRel.Streams[0].Stream)
	assert.Empty(t, byRel.Findings)

	both := StreamsList(s, StreamsQuery{Repo: "mas-bandwidth/nova", Release: "v1.2.0"})
	assert.Empty(t, both.Streams)
}

// TestStreamsListOverThreeThousandCards walks a snapshot of 3,000 cards once.
// The duration is logged. The test does not assert a wall-clock bound.
func TestStreamsListOverThreeThousandCards(t *testing.T) {
	t.Parallel()
	const n = 3000
	s := &Snapshot{Work: NewTable(Work), Merge: NewTable(Merge)}
	s.Work.SetRows([]string{"big"})
	s.Merge.Put(&Card{ID: CtlID("big"), Row: "big", Col: Ctl, Rev: 1, Fields: map[string]string{FieldRelease: "v9"}})
	brief := "tier: flash\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/tools\n\nTHE TASK. Count this card. Done.\n"
	for i := 0; i < n; i++ {
		col := Ready
		if i%10 == 0 {
			col = Landed
		}
		s.Work.Put(&Card{ID: fmt.Sprintf("c-%d", i), Row: "big", Col: col, Score: float64(i), Rev: 1, Fields: map[string]string{"kind": "primary", "brief": brief}})
	}
	start := time.Now()
	got := StreamsList(s, StreamsQuery{Cards: true, Release: "v9"})
	d := time.Since(start)
	t.Logf("list of %d cards: %s", n, d)
	require.Len(t, got.Streams, 1)
	assert.Equal(t, 2700, got.Streams[0].Open)
	assert.Equal(t, 300, got.Streams[0].Landed)
	assert.Equal(t, []string{"mas-bandwidth/nova-tools"}, got.Streams[0].Repos)
	assert.Equal(t, []string{"sprint/tools"}, got.Streams[0].Bases)
	require.Len(t, got.Streams[0].Cards, n)
	assert.Equal(t, "Count this card.", got.Streams[0].Cards[0].Title)
	assert.Empty(t, got.Findings)
}
