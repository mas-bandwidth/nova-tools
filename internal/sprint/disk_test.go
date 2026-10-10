package sprint_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// fullDisk is a volume at 96% with 5% of its inodes free and one directory the
// bounded scan named, at the rig's clock.
func (r *alarmRig) fullDisk() *sprint.DiskReading {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	return &sprint.DiskReading{At: r.now, Volume: "/ai", Path: "/ai/working", Free: 4 << 30, Total: 100 << 30,
		InodesFree: 5, InodesTotal: 100,
		Top: []sprint.DiskDir{{Path: "/ai/working/jobs", Bytes: 40 << 30}, {Path: "/ai/working/repos", Bytes: 10 << 30}}}
}

// diskBeat is a beat of m1 carrying the disk reading, at the clock's time.
func (r *alarmRig) diskBeat(d *sprint.DiskReading) {
	r.t.Helper()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	_, err := r.st.BeatWithDisk(r.ctx, "m1", &zero, hostload.Source{}, d)
	require.NoError(r.t, err)
}

// diskTicks runs n ticks of the machine, the clock a second on before each, m1
// beating with the disk reading.
func (r *alarmRig) diskTicks(d *sprint.DiskReading, n int) {
	r.t.Helper()
	for range n {
		r.mu.Lock()
		r.now = r.now.Add(time.Second)
		r.mu.Unlock()
		r.diskBeat(d)
		_, err := r.st.Tick(r.ctx)
		require.NoError(r.t, err)
	}
}

// diskJudgments is every NDiskWatermark judgment the sprint wrote, in order.
func (r *alarmRig) diskJudgments() []sprint.Note {
	r.t.Helper()
	notes, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	var out []sprint.Note
	for _, n := range notes {
		if n.Kind == sprint.Judgment && n.Type == sprint.NDiskWatermark {
			out = append(out, n)
		}
	}
	return out
}

// A full volume is one judgment per volume, naming the volume, the machine and
// the largest directories under the AI root, and no new lane starts on that
// machine while the volume is over its stop line: the deal gives it nothing,
// the disk quiet says why, and the volume clearing lifts it. The judgment is
// re-raised every re-raise cadence while the volume holds.
func TestAFullVolumeIsAJudgmentAndStartsNoLane(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)

	// the full beat arrives: one judgment names the volume, the machine and the
	// largest directory, and the machine's disk quiet is written for the next deal
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	r.diskBeat(r.fullDisk())
	_, err = r.st.Tick(r.ctx)
	require.NoError(t, err)

	judged := r.diskJudgments()
	require.Len(t, judged, 1, "a full volume is one judgment")
	assert.Equal(t, []string{sprint.DiskSubject("/ai")}, judged[0].Subjects(), "the judgment is the volume's")
	assert.Contains(t, judged[0].What, "/ai", "the judgment names the volume")
	assert.Contains(t, judged[0].What, "m1", "the judgment names the machine on it")
	assert.Contains(t, judged[0].What, "96%", "the judgment names the use")
	assert.Contains(t, judged[0].What, "/ai/working/jobs", "the judgment names the largest directory the bounded scan found")
	assert.Contains(t, judged[0].What, "40.0 GiB", "the judgment names that directory's size")

	props := r.snap().Fleet.Props()
	assert.NotEmpty(t, props[sprint.DiskQuietPropPrefix+"m1"], "the disk quiet is set on the fleet table")
	lines := sprint.QuietLines(props, r.now)
	require.Len(t, lines, 1, "one quiet line for the one volume")
	assert.Contains(t, lines[0], "m1", "the readiness headline names the machine")
	assert.Contains(t, lines[0], "volume /ai is 96% full", "the readiness headline says why")

	// a ready card: the deal gives the disk-held machine no new lane, and the card
	// stays ready for a machine that can start it
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a"}}))
	r.diskTicks(r.fullDisk(), 1)
	s := r.snap()
	assert.Empty(t, s.Fleet.Column(sprint.Working), "no new lane starts on a machine over the stop line")
	assert.Empty(t, s.Fleet.Column(sprint.Ready), "the deal places no card on a machine over the stop line")
	assert.Len(t, s.Work.Column(sprint.Ready), 1, "the card waits ready for a machine that can start it")

	// the volume clears: the quiet is lifted and the card is dealt
	clear := *r.fullDisk()
	clear.Free, clear.Total = 40<<30, 100<<30 // 60% used
	r.diskTicks(&clear, 2)
	s = r.snap()
	assert.Empty(t, s.Fleet.Props()[sprint.DiskQuietPropPrefix+"m1"], "the quiet is lifted when the volume clears")
	assert.Len(t, s.Fleet.Column(sprint.Ready), 1, "the card is dealt once the volume clears")
}

// The volume's judgment is written once and re-raised every re-raise cadence
// while the volume holds, never every tick.
func TestTheDiskWatermarkReraisesOnItsCadence(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.diskTicks(r.fullDisk(), 2)
	assert.Len(t, r.diskJudgments(), 1, "one judgment while the cadence has not passed")

	// the cadence passes (the default 30m) with the volume still full: raised again
	r.mu.Lock()
	r.now = r.now.Add(sprint.DiskReraiseDefault)
	r.mu.Unlock()
	r.diskTicks(r.fullDisk(), 1)
	assert.Len(t, r.diskJudgments(), 2, "the judgment is raised again on the cadence")
}

// The thresholds are the work table's settings (PropDiskWarn, PropDiskStop,
// PropDiskReraise), each with its default; a snapshot without the properties
// reads the defaults.
func TestDiskThresholdsHaveDefaults(t *testing.T) {
	t.Parallel()
	s := &sprint.Snapshot{}
	assert.Equal(t, sprint.DiskWarnDefault, s.DiskWarn(), "the warn line's default")
	assert.Equal(t, sprint.DiskStopDefault, s.DiskStop(), "the stop line's default")
	assert.Equal(t, sprint.DiskReraiseDefault, s.DiskReraise(), "the re-raise cadence's default")

	d := sprint.DiskReading{At: time.Now(), Volume: "/ai", Path: "/ai", Free: 45 << 30, Total: 100 << 30}
	beat := sprint.Beat{At: time.Now(), Disk: &d}
	assert.NotEmpty(t, sprint.DiskHeld(beat, time.Now(), 50), "55% is over a stop line of 50")
	assert.Empty(t, sprint.DiskHeld(beat, time.Now(), 60), "55% is under a stop line of 60")
}
