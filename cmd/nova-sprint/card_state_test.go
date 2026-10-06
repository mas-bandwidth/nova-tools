package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCardStateIsTheTableColumnInEveryReader: a card's live column is one
// field, read from its table row, that `card --json` and `needs` both print
// under "column" — and `needs` labels the waiting card's column apart from
// each need's column, so the 2026-10-04 misreading (a need's state read as
// the card's) cannot happen again. The twin store stands in for the sprint.
func TestCardStateIsTheTableColumnInEveryReader(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	// b first, then a waits on it: both are waiting cards, and a's one need is b.
	ta.ok("add --one --stream s1 b")
	ta.ok("add --one --stream s1 a --needs b")

	var card map[string]any
	ta.json("card a", &card)
	require.Equal(t, "waiting", card["column"], "card --json prints the table row's column under \"column\"")

	var needs struct {
		Streams []struct {
			Cards []struct {
				ID     string `json:"id"`
				Column string `json:"column"`
				Needs  []struct {
					ID     string `json:"id"`
					Column string `json:"column"`
				} `json:"needs"`
			} `json:"cards"`
		} `json:"streams"`
	}
	ta.json("needs --stream s1", &needs)
	found := false
	for _, st := range needs.Streams {
		for _, c := range st.Cards {
			if c.ID != "a" {
				continue
			}
			found = true
			require.Equal(t, "waiting", c.Column, "the waiting card's own column, labelled")
			require.Len(t, c.Needs, 1)
			require.Equal(t, "b", c.Needs[0].ID)
			require.Equal(t, "ready", c.Needs[0].Column, "the need's column, labelled apart from the card's")
		}
	}
	require.True(t, found, "needs prints the waiting card a")

	out := ta.ok("needs --stream s1")
	require.Contains(t, out, "card a column waiting", "needs text names the card's column")
	require.Contains(t, out, "need b column ready", "needs text names the need's column apart")
	ta.clean()
}
