package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gh's failed log read into its failing tests: the job and time cut from each
// line, one test per name (the first job's), its file and failing line, a
// build failure and a log with no go test output naming none.
func TestRedTestsReadsAFailedRunLog(t *testing.T) {
	t.Parallel()
	log := "hosted\tRun go test\t2026-10-05T21:00:00.1234567Z --- FAIL: TestA (0.01s)\n" +
		"hosted\tRun go test\t2026-10-05T21:00:00.1234567Z     a_test.go:9: want 1, got 2\n" +
		"hosted\tRun go test\t2026-10-05T21:00:00.1234567Z FAIL\tm/p\t0.1s\n" +
		"darwin\tRun go test\t2026-10-05T21:00:01.0000000Z --- FAIL: TestA (0.01s)\n" +
		"darwin\tRun go test\t2026-10-05T21:00:01.0000000Z     --- FAIL: TestB/sub (0.00s)\n" +
		"darwin\tRun go test\t2026-10-05T21:00:01.0000000Z --- FAIL: TestB (0.00s)\n" +
		"darwin\tRun go test\t2026-10-05T21:00:01.0000000Z FAIL\tm/q\t0.3s\n" +
		"build\tRun go vet\t2026-10-05T21:00:02.0000000Z s.go:3:2: undefined: x\n" +
		"build\tRun go vet\t2026-10-05T21:00:02.0000000Z FAIL\tm/s [build failed]\n"
	assert.Equal(t, []RedTest{
		{Test: "TestA", Pkg: "m/p", Job: "hosted", File: "a_test.go", Line: "a_test.go:9: want 1, got 2"},
		{Test: "TestB", Pkg: "m/q", Job: "darwin", Line: "--- FAIL: TestB/sub (0.00s)"},
	}, RedTests(log))
	assert.Empty(t, RedTests("lint\tRun make\t2026-10-05T21:00:00Z make: *** [lint] Error 1\n"))
}

// The add of a red run's cards: a test an open card names is left out, an id a
// landed card holds takes the next -<n>, and the rest score below every card.
func TestFixCardsDeduplicatesByTestNameAndRanksFirst(t *testing.T) {
	t.Parallel()
	sp := FixSpec{Repo: "o/r", Base: "sprint/x", Module: "m", Stream: RedStreamPrefix + "2026-10-05", Where: "dev at abc"}
	brief := func(test string) string {
		return FixBrief(RedTest{Test: test, Pkg: "m/p", Job: "hosted", File: "p_test.go", Line: "p_test.go:1: boom", Run: "7"}, sp)
	}
	s := newWorld(t).s
	s.Work.Put(&Card{ID: "other", Row: "s1", Col: Ready, Score: 3, Fields: map[string]string{"brief": brief("TestOpen")}})
	s.Work.Put(&Card{ID: FixCardID("TestAgain"), Row: sp.Stream, Col: Landed, Score: 1, Fields: map[string]string{"brief": brief("TestAgain")}})
	r := AddReq{Stream: sp.Stream, Cards: []CardAdd{
		{ID: FixCardID("TestOpen"), Brief: brief("TestOpen")},
		{ID: FixCardID("TestAgain"), Brief: brief("TestAgain")},
		{ID: FixCardID("TestNew"), Brief: brief("TestNew")},
	}}
	add, dup := FixCards(s, r)
	assert.Equal(t, []string{"TestOpen"}, dup)
	require.Len(t, add.Cards, 2)
	assert.Equal(t, "red-TestAgain-2", add.Cards[0].ID, "a landed card's id is taken")
	assert.Contains(t, add.Cards[0].Brief, "red-TestAgain-2: make TestAgain green")
	assert.Equal(t, "red-TestNew", add.Cards[1].ID)
	require.NotNil(t, add.Score)
	assert.Equal(t, -1.0, *add.Score, "below the lowest card on the table")
	assert.Equal(t, "TestNew", RedTestOf(add.Cards[1].Brief))
	assert.Equal(t, ".", RedDir("m", "m"))
}
