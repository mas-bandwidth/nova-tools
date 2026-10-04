// Unit coverage for the two lease.go entry points the unit tier's per-function
// table showed at zero: JobLease.Live and StartSlotLease. Everything runs
// in-process over plain files in t.TempDir() through the package's own seams:
// no sleeps, no real time, no network, no subprocess, no Redis or Postgres.
package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLeaseCoverLiveAsksTheKernelOrTheHeartbeat reaches Live on every answer
// it can give. Live is reclaimable negated: a record whose pid this kernel can
// be asked about is live while the pid is alive, and a record it cannot ask
// about -- unparsed, or written on another host -- is live until its heartbeat
// is older than JobLeaseStale. now is handed in, so no real time is read.
func TestLeaseCoverLiveAsksTheKernelOrTheHeartbeat(t *testing.T) {
	t.Parallel()

	host, err := os.Hostname()
	require.NoError(t, err)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-JobLeaseStale - time.Second)

	rows := []struct {
		name  string
		lease JobLease
		want  bool
	}{
		{
			name:  "the main path: a record naming this process's live pid",
			lease: JobLease{PID: os.Getpid(), Host: host, Known: true, Beat: stale},
			want:  true,
		},
		{
			name:  "an unknown owner is held while its heartbeat is young",
			lease: JobLease{Known: false, Beat: now},
			want:  true,
		},
		{
			name:  "the refusal: an unknown owner older than the stale bound is gone",
			lease: JobLease{Known: false, Beat: stale},
			want:  false,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, row.lease.Live(now), "Live at %s for %s", now, row.lease)
		})
	}
}

// TestLeaseCoverStartSlotLeaseTakesAndRefuses reaches StartSlotLease on its
// main path, where the slot directory is free and the release removes the
// lease, and on its refusal, where a second run in the same slot directory is
// turned away by name. The barrier is the lease file itself, so nothing here
// waits on a clock.
func TestLeaseCoverStartSlotLeaseTakesAndRefuses(t *testing.T) {
	t.Parallel()

	slot := t.TempDir()
	release, err := StartSlotLease(slot, "card-1")
	require.NoError(t, err, "the free slot lease was refused: %v", err)
	raw, err := os.ReadFile(filepath.Join(slot, SlotLeaseName))
	require.NoError(t, err, "StartSlotLease took no lease: %v", err)
	assert.Contains(t, string(raw), "label=card-1\n", "the slot lease does not name the card:\n%s", raw)

	second, err := StartSlotLease(slot, "card-2")
	if err == nil {
		second()
	}
	require.Error(t, err, "a second run took a slot directory a live run holds")
	var held *JobLeaseHeldError
	require.ErrorAs(t, err, &held, "the refusal is %v (%T), want a *JobLeaseHeldError naming the holder", err, err)
	assert.Contains(t, err.Error(), "card-1", "the refusal does not name the holder's card: %v", err)

	release()
	_, err = os.Lstat(filepath.Join(slot, SlotLeaseName))
	require.True(t, os.IsNotExist(err), "the released slot lease outlived the run: %v", err)
}
