package refmodel_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// The levelrefuser case (Zhi, zhi-d61b4dd67f9b): the emptiest open member
// refused the longest queue's newest card at staging. The wedge of 2026-10-02
// (nova-tools#5122) at the model's one width: four members up, A holding 3
// ready cards, T 2, S 1 and U 4 working, so the backlogs are -61, -62, -63
// and -60 and the mean -62; A's newest refused by S. The engine's level and
// the model's agree on one move, A's next older card to S: the refused card is
// skipped, not moved one below (no gap: the wedge's rule) nor onto S (no skip:
// the #5000 probe), and the call ends.
func TestLevelRefuserTheEngineAndTheModelAgree(t *testing.T) {
	t.Parallel()
	w := newWorld("reader-a", "reader-b")
	for _, m := range []string{"A", "T", "S", "U"} {
		w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "up", Member: m, Width: refmodel.Width, Who: coordinator}))
	}
	w.s.Work.SetRows(append(w.s.Work.Rows(), "s1"))
	score := 1.0
	put := func(id, member, col string, extra map[string]string) {
		fields := map[string]string{"kind": "work", "primary": id, "stream": "s1", "attempt": "1", "gen": "2", "member": member,
			"dealt": t0.UTC().Format(time.RFC3339), "untaken_since": t0.UTC().Format(time.RFC3339), "redeals": "0"}
		for k, v := range extra {
			fields[k] = v
		}
		w.s.Fleet.Put(&sprint.Card{ID: id + ".w1", Row: member, Col: col, Score: score, Rev: 1, Fields: fields})
		w.s.Work.Put(&sprint.Card{ID: id, Row: "s1", Col: sprint.Working, Score: score, Rev: 1,
			Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1", "work": id + ".w1"}})
		score++
	}
	for member, n := range map[string]int{"A": 2, "T": 2, "S": 1} {
		for i := range n {
			put(fmt.Sprintf("%s-ready-%d", member, i), member, sprint.Ready, nil)
		}
	}
	for i := range 4 {
		put(fmt.Sprintf("U-working-%d", i), "U", sprint.Working, nil)
	}
	put("A-refused", "A", sprint.Ready, map[string]string{sprint.FieldStagingTake + "1": sprint.ProviderTake{Member: "S"}.String()})

	p := sprint.FleetStep(w.s, sprint.FleetReq{Op: "level", Who: sprint.MachineActor})
	require.Empty(t, p.Refused)
	engine := map[string]string{}
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Entry.Move != nil {
				engine[u.Key] = ch.Entry.Move.Row
			}
		}
	}
	assert.Equal(t, map[string]string{"A-ready-1.w1": "S"}, engine)

	s := refmodel.Abstract(refmodel.Observed{Snap: w.s, Machine: refmodel.Running})
	assert.Equal(t, []string{"S"}, s.Work["A-refused.w1"].Refusers)
	after, err := refmodel.Level(s, engine)
	require.NoError(t, err, "the model refuses the engine's level")
	for id, c := range after.Work {
		assert.NotContains(t, c.Refusers, c.Member, "%s levelled onto a member that refused it", id)
	}
	assert.Equal(t, "A", after.Work["A-refused.w1"].Member)
	assert.Equal(t, "S", after.Work["A-ready-1.w1"].Member)
}
