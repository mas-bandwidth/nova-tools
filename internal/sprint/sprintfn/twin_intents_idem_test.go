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

// secondRunWritesNothing runs the scenario, puts what it decided in the world,
// runs it again, and fails unless the second run decides no entry, no command
// and no refusal. It says whether the first run decided anything; a first run
// that was refused decided nothing and has no second run to check.
func secondRunWritesNothing(t *testing.T, sc diffScenario, label string) bool {
	t.Helper()
	first, ref, _ := runDerive(sc)
	if ref != nil {
		return false
	}
	applyPlan(&sc, first)
	second, ref, _ := runDerive(sc)
	if ref != nil {
		t.Fatalf("%s: the second run was refused: %v\nintents %+v", label, ref, sc.intents)
	}
	if len(second.Entries) != 0 || len(second.Cmds) != 0 {
		t.Fatalf("%s: the second run decided %d entries and %d commands; want none\nfirst: %+v %+v\nsecond: %+v %+v\nintents %+v",
			label, len(second.Entries), len(second.Cmds), first.Entries, first.Cmds, second.Entries, second.Cmds, sc.intents)
	}
	return len(first.Entries)+len(first.Cmds) > 0
}

// TestIntentsSecondRunWritesNothing: on 4,000 random worlds and steps, the
// second run of a step decides no entry, no command, and no refusal; and so does
// the second run of each of its intents alone, which is what says which kind
// did the deciding. The draw reaches needmet, needgone, waive and waitfor (with
// a need that has no record, which enters wait:<n> and missing), and a kind is
// counted only for the intents that decided something when run alone, so that a
// kind whose rule decided nothing would fail the minimum below while another
// intent in its step decided.
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
				secondRunWritesNothing(t, sc, "run "+strconv.Itoa(i))
				for j, it := range sc.intents {
					solo := sc
					solo.intents = []Intent{it}
					if secondRunWritesNothing(t, solo, "run "+strconv.Itoa(i)+" intent "+strconv.Itoa(j)+" ("+it.Kind+") alone") {
						mu.Lock()
						decided[it.Kind]++
						mu.Unlock()
					}
				}
			}
		})
	}
	t.Cleanup(func() {
		t.Logf("intents that decided something alone, by kind: %v", decided)
		for _, kind := range []string{IntentWaitFor, IntentNeedMet, IntentNeedGone, IntentWaive} {
			if decided[kind] < 150 {
				t.Errorf("the draw ran %d intents of %s that decided something alone; want at least 150 (%v)", decided[kind], kind, decided)
			}
		}
	})
}
