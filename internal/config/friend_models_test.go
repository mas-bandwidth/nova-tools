package config

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// addModels adds model rows, name then tier, to the store.
func addModels(t *testing.T, st Store, nameTier ...string) {
	t.Helper()
	k, _ := Lookup(KindModel)
	for i := 0; i < len(nameTier); i += 2 {
		row, err := k.NewRow(nameTier[i], map[string]string{"tier": nameTier[i+1]})
		require.NoError(t, err, "model %s", nameTier[i])
		_, err = st.Insert(context.Background(), KindModel, row, "t")
		require.NoError(t, err, "model %s", nameTier[i])
	}
}

// TestTheClassIsTheStrongestModelsTier: a friend's row lists her models
// strongest to weakest (the owner, 2026-10-06: "an array of models this agent
// can do, strongest to weakest"); her class is the tier of the first and the
// tiers she may be dealt are the tiers of them all, derived and never stored.
// A row with no models falls back to its tiers, the highest its class. The
// store refuses a model with no row, naming the verb that adds it, and a list
// not strongest first; the kind refuses both lists at once, neither, and a
// model twice.
func TestTheClassIsTheStrongestModelsTier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tiers := map[string]string{"grok-4-7-xhigh": "heavy", "grok-4-7": "pro", "deepseek-v4": "pro",
		"claude-fable-5-1": "frontier", "claude-opus-5-5": "heavy", "claude-sonnet-5-5": "pro", "claude-haiku-4-5": "flash"}
	for _, c := range []struct {
		models, tiers, class, dealt string
	}{
		{models: "grok-4-7-xhigh,grok-4-7", class: "heavy", dealt: "heavy,pro"},
		{models: "deepseek-v4", class: "pro", dealt: "pro"},
		{models: "claude-fable-5-1,claude-opus-5-5,claude-sonnet-5-5,claude-haiku-4-5", class: "frontier", dealt: "flash,frontier,heavy,pro"},
		{tiers: "flash,pro", class: "pro", dealt: "flash,pro"},
		{tiers: "heavy,pro", class: "heavy", dealt: "heavy,pro"},
		{},
	} {
		class, dealt, err := FriendClass(Row{Name: "f", Fields: map[string]string{"models": c.models, "tiers": c.tiers}}, tiers)
		require.NoError(t, err, "%+v", c)
		assert.Equal(t, c.class, class, "%+v: the class is the tier of her first model (or the highest of the fallback tiers)", c)
		assert.Equal(t, c.dealt, strings.Join(dealt, ","), "%+v: the tiers she may be dealt", c)
	}
	_, _, err := FriendClass(Row{Name: "f", Fields: map[string]string{"models": "nobody"}}, tiers)
	require.ErrorContains(t, err, "nova-config model add nobody --tier", "a model no row names")

	st := NewMem()
	addModels(t, st, "grok-4-7-xhigh", "heavy", "grok-4-7", "pro")
	friend, _ := Lookup(KindFriend)
	row, err := friend.NewRow("johnny", map[string]string{"slots": "1", "models": "grok-4-7-xhigh,grok-4-7"})
	require.NoError(t, err)
	assert.Equal(t, "grok-4-7-xhigh,grok-4-7", row.Fields["models"], "the order is kept: strongest first")
	_, err = st.Insert(ctx, KindFriend, row, "t")
	require.NoError(t, err)

	for _, c := range []struct{ models, refused string }{
		{"grok-4-7,grok-4-7-xhigh", "lists grok-4-7-xhigh (heavy) after a pro model; want her models strongest to weakest"},
		{"grok-4-7-xhigh,grok-5", "--models grok-5 names no model row; add it first: nova-config model add grok-5 --tier"},
		{"grok-4-7-xhigh,grok-4-7-xhigh", "names model grok-4-7-xhigh twice"},
	} {
		_, _, err := st.Update(ctx, KindFriend, "johnny", map[string]string{"models": c.models}, "t")
		require.ErrorContains(t, err, c.refused, "--models %s", c.models)
	}
	_, _, err = st.Update(ctx, KindFriend, "johnny", map[string]string{"tiers": "pro"}, "t")
	require.ErrorContains(t, err, "names both --models and --tiers", "both lists at once")
	_, _, err = st.Update(ctx, KindFriend, "johnny", map[string]string{"models": ""}, "t")
	require.ErrorContains(t, err, "names no model: want --models", "neither list")
	_, err = st.Delete(ctx, KindModel, "grok-4-7", "t")
	require.ErrorContains(t, err, "model grok-4-7 is in the --models of friend johnny", "a model a friend lists is not removed")

	derived, err := deriveFriend(ctx, st, []Row{row})
	require.NoError(t, err)
	assert.Equal(t, "heavy,pro", derived[0].Fields["tiers"], "apply writes the derived tiers to Redis")
	assert.Equal(t, "", row.Fields["tiers"], "the stored row is not changed")
}
