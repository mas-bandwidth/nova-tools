package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The tick runs gc on every machine once an hour, and as soon as a volume passes 80%, at
// most once every ten minutes while it stays there.
func TestTheTickRunsGcHourlyAndOnAFullVolume(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	ms := []GCMachine{
		{Name: "new", Volume: -1},
		{Name: "hour", Last: now.Add(-GCEvery), Volume: 40},
		{Name: "quiet", Last: now.Add(-30 * time.Minute), Volume: 79},
		{Name: "full", Last: now.Add(-GCVolumeRetry), Volume: 80},
		{Name: "full-just-ran", Last: now.Add(-time.Minute), Volume: 97},
	}
	assert.Equal(t, []GCRun{{"new", "first"}, {"hour", "hourly"}, {"full", "volume 80% >= 80%"}}, GCDue(ms, now))
}

func TestGcVolumeIsReadFromTheSummaryOrDf(t *testing.T) {
	t.Parallel()
	n, ok := ParseGCVolume("GC jobs count=1 bytes=2 kept=0 refused=0 failed=0\nGC OK freed=2 volume=83%\n")
	assert.True(t, ok)
	assert.Equal(t, 83, n)
	n, ok = ParseGCVolume("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 100 61 39 61% /\n/dev/sdb1 100 91 9 91% /home\n")
	assert.True(t, ok)
	assert.Equal(t, 91, n)
	_, ok = ParseGCVolume("ssh: connect to host x: refused\n")
	assert.False(t, ok)
}
