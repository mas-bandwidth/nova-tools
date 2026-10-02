package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// primary is a primary as the store holds it, placed.
func (ta *testApp) primary(id string) *sprint.Card {
	ta.t.Helper()
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	s, err := st.Load(context.Background(), []string{sprint.Work}, nil)
	require.NoError(ta.t, err)
	c := s.Work.Placed(id)
	require.NotNil(ta.t, c, "no primary %s placed", id)
	return c
}

// brief end to end on the twin (the owner, 2026-10-01: "What other things
// should you be able to do to mutate a stopped sprint" / "I don't want you
// manually hopping in and working around it and doing manual stuff."): refused
// on a RUNNING machine, a brief that fails the card lint refused with nothing
// changed, replaced on a STOPPED machine with the card's id, stream, score and
// needs kept, and refused once the card is dealt.
func TestBriefReplacesAnUnstartedPrimarysBriefOnAStoppedSprint(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 1 --brief-file " + writeBrief(t, "the old work"))
	ta.ok("add --stream a a-2 --needs a-1 --brief-file " + writeBrief(t, "the old second"))
	good := writeBrief(t, "the new work")
	before := *ta.primary("a-2")

	ta.ok("start")
	code, _, errs := ta.do("brief a-2 --brief-file " + good)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "the machine is RUNNING")
	assert.Contains(t, errs, "run: nova-sprint stop")
	ta.ok("stop")

	applies := ta.applies()
	code, out, errs := ta.do("brief a-2 --brief 'handle the empty case'")
	assert.Equal(t, 2, code)
	assert.NotContains(t, out, "MOVED")
	assert.Contains(t, errs, "LINT DRIFT brief rule-worktree")
	assert.Contains(t, errs, "nova-sprint brief: the brief fails the card lint")
	assert.Equal(t, applies, ta.applies(), "a brief that fails the lint wrote")
	assert.Equal(t, before.Fields["brief"], ta.primary("a-2").F("brief"))

	// over the bound a brief may be, as add's: refused whole, nothing written
	code, out, errs = ta.do("brief a-2 --brief-file " + writeBrief(t, strings.Repeat("x", store.MaxBriefBytes)))
	assert.NotEqual(t, 0, code)
	assert.NotContains(t, out, "MOVED")
	assert.Contains(t, errs, "over the bound")
	assert.Equal(t, applies, ta.applies(), "a brief over the bound wrote")

	assert.Contains(t, ta.ok("brief a-2 --brief-file "+good), "a-2 brief replaced")
	after := ta.primary("a-2")
	assert.Equal(t, strings.TrimSuffix(passingBrief("the new work"), "\n"), after.F("brief"))
	assert.Equal(t, before.Row, after.Row)
	assert.Equal(t, before.Col, after.Col)
	assert.Equal(t, before.Score, after.Score)
	assert.Equal(t, "a-1", after.F("needs"))

	ta.deal(1)
	code, _, errs = ta.do("brief a-1 --brief-file " + good)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "a-1 is working: a card dealt, working, in review, merging or landed keeps its brief")
	ta.clean()
}
