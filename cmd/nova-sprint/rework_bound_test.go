package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
)

// The automatic answer (nova-sprint answer, by nova-decide) applies only a verb the judgment
// prints (allowedVerbs, decide.Choose), and the judgment of a card at its brief's bound prints
// brief and drop, never rework (sprint.NBriefWrong; brief_bound.go): so the decision cannot
// rework such a card, whatever it chooses, and the rework verb refuses it besides. Before this
// the broken-read judgment printed rework, and the answer took it once an hour per card: on
// 2026-10-03 two gating cards reached attempts 262 and 17 with the same finding every time.
func TestTheAnswerPathCannotReworkACardAtTheBriefBound(t *testing.T) {
	t.Parallel()
	n := sprint.Note{ID: "j1", Kind: sprint.Judgment, Type: sprint.NBriefWrong, Stream: "s1", Card: "s1-1", Primaries: []string{"s1-1"},
		Decisions: append([]string(nil), sprint.Decisions[sprint.NBriefWrong]...), What: "s1-1 has failed the same way twice"}
	allowed := allowedVerbs(cardCommands(n, "s1-1"))
	assert.Equal(t, []string{decide.VerbDrop}, allowed, "brief is no verb the decision chooses, and rework is not printed")
	ch := decide.Choose(map[string]decide.Answer{"verb": {Type: decide.Choice, Value: decide.VerbRework, P: map[string]float64{decide.VerbRework: 0.99}},
		"fix": {Value: "keep to the PATHS"}}, allowed, 0.5)
	assert.Equal(t, decide.ActList, ch.Act)
	assert.Contains(t, ch.Why, "chose rework, which the judgment does not print")
	// the plain broken-read judgment still prints rework, for a first finding
	first := n
	first.Type, first.Decisions = sprint.NReadBroken, append([]string(nil), sprint.Decisions[sprint.NReadBroken]...)
	assert.Contains(t, allowedVerbs(cardCommands(first, "s1-1")), decide.VerbRework)
}
