package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reader set and reader add --tiers write the tiers cell, where prints it
// (all when empty), and a tier that is not a route writes nothing.
func TestReaderSetPrintsTiersAndRefusesABadTier(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("reader add reader-flash --tiers flash")
	ta.ok("reader set reader-a --tiers pro,flash")

	var v struct {
		Tables map[string]map[string]map[string]string
	}
	ta.json("where", &v)
	readers := v.Tables[sprint.Readers]
	require.Equal(t, "flash", readers["reader-flash"][sprint.ReaderTiers])
	require.Equal(t, "flash,pro", readers["reader-a"][sprint.ReaderTiers], "stored in ladder order")
	require.Equal(t, "all", readers["reader-b"][sprint.ReaderTiers], "an empty cell prints all")

	code, _, errs := ta.do("reader set reader-a --tiers no-such")
	assert.NotEqual(t, 0, code, errs)
	ta.json("where", &v)
	assert.Equal(t, "flash,pro", v.Tables[sprint.Readers]["reader-a"][sprint.ReaderTiers], "a bad tier writes nothing")

	ta.ok("reader set reader-a --tiers all")
	ta.json("where", &v)
	assert.Equal(t, "all", v.Tables[sprint.Readers]["reader-a"][sprint.ReaderTiers])

	code, _, errs = ta.do("reader set reader-nope --tiers flash")
	assert.NotEqual(t, 0, code, errs)
	assert.Contains(t, errs, "nothing was changed")
}
