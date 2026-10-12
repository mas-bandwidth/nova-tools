package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The names are made up: no real friend's pronouns are set here, only the
// mechanism (the friend row's pronouns setting) is held.

func TestAFriendRowNamesItsPronounsOneOfThreeAndGoesByTheyThemWhenNone(t *testing.T) {
	t.Parallel()
	friend, ok := Lookup(KindFriend)
	require.True(t, ok)
	f, ok := friend.Field("pronouns")
	require.True(t, ok, "the friend kind has a pronouns field")
	assert.Equal(t, []string{"she/her", "he/him", "they/them"}, f.Enum)
	assert.Equal(t, "they/them", f.Default)
	assert.False(t, f.Required, "pronouns are optional")

	for _, p := range FriendPronounSets {
		row, err := friend.NewRow("pat", map[string]string{"slots": "8", "tiers": "flash", "pronouns": p})
		require.NoError(t, err, p)
		assert.Equal(t, p, row.Fields["pronouns"])
		assert.Equal(t, p, FriendPronouns(row))
	}

	row, err := friend.NewRow("pat", map[string]string{"slots": "8", "tiers": "flash"})
	require.NoError(t, err)
	assert.Equal(t, "they/them", row.Fields["pronouns"], "a row that names none goes by they/them")
	assert.Equal(t, "they/them", FriendPronouns(Row{Name: "pat", Fields: map[string]string{}}), "a stored row from before migration 0037 has none: they/them")
}

func TestAFriendRowRefusesPronounsThatAreNotOneOfTheThree(t *testing.T) {
	t.Parallel()
	friend, ok := Lookup(KindFriend)
	require.True(t, ok)
	for _, bad := range []string{"it/its", "she", "xe/xem"} {
		_, err := friend.Changes(map[string]string{"pronouns": bad})
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "want one of she/her, he/him, they/them", bad)
	}
}
