// TestConcurrentAppendKeepsFirstProse pins the immutable-winner rule in
// SPEC-CAIRN, "The four verbs": the same entry id carrying different prose is
// never an overwrite. Two writers that both read the entry absent must not
// both publish. The first entry to land is the only one kept, and every other
// writer re-reads it and reports duplicate or conflict under the same rules a
// sequential retry already uses.
package cairn

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// appendOutcome is one writer's result in the concurrent append storm.
type appendOutcome struct {
	prose string
	res   AppendResult
	err   error
}

// runConcurrentAppends starts one Append per prose at the same instant and
// returns every writer's outcome once all have returned. The shared start
// channel holds the writers until the last is ready, so they contend on the
// entry file rather than run one after another.
func runConcurrentAppends(t *testing.T, store, session, id string, prose []string, now time.Time) []appendOutcome {
	t.Helper()
	outcomes := make([]appendOutcome, len(prose))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range prose {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := Append(store, session, id, prose[i], "src", now, "manual")
			outcomes[i] = appendOutcome{prose: prose[i], res: res, err: err}
		}()
	}
	close(start)
	wg.Wait()
	return outcomes
}

func TestConcurrentAppendKeepsFirstProse(t *testing.T) {
	t.Parallel()

	const writers = 32

	t.Run("different prose keeps one winner and refuses the rest", func(t *testing.T) {
		t.Parallel()

		store := t.TempDir()
		now := time.Now().UTC()
		require.NoError(t, Open(store, "s", "src", now, "manual"))

		prose := make([]string, writers)
		for i := range prose {
			prose[i] = fmt.Sprintf("distinct prose %d", i)
		}
		outcomes := runConcurrentAppends(t, store, "s", "e", prose, now)

		winners, conflicts := 0, 0
		var winnerProse string
		var winnerStamp time.Time
		for _, o := range outcomes {
			if o.err != nil {
				var conflict *ConflictError
				require.ErrorAs(t, o.err, &conflict, "a losing writer with different prose is refused as a conflict, got %v", o.err)
				conflicts++
				continue
			}
			require.True(t, o.res.Persisted, "an accepted writer reports persisted, got %+v", o.res)
			if o.res.Duplicate {
				continue
			}
			winners++
			winnerProse = o.prose
			winnerStamp = o.res.Stamp
		}
		require.Equal(t, 1, winners, "exactly one writer publishes the entry; the others re-read it and report duplicate or conflict")
		require.Equal(t, writers-1, conflicts, "every losing writer with different prose is refused as a conflict")

		got, err := EntryText(store, "s", "e")
		require.NoError(t, err)
		require.Equal(t, winnerProse, got, "the persisted prose is the winner's, immutable")
		rc, err := Receipt(store, "s", "e")
		require.NoError(t, err)
		require.True(t, rc.Stamp.Equal(winnerStamp), "the stored stamp is the winner's: got %v want %v", rc.Stamp, winnerStamp)
		rows, _, total, err := Index(store, "")
		require.NoError(t, err)
		require.Equal(t, 1, total, "one id yields one entry")
		require.Len(t, rows, 1, "one id yields one entry")

		dup, err := Append(store, "s", "e", winnerProse, "src", now, "manual")
		require.NoError(t, err, "a sequential retry of the winner's prose still succeeds")
		require.True(t, dup.Duplicate, "the retry reports duplicate, got %+v", dup)
		require.True(t, dup.Stamp.Equal(winnerStamp), "the retry reports the original stamp: got %v want %v", dup.Stamp, winnerStamp)
		_, err = Append(store, "s", "e", "some other prose", "src", now, "manual")
		var conflict *ConflictError
		require.ErrorAs(t, err, &conflict, "a later different prose is still a conflict, got %v", err)
	})

	t.Run("identical prose keeps one entry and reports duplicates", func(t *testing.T) {
		t.Parallel()

		store := t.TempDir()
		now := time.Now().UTC()
		require.NoError(t, Open(store, "s", "src", now, "manual"))

		prose := make([]string, writers)
		for i := range prose {
			prose[i] = "the same words"
		}
		outcomes := runConcurrentAppends(t, store, "s", "e", prose, now)

		winners := 0
		var winnerStamp time.Time
		for _, o := range outcomes {
			require.NoError(t, o.err, "identical prose never conflicts")
			require.True(t, o.res.Persisted, "an accepted writer reports persisted, got %+v", o.res)
			if !o.res.Duplicate {
				winners++
				winnerStamp = o.res.Stamp
			}
		}
		require.Equal(t, 1, winners, "exactly one writer publishes the entry; the others are duplicates")
		duplicates := 0
		for _, o := range outcomes {
			if o.res.Duplicate {
				duplicates++
			}
			require.True(t, o.res.Stamp.Equal(winnerStamp), "every writer reports the winner's stamp: got %v want %v", o.res.Stamp, winnerStamp)
		}
		require.Equal(t, writers-1, duplicates, "every other identical writer is a duplicate")

		got, err := EntryText(store, "s", "e")
		require.NoError(t, err)
		require.Equal(t, "the same words", got)
		rows, _, total, err := Index(store, "")
		require.NoError(t, err)
		require.Equal(t, 1, total, "identical concurrent appends leave one entry")
		require.Len(t, rows, 1, "identical concurrent appends leave one entry")
	})
}
