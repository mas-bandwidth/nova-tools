package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// TestTheAskVerbLoadsEveryReadIdentityOfAReask pins that the real store's ask
// step loads the re-ask records of a read taken back with no verdict: its
// extras name every identity up to the take-back bound (plain, .g1, .g2), so
// the ask sees a retired re-ask instead of recreating an id that already
// exists (read-asked-again-after-takebackc-t-b). The ask verb plans through
// store.AskStep, so this is the shape the live store is read in.
func TestTheAskVerbLoadsEveryReadIdentityOfAReask(t *testing.T) {
	t.Parallel()
	s := &sprint.Snapshot{Readers: sprint.NewTable(sprint.Readers), Work: sprint.NewTable(sprint.Work)}
	s.Readers.SetRows([]string{"reader-a"})
	s.Work.SetRows([]string{"s1"})
	s.Work.Put(&sprint.Card{ID: "s1-1", Row: "s1", Col: sprint.Review, Rev: 1,
		Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1"}})
	step := store.AskStep(sprint.AskReq{})
	require.NotNil(t, step.Extras)
	reads := step.Extras(s)[sprint.Readers]
	for _, id := range []string{"s1-1.r1.reader-a", "s1-1.r1.reader-a.g1", "s1-1.r1.reader-a.g2"} {
		assert.Contains(t, reads, id, "the ask step loads %s", id)
	}
}
