package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A reader row carries the tiers it reads (docs/SPEC-SPRINT.md section 6, a
// reader's tiers): reader add --tiers and reader set --tiers write them, the
// verbs and where print them, and the ask never asks a reader a read outside
// its tiers (internal/sprint/reader_tiers_test.go holds the ask's rule).
func TestReaderTiersAreSetPrintedAndKeptByTheAsk(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	assert.Contains(t, ta.ok("reader add reader-fast --tiers flash"), "READER-ADD OK readers=reader-fast tiers=flash")
	assert.Contains(t, ta.ok("reader set reader-a --tiers pro,flash"), "READER-SET OK readers=reader-a tiers=flash,pro", "in ladder order")
	assert.Contains(t, ta.ok("reader set reader-a --tiers flash,pro,heavy"), "tiers=flash,pro,heavy", "every tier is the default again")
	assert.Contains(t, ta.ok("reader add reader-fast"), "tiers=flash", "a row added again keeps its tiers")
	assert.Contains(t, ta.ok("reader set reader-a reader-fast --tiers flash,heavy"), "tiers=flash,heavy")
	assert.Contains(t, ta.ok("reader set reader-a --tiers flash,pro,heavy"), "tiers=flash,pro,heavy")
	assert.Contains(t, ta.ok("reader set reader-fast --tiers flash"), "tiers=flash")

	code, _, errs := ta.do("reader set reader-x --tiers flash")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no reader reader-x on the readers table")
	code, _, errs = ta.do("reader set reader-a --tiers turbo")
	assert.NotZero(t, code)
	assert.Contains(t, errs, `--tiers names "turbo"`)
	code, _, errs = ta.do("reader set reader-a")
	assert.NotZero(t, code)
	assert.Contains(t, errs, "wants --tiers")
	code, _, _ = ta.do("reader add reader-c --tiers frontier")
	assert.NotZero(t, code, "frontier is read on heavy: no reader reads frontier")

	assert.Contains(t, ta.ok("where --all"), "tiers: reader-fast reads flash", "under the readers table")

	// two pro cards in review: their reads go to the pro readers, never to fast
	ta.inReview(2)
	ta.ok("ask")
	assert.Empty(t, ta.askedOf("reader-fast"), "a flash reader is asked no pro read")
	assert.Len(t, append(ta.askedOf("reader-a"), ta.askedOf("reader-b")...), 2)
	// one pro reader up: the next reads wait, never asked of fast
	ta.ok("reader away reader-b")
	code, _, _ = ta.do("ask s1-1 --another")
	assert.NotZero(t, code, "no pro reader is free")
	assert.Empty(t, ta.askedOf("reader-fast"))
}
