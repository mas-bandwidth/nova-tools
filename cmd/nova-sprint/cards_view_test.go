package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestViewCardsCountsReviewByTier tests:
// a twin store with cards of several tiers and columns:
// --col review --by tier prints the counts; --json is one object; the default tier is the dealer's default.
func TestViewCardsCountsReviewByTier(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")

	// Cards with various tiers:
	// s1-1: flash
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "s1: task 1 (s1) tier: flash"))
	// s1-2: pro
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "s1: task 2 (s1) tier: pro"))
	// s1-3: default tier (no brief line -> dealer default is flash)
	ta.ok("add --stream s1 --count 1 --one")
	// s1-4: heavy (will stay in ready/working, not review)
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "s1: task 4 (s1) tier: heavy"))
	// s1-5: frontier (will stay in ready/waiting, not review)
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "s1: task 5 (s1) tier: frontier"))

	ta.deal(5)
	ta.ok("take --as m1 --limit 3")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1 s1-3.w1@1")

	// Text output: --col review --by tier prints the counts
	out := ta.ok("view cards --col review --by tier")
	assert.Contains(t, out, "VIEW cards by=tier total=3\n")
	assert.Contains(t, out, "flash 2\n")
	assert.Contains(t, out, "pro 1\n")
	assert.NotContains(t, out, "heavy")
	assert.NotContains(t, out, "frontier")

	// --json is one object
	jsonOut := strings.TrimSpace(ta.ok("view cards --col review --by tier --json"))
	var obj map[string]any
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &obj), "--json is one object")
	assert.Equal(t, "cards", obj["view"])
	assert.Equal(t, float64(viewSchema), obj["schema"])
	assert.Equal(t, float64(3), obj["total"])
	assert.Equal(t, "tier", obj["by"])
	counts, ok := obj["counts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(2), counts["flash"])
	assert.Equal(t, float64(1), counts["pro"])

	var v cardsView
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &v))
	assert.Equal(t, "cards", v.View)
	assert.Equal(t, viewSchema, v.Schema)
	assert.Equal(t, 3, v.Total)
	assert.Equal(t, "tier", v.By)
	assert.Equal(t, 2, v.Counts["flash"])
	assert.Equal(t, 1, v.Counts["pro"])
	assert.Zero(t, v.Counts["heavy"])
	assert.Zero(t, v.Counts["frontier"])

	// Also verify that without --col review, all cards are counted by tier:
	allTierOut := ta.ok("view cards --by tier")
	assert.Contains(t, allTierOut, "VIEW cards by=tier total=5\n")
	assert.Contains(t, allTierOut, "flash 2\n")
	assert.Contains(t, allTierOut, "frontier 1\n")
	assert.Contains(t, allTierOut, "heavy 1\n")
	assert.Contains(t, allTierOut, "pro 1\n")
}

func TestViewCardsListAndFilters(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")

	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "s1: task 1 (s1) tier: flash"))
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "s1: task 2 (s1) tier: pro"))
	ta.ok("add --stream s2 --count 1 --one --brief-file " + writeBrief(t, "s2: task 1 (s2) tier: heavy"))

	ta.deal(1)
	ta.ok("take --as m1 --limit 1")
	ta.ok("finish --as m1 s1-1.w1@1")

	// List without --by
	out := ta.ok("view cards --col review")
	assert.Contains(t, out, "VIEW cards total=1\n")
	assert.Contains(t, out, "CARD s1-1 stream=s1 col=review tier=flash holder=m1\n")

	// List JSON
	var v cardsView
	ta.json("view cards --col review", &v)
	assert.Equal(t, 1, v.Total)
	require.Len(t, v.Cards, 1)
	assert.Equal(t, "s1-1", v.Cards[0].ID)
	assert.Equal(t, "flash", v.Cards[0].Tier)
	assert.Equal(t, "m1", v.Cards[0].Holder)
	assert.Equal(t, "review", v.Cards[0].Col)

	// Filter by stream
	s2Out := ta.ok("view cards --stream s2")
	assert.Contains(t, s2Out, "CARD s2-1")
	assert.NotContains(t, s2Out, "s1-1")

	// Filter by holder
	hOut := ta.ok("view cards --holder m1")
	assert.Contains(t, hOut, "CARD s1-1")

	// By col
	colOut := ta.ok("view cards --by col")
	assert.Contains(t, colOut, "VIEW cards by=col total=3\n")
	assert.Contains(t, colOut, "review 1\n")

	// By stream
	streamOut := ta.ok("view cards --by stream")
	assert.Contains(t, streamOut, "VIEW cards by=stream total=3\n")
	assert.Contains(t, streamOut, "s1 2\n")
	assert.Contains(t, streamOut, "s2 1\n")

	// By holder
	holderOut := ta.ok("view cards --by holder")
	assert.Contains(t, holderOut, "VIEW cards by=holder total=3\n")
	assert.Contains(t, holderOut, "m1 1\n")
	assert.Contains(t, holderOut, "- 2\n")
}

func TestViewCardsRefusals(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")

	code, _, errs := ta.do("view cards extra")
	assert.NotZero(t, code)
	assert.Contains(t, errs, "takes no words")

	code, _, errs = ta.do("view cards --col nonsense")
	assert.NotZero(t, code)
	assert.Contains(t, errs, "--col wants")

	code, _, errs = ta.do("view cards --by nonsense")
	assert.NotZero(t, code)
	assert.Contains(t, errs, "--by wants")
}
