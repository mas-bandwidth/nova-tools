package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// needs <card> edits a card's needs in place (the owner, 2026-10-07: "If it's just
// dependencies, please check if the dependencies are still correct"): a stale need dropped
// leaves the card waiting on the live one; the last unlanded need dropped moves it to ready
// in the same step; each change is one log line with the actor and the reason; needs <card>
// alone prints the needs with each need's state and what the card still waits for.
func TestNeedsEditsACardsNeedsInPlace(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 b --one --needs s1-1,s1-2")
	before := ta.primary("b")
	require.Equal(t, sprint.Waiting, before.Col)

	out := ta.ok("needs b")
	assert.Contains(t, out, "NEED s1-1 ready\n")
	assert.Contains(t, out, "NEED s1-2 ready\n")
	assert.Contains(t, out, "NEEDS OK card=b state=waiting needs=2 waits_for=s1-1,s1-2")
	var v struct {
		Card    string             `json:"card"`
		State   string             `json:"state"`
		Needs   []sprint.NeedState `json:"needs"`
		WaitsOn []string           `json:"waits_for"`
	}
	ta.json("needs b", &v)
	assert.Equal(t, "b", v.Card)
	assert.Equal(t, []string{"s1-1", "s1-2"}, v.WaitsOn)
	require.Len(t, v.Needs, 2)
	assert.Equal(t, sprint.NeedState{ID: "s1-1", State: "ready"}, v.Needs[0])

	// one need dropped: the card waits on the other, its id, stream and place kept
	out = ta.ok("needs b --drop s1-2 --reason 'deferred to the next release'")
	assert.Contains(t, out, "b needs s1-1,s1-2 -> s1-1: deferred to the next release", "needs --drop")
	after := ta.primary("b")
	assert.Equal(t, sprint.Waiting, after.Col)
	assert.Equal(t, "s1-1", after.F("needs"))
	assert.Equal(t, before.Row, after.Row, "the stream")
	assert.Equal(t, before.Score, after.Score, "the place in line")
	logged := ta.ok("log --card b")
	assert.Contains(t, logged, "b needs s1-1,s1-2 -> s1-1: deferred to the next release", "the change is on its timeline with the reason")
	assert.Contains(t, logged, "coordinator", "and the actor")
	assert.Contains(t, ta.ok("needs b"), "NEEDS OK card=b state=waiting needs=1 waits_for=s1-1")

	// the last unlanded need dropped: ready in the same step
	out = ta.ok("needs b --drop s1-1 --reason 'done on the base already'")
	assert.Contains(t, out, "b waiting -> ready (its needs landed)", "needs --drop to none")
	after = ta.primary("b")
	assert.Equal(t, sprint.Ready, after.Col)
	assert.False(t, after.Has("needs"))
	assert.Contains(t, ta.ok("needs b"), "NEEDS OK card=b state=ready needs=0 waits_for=-")

	// --add puts a landed need on a ready card, records it and moves nothing; an unlanded
	// one is refused (a ready card would be dealt before its need)
	code, _, errs := ta.do("needs b --add s1-2 --reason 'it builds on s1-2'")
	require.Equal(t, 1, code, "an unlanded need on a ready card: %s", errs)
	assert.Contains(t, errs, "b is ready and s1-2 is ready, not landed", errs)
	assert.Equal(t, sprint.Ready, ta.primary("b").Col)
	ta.clean()
}

// A drop that leaves only live needs keeps the card waiting; a cycle, a need the card does
// not have, a card off the table, a landed card and a missing reason are refused with nothing
// written; the edit is the coordinator's alone and wants an actor.
func TestNeedsRefusesACycleALandedCardAndAnIntruder(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 b --one --needs s1-1")
	ta.ok("add --stream s2 c --one --needs b")
	before := ta.applies()
	for _, tc := range []struct {
		line string
		code int
		want string
	}{
		{"needs b --add c --reason r", 1, "the needs would make a cycle: b needs c needs b"},
		{"needs b --drop s1-2 --reason r", 1, "s1-2 is no need of b (its needs: s1-1)"},
		{"needs b --add nosuch --reason r", 1, "not a card on the table: nosuch (no card)"},
		{"needs b --drop s1-1", 2, "wants --reason <text>"},
		{"needs --drop s1-1 --reason r", 2, "--drop, --add and --reason want <card>"},
		{"needs b --roots", 2, "not both"},
		{"needs b c", 2, "takes one <card> at most"},
		{"needs b --drop s1-1 --reason r --actor intruder", 2, "the coordinator's alone: coordinator, not intruder"},
	} {
		code, out, errs := ta.do(tc.line)
		assert.Equal(t, tc.code, code, "%s: exit %d\n%s%s", tc.line, code, out, errs)
		assert.Contains(t, errs, tc.want, "%s: exit %d\n%s%s", tc.line, code, out, errs)
	}
	require.Equal(t, before, ta.applies(), "a refused needs wrote")
	assert.Equal(t, "s1-1", ta.primary("b").F("needs"), "a refusal changed the needs")
	assert.Equal(t, sprint.Waiting, ta.primary("b").Col)

	// no actor at all: the write is refused as every write is; the reads still run
	ta.a.getenv = func(k string) string { return map[string]string{"NOVA_SPRINT_REDIS": "mem:0"}[k] }
	code, _, errs := ta.do("needs b --drop s1-1 --reason r")
	assert.Equal(t, 2, code, "no actor: %d %s", code, errs)
	assert.Contains(t, errs, "--actor <name> is required (or NOVA_SPRINT_ACTOR)", errs)
	ta.ok("needs b")
	ta.ok("needs --stream s2")
	ta.a.getenv = func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": "mem:0", "NOVA_SPRINT_ACTOR": "coordinator"}[k]
	}

	// a landed card keeps its needs: landed is final
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1")
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 5")
	ta.ok("read --as reader-b --ok --limit 5")
	ta.ok("accept --read-ok")
	ta.ok("merge --stream s1 --batch 10")
	ta.ok("tick")
	require.Equal(t, sprint.Landed, ta.primary("s1-1").Col)
	require.NotEqual(t, sprint.Waiting, ta.primary("b").Col, "b went ready as its need landed (and the running machine may have dealt it)")
	code, _, errs = ta.do("needs s1-1 --drop s1-2 --reason r")
	assert.Equal(t, 1, code, "a landed card: %d %s", code, errs)
	assert.Contains(t, errs, "s1-1 landed: landed is final", errs)
	// a sentinel's needs change with sentinel set
	ta.ok("add --stream s2 --sentinel s2-stop")
	code, _, errs = ta.do("needs s2-stop --add s1-2 --reason r")
	assert.Equal(t, 1, code, "a sentinel: %d %s", code, errs)
	assert.Contains(t, errs, "s2-stop is a sentinel: its needs change with nova-sprint sentinel set", errs)
	ta.clean()
}

// The verb's usage in the command reference is the verb table's, word for word, and its -h
// carries the verb's own paragraph (docs/CLI.md is what a stranger reads first).
func TestNeedsUsageIsInTheCLIReference(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	require.NoError(t, err)
	var syntax string
	for _, v := range verbs {
		if v.name == "needs" {
			syntax = v.syntax
		}
	}
	require.NotEmpty(t, syntax)
	assert.Contains(t, string(raw), "\nnova-sprint needs "+syntax+"\n", "docs/CLI.md's verbs block lacks the needs line as the verb table prints it")
	a := newApp(func(string) string { return "" })
	var out, errb strings.Builder
	code := a.run([]string{"needs", "-h"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), needsWords, "needs -h lacks its paragraph")
	assert.Contains(t, out.String(), "needs "+syntax, "needs -h lacks its usage")
}
