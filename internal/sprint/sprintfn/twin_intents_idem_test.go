package sprintfn

import (
	"math/rand/v2"
	"strconv"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Idempotence (1.3.3, E7): an intent run a second time on the state its first
// run left, with nothing changed in between, decides no entry and no command.
// The request is the same (the same create entries, the same intents); what the
// first run decided is in the world: its commands in the sprint's keys, the
// fields its entries set on the cards, and the cards a waitfor admitted. The
// requests it gave J are not writes, and may be asked again: J opens a
// judgment once for a cause.

// applyPlan puts a plan in the scenario's world.
func applyPlan(sc *diffScenario, plan derivePlan) {
	sc.keys = append(append([]Cmd{}, sc.keys...), plan.Cmds...)
	recs := make(map[string]fixRec, len(sc.recs))
	for id, r := range sc.recs {
		recs[id] = r
	}
	for _, e := range plan.Entries {
		for i, id := range e.IDs {
			r := recs[id]
			rev, _ := strconv.Atoi(r.rev)
			fields := map[string]string{}
			for k, v := range r.fields {
				fields[k] = v
			}
			for k, v := range e.Each[i] {
				fields[k] = v
			}
			recs[id] = fixRec{row: r.row, col: r.col, rev: strconv.Itoa(rev + 1), fields: fields}
		}
	}
	idx := newRequestIndex(sc.entries, &deriveWork{})
	for _, it := range sc.intents {
		if it.Kind != IntentWaitFor {
			continue
		}
		if made, ok := idx.created(it.Card); ok {
			recs[it.Card] = fixRec{row: "s1", col: string(sprint.Waiting), rev: "1", fields: map[string]string{
				deriveFieldNeeds: made.fields[deriveFieldNeeds], deriveFieldOpen: made.fields[deriveFieldOpen]}}
		}
	}
	sc.recs = recs
}

// TestIntentsSecondRunWritesNothing: on 4,000 random worlds and steps, each
// intent that decides something decides nothing the second time: no entry, no
// command, and no refusal. The draw reaches needmet, needgone, waive and
// waitfor (with a need that has no record, which enters wait:<n> and missing).
func TestIntentsSecondRunWritesNothing(t *testing.T) {
	t.Parallel()
	const chunks, per = 4, 1000
	var mu sync.Mutex
	decided := map[string]int{}
	for chunk := 0; chunk < chunks; chunk++ {
		t.Run("chunk "+strconv.Itoa(chunk), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewPCG(uint64(chunk)+1, 0x1de4))
			for i := 0; i < per; i++ {
				sc := randomScenario(rng)
				first, ref, _ := runDerive(sc)
				if ref != nil {
					continue
				}
				kinds := map[string]bool{}
				for _, it := range sc.intents {
					kinds[it.Kind] = true
				}
				mu.Lock()
				for k := range kinds {
					if len(first.Entries)+len(first.Cmds) > 0 {
						decided[k]++
					}
				}
				mu.Unlock()
				applyPlan(&sc, first)
				second, ref, _ := runDerive(sc)
				if ref != nil {
					t.Fatalf("run %d: the second run was refused: %v\nintents %+v", i, ref, sc.intents)
				}
				if len(second.Entries) != 0 || len(second.Cmds) != 0 {
					t.Fatalf("run %d: the second run decided %d entries and %d commands; want none\nfirst: %+v %+v\nsecond: %+v %+v\nintents %+v",
						i, len(second.Entries), len(second.Cmds), first.Entries, first.Cmds, second.Entries, second.Cmds, sc.intents)
				}
			}
		})
	}
	t.Cleanup(func() {
		for _, kind := range []string{IntentWaitFor, IntentNeedMet, IntentNeedGone, IntentWaive} {
			if decided[kind] < 150 {
				t.Errorf("the draw ran %d steps with a %s that decided something; want at least 150 (%v)", decided[kind], kind, decided)
			}
		}
	})
}
