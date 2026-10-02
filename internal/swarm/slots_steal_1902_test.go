package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Giving back the seat you are sitting in is the ordinary end of a run, not a steal:
// run's dispatcher and native's cleanup both release a lease whose pid is this
// process's and is alive.
func TestAHolderMayStillReleaseItsOwnLiveSeat(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	writeShares1902(t, store, "capacity\t2\nreserve\t0\nbench\t2\n")
	_, _, _, _, _, ok, err := TakeSlotLeases(store, "bench", 1, time.Hour, "card-7", time.Now().UTC(), os.Getpid())
	require.NoError(t, err, "take: ok=%v err=%v", ok, err)
	require.True(t, ok, "take: ok=%v err=%v", ok, err)
	released, held, live, err := ReleaseSlotLeasesForcing(store, "bench", "card-7", false, false)
	require.NoError(t, err, "a holder could not give back its own seat: released=%d held=%d live=%d err=%v", released, held, live, err)
	require.Equal(t, 1, released, "a holder could not give back its own seat: released=%d held=%d live=%d err=%v", released, held, live, err)
	require.Equal(t, 0, held, "a holder could not give back its own seat: released=%d held=%d live=%d err=%v", released, held, live, err)
	require.Equal(t, 0, live, "a holder could not give back its own seat: released=%d held=%d live=%d err=%v", released, held, live, err)
	_, err = os.Stat(filepath.Join(store, "slots"))
	require.NoError(t, err)
}

func writeShares1902(t *testing.T, store, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(store, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(store, "shares.tsv"), []byte(body), 0o644))
}
