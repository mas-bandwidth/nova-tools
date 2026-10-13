package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The names are made up. No real friend's pronouns are written here.

func TestAnUnstatedPronounIsShownUnstatedAndASetOneSurvivesEverySet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	friend, ok := Lookup(KindFriend)
	require.True(t, ok)

	empty := Row{Name: "pat", Fields: map[string]string{}}
	assert.Equal(t, "they/them", FriendPronouns(empty), "an empty value reads they/them")
	assert.Contains(t, ListLine(friend, empty), "pronouns=they/them(unstated)", "list prints an empty pronoun as unstated")
	assert.Contains(t, ShowLine(friend, empty), "pronouns=they/them(unstated)", "show prints an empty pronoun as unstated")
	statedLine := ListLine(friend, Row{Name: "pat", Fields: map[string]string{"pronouns": "she/her"}})
	assert.Contains(t, statedLine, "pronouns=she/her")
	assert.NotContains(t, statedLine, "unstated", "a stated pronoun prints bare")

	st := NewMem()
	row, err := friend.NewRow("pat", map[string]string{"tiers": "flash", "pronouns": "she/her", "width": "4"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindFriend, row, "ada")
	require.NoError(t, err)

	width, err := friend.Changes(map[string]string{"width": "9"})
	require.NoError(t, err)
	assert.NotContains(t, width, "pronouns", "a width set names no pronoun")
	got, _, err := st.Update(ctx, KindFriend, "pat", width, "ada")
	require.NoError(t, err)
	assert.Equal(t, "she/her", got.Fields["pronouns"], "a stated pronoun survives a width set")
	assert.Equal(t, "9", got.Fields["width"])

	unstated, err := friend.NewRow("quinn", map[string]string{"tiers": "flash", "width": "4"})
	require.NoError(t, err)
	assert.Equal(t, "", unstated.Fields["pronouns"], "add without --pronouns stores an unstated pronoun")
	_, err = st.Insert(ctx, KindFriend, unstated, "ada")
	require.NoError(t, err)
	gotU, _, err := st.Update(ctx, KindFriend, "quinn", width, "ada")
	require.NoError(t, err)
	assert.Equal(t, "", gotU.Fields["pronouns"], "a width set does not write the field default over an unstated pronoun")
	assert.Equal(t, "they/them", FriendPronouns(gotU))
	assert.Contains(t, ListLine(friend, gotU), "pronouns=they/them(unstated)")

	prev := View{}
	for k, v := range got.Fields {
		prev[k] = v
	}
	prev["width"] = "4"
	ops := Plan(friend, []Row{got}, map[string]View{"pat": prev})
	require.Len(t, ops, 1, "plan %+v", ops)
	assert.Equal(t, OpSet, ops[0].Op)
	assert.Equal(t, []string{"width"}, ops[0].Changed, "an apply plan of a width change does not rewrite pronouns: %+v", ops)
	assert.Equal(t, "she/her", ops[0].Row.Fields["pronouns"], "a stated pronoun survives the apply plan")

	next, err := friend.Changes(map[string]string{"pronouns": "he/him"})
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindFriend, "pat", next, "ada")
	require.NoError(t, err)
	hist, err := st.History(ctx, KindFriend, "pat")
	require.NoError(t, err)
	var saw bool
	for _, c := range hist {
		if c.Op == OpSet && c.Before["pronouns"] != c.After["pronouns"] {
			saw = true
			assert.Contains(t, HistoryLine(c), "pronouns=")
		}
	}
	assert.True(t, saw, "a pronoun change shows on the history path")
}
