package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A friend's card goes only to a friend whose tiers include its tier (docs/SPEC-SPRINT.md
// section 1; the owner, 2026-10-03 ~12:42 PM ET: "Now remember that some friends have
// weaker models. Freddy in particular is more like flash."; ~12:45 PM ET: "There is a
// responsibility to categorize cards for friends so they match to the set of friends who
// can do them, default all."): friend sync copies each friend row's tiers, add refuses a
// card naming a friend who cannot do its tier and a friend's card naming no tier, the deal
// matches, and card and where --json say who can take each card waiting.

// tierApp is a sprint whose friends are freddy (flash) and stella (flash and pro), synced
// and beating.
func tierApp(t *testing.T) *testApp {
	t.Helper()
	ta, cfg := friendApp(t)
	for name, tiers := range map[string]string{"freddy": "flash", "stella": "flash,pro"} {
		_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: name, Fields: map[string]string{"slots": "2", "tiers": tiers}}, "t")
		require.NoError(t, err)
	}
	ta.ok("friend sync --root " + t.TempDir())
	ta.ok("friend beat freddy")
	ta.ok("friend beat stella")
	return ta
}

// addFriendCard adds the card id to stream s1 with a passing brief leading with lead.
func (ta *testApp) addFriendCard(id, lead string) (int, string) {
	ta.t.Helper()
	dir := ta.t.TempDir()
	require.NoError(ta.t, os.WriteFile(filepath.Join(dir, id+".md"), []byte(passingBrief(lead)), 0o644))
	code, _, errs := ta.do("add --stream s1 --brief-dir " + dir)
	return code, errs
}

func TestAddRefusesAFriendsCardOfATierItsFriendCannotDoAndOneWithNoTier(t *testing.T) {
	t.Parallel()
	ta := tierApp(t)
	code, errs := ta.addFriendCard("c1", "c1: a card tier: pro\nWHO: friend freddy")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "the brief says WHO: friend freddy and tier: pro, and freddy's tiers are flash: name a friend whose tiers include pro, write WHO: friend for any friend who can, or change the card's tier")

	code, errs = ta.addFriendCard("c2", "c2: a card\nWHO: friend")
	assert.Equal(t, 2, code, "a friend's card naming no tier is refused")
	assert.Contains(t, errs, "LINT DRIFT brief friend-tier: 1: c2: a card remedy=a friend's card (WHO: friend or WHO: friend <name>) names its tier on line 1")

	code, _ = ta.addFriendCard("c3", "c3: a card\nREPO: mas-bandwidth/nova-tools")
	assert.Equal(t, 0, code, "a machine's card names no tier, as before")
	code, _ = ta.addFriendCard("c4", "c4: a card tier: pro\nWHO: friend stella")
	assert.Equal(t, 0, code, "stella does pro")
	code, _ = ta.addFriendCard("c5", "c5: a card tier: frontier\nWHO: friend")
	assert.Equal(t, 0, code, "a card for any friend is admitted whoever can take it: the deal judges one no friend can")
}

func TestFriendSyncCopiesTheTiersAndAChangeIsAnUpdate(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy")
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"tiers": "flash,pro"}, "t")
	require.NoError(t, err)
	assert.Contains(t, ta.ok("friend sync --root "+root), "FRIEND-SYNC OK added=- removed=- updated=amy friends=1 jobs=0")
	assert.Contains(t, ta.ok("friend sync --root "+root), "nothing to do")
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	tiers, err := st.FriendTiers(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"amy": {"flash", "pro"}}, tiers)
}

// card and where --json say, for each friend's card waiting, which friends can take it;
// the tick deals the pro card only to stella, and judges the frontier card no friend can.
func TestCardAndWhereShowWhoCanTakeAFriendsCard(t *testing.T) {
	t.Parallel()
	ta := tierApp(t)
	for id, lead := range map[string]string{
		"s1-1": "s1-1: a card tier: flash\nREPO: mas-bandwidth/nova-tools\nWHO: friend",
		"s1-2": "s1-2: a card tier: pro\nREPO: mas-bandwidth/nova-tools\nWHO: friend",
		"s1-3": "s1-3: a card tier: frontier\nREPO: mas-bandwidth/nova-tools\nWHO: friend",
	} {
		code, errs := ta.addFriendCard(id, lead)
		require.Equal(t, 0, code, errs)
	}
	assert.Contains(t, ta.ok("card s1-1"), " who=friend takers=freddy,stella\n")
	assert.Contains(t, ta.ok("card s1-3"), " who=friend takers=-\n", "no friend can take a frontier card")
	var c cardView
	ta.json("card s1-2", &c)
	require.NotNil(t, c.Takers)
	assert.Equal(t, []string{"stella"}, *c.Takers)
	ta.json("card s1-3", &c)
	require.NotNil(t, c.Takers)
	assert.Empty(t, *c.Takers, "takers [] when none can")

	var w whereView
	ta.json("where", &w)
	got := map[string][]string{}
	for _, f := range w.FriendCards {
		got[f.Card] = f.Takers
		assert.Equal(t, sprint.WhoFriend, f.Who)
		assert.Equal(t, sprint.Ready, f.Col)
	}
	assert.Equal(t, map[string][]string{"s1-1": {"freddy", "stella"}, "s1-2": {"stella"}, "s1-3": {}}, got)

	ta.ok("start")
	ta.ok("tick")
	ta.json("card s1-2", &c)
	require.Len(t, c.Work, 1)
	assert.Equal(t, sprint.FriendRow("stella"), c.Work[0].Row, "the pro card goes to stella, never to flash-only freddy")
	assert.Nil(t, c.Takers, "a card dealt shows no takers")
	ta.json("where", &w)
	got = map[string][]string{}
	for _, f := range w.FriendCards {
		got[f.Card] = f.Takers
	}
	assert.Equal(t, map[string][]string{"s1-3": {}}, got, "the tick's where record: only the frontier card waits, and no friend can take it")
	inbox := ta.ok("inbox")
	assert.Contains(t, inbox, "1 friend's cards of tier frontier wait and no friend who may take them can do tier frontier (s1-3); friends: freddy (flash), stella (flash,pro)")
	ta.clean()
}
