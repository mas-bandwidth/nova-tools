package sprint

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gib = 1 << 30

// volumeAt is a reading of the studio's AI volume at used percent of its bytes, taken now,
// with the largest directories its scan found.
func volumeAt(now time.Time, used int) Disk {
	size := uint64(1000 * gib)
	return Disk{At: now, Host: "studio", Dir: "/Volumes/nova/ai/buds", Volume: "/Volumes/nova", Size: size, Free: size * uint64(100-used) / 100,
		Inodes: 10_000_000, InodesFree: 9_000_000, Root: "/Volumes/nova/ai",
		Top: []DirSize{{Path: "buds", Bytes: 600 * gib}, {Path: "cache", Bytes: 200 * gib, Partial: true}}}
}

// diskNotes is the volume judgments the world has written, and the cleared notes naming one.
func (w *world) diskNotes() (raised []Note, cleared int) {
	for _, n := range w.notes {
		switch {
		case n.Kind == Judgment && n.Type == NDiskAlarm:
			raised = append(raised, n)
		case n.Type == NAlarmCleared && strings.HasPrefix(n.What, NDiskAlarm+":"):
			cleared++
		}
	}
	return raised, cleared
}

// On 2026-10-06 the AI volume reached 100% with no warning: the first sign was the bus
// store refusing writes and lanes dying, the rows carried no disk figure, the tick raised
// no judgment of the volume and nothing refused a new lane on a full disk (docs/SPEC-SPRINT.md
// section 8, "Disk watermark"). Over the alarm (80% by default) the tick raises one
// judgment per volume, two names on one volume sharing it, naming the volume, the machine
// and the largest directories; its line follows the figure in place; it is raised again
// every 30 minutes while it holds; above the hold (95%) no new lane is dealt there and a
// ready card the fleet has no room for says why; and under the alarm it clears once. The
// thresholds are settings.
func TestAFullVolumeIsAJudgmentAndStartsNoLane(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 2}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2", Width: 2}))
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s1", Count: 12}))
	tick := func(d time.Duration) Plan {
		w.t.Helper()
		w.tick(d)
		p, _ := tickDisk(w.s, TickReq{})
		return w.must(p)
	}
	// under the alarm: nothing, and the load cell carries the figure
	w.s.Disks = map[string]Disk{"m1": volumeAt(w.s.Now, 70)}
	tick(time.Second)
	raised, _ := w.diskNotes()
	require.Empty(t, raised, "a volume at 70%% is under the alarm")
	b := Beat{At: w.s.Now, Load: 12.5}
	d := volumeAt(w.s.Now, 70)
	b.Disk = &d
	assert.Equal(t, "12.5% disk 70%", LoadText(b, w.s.Now), "the fleet row carries the volume's use")
	assert.Equal(t, "70% used, 300G free, 9.0M inodes free", DiskCell(b, w.s.Now), "the dashboard's figure")
	assert.Empty(t, DiskCell(b, w.s.Now.Add(BeatDeadline+time.Second)), "a stale beat carries no figure")

	// over the alarm: one judgment of the volume, m1 and the friend stella on it
	w.s.Disks = map[string]Disk{"m1": volumeAt(w.s.Now, 85), "stella": volumeAt(w.s.Now, 85), "m2": {At: w.s.Now, Host: "vision", Volume: "/", Size: 100 * gib, Free: 60 * gib}}
	tick(time.Second)
	raised, _ = w.diskNotes()
	require.Len(t, raised, 1, "one judgment per volume, however many work on it")
	j := raised[0]
	for _, want := range []string{"/Volumes/nova", "studio", "m1, stella", "85% used", "above the alarm of 80%", "buds 600G", "cache 200G+"} {
		assert.Contains(t, j.What, want, "the judgment names the volume, the machine, who is on it, its use and the largest directories")
	}
	assert.Equal(t, VolumeSubject("studio", "/Volumes/nova"), j.Stream)
	assert.Equal(t, []string{"act", "ack", "wait 30m"}, j.Decisions)

	// the use moves: the same judgment, its line updated in place, none written again
	w.s.Disks["m1"], w.s.Disks["stella"] = volumeAt(w.s.Now, 88), volumeAt(w.s.Now, 88)
	p := tick(time.Minute)
	raised, _ = w.diskNotes()
	require.Len(t, raised, 1, "a figure that moves is the same episode")
	require.Len(t, p.Updates, 1, "its line follows the figure")
	assert.Contains(t, p.Updates[0].What, "88% used")

	// still over 30 minutes later: closed and raised again, so it is pushed again
	tick(DiskReraise)
	raised, cleared := w.diskNotes()
	require.Len(t, raised, 2, "a volume over the alarm for 30 minutes is raised again")
	require.Zero(t, cleared, "a raise again is not a clear")
	require.Len(t, w.openOf(NDiskAlarm), 1, "one judgment open per volume")

	// above the hold: m1 starts no new lane; the deal gives every card to m2
	w.s.Disks["m1"] = volumeAt(w.s.Now, 97)
	assert.Contains(t, w.s.DiskFull("m1"), "above the hold of 95%")
	assert.Empty(t, w.s.DiskFull("m2"))
	dp, _ := TickDeal(w.s, TickReq{})
	w.must(dp)
	assert.Zero(t, w.s.Fleet.Count("m1", Ready)+w.s.Fleet.Count("m1", Working), "no card dealt to a member on a full volume")
	assert.Positive(t, w.s.Fleet.Count("m2", Ready), "the member with room takes the cards")
	m2 := w.s.Fleet.Count("m2", Ready)

	// every member up on a full volume: nothing dealt, the deal refuses saying why, and a
	// ready card is held by the volume's judgment
	w.s.Disks["m2"] = Disk{At: w.s.Now, Host: "vision", Volume: "/", Size: 100 * gib, Free: 1 * gib}
	tick(time.Second)
	dp, _ = TickDeal(w.s, TickReq{})
	w.must(dp)
	assert.Equal(t, m2, w.s.Fleet.Count("m2", Ready), "no new lane on any full volume")
	refused := Deal(w.s, DealReq{Sel: Sel{Limit: 1}})
	require.NotEmpty(t, refused.Refused, "the deal verb refuses too")
	assert.Contains(t, refused.Refused[0].Why, "above the hold")
	var readyID string
	for _, c := range w.s.Work.Column(Ready) {
		readyID = c.ID
		break
	}
	require.NotEmpty(t, readyID, "the fixture: a card left ready")
	hd := Holder(running(w), w.s.Now, readyID)
	assert.Equal(t, HeldByJudgment, hd.By, "%s", hd)
	assert.Contains(t, hd.Why, "no new lane on a full volume", "the held reason names the full volume")

	// under the alarm again: both volumes' judgments close, each with one cleared note
	w.s.Disks = map[string]Disk{"m1": volumeAt(w.s.Now, 40), "m2": {At: w.s.Now, Host: "vision", Volume: "/", Size: 100 * gib, Free: 90 * gib}}
	tick(time.Second)
	_, cleared = w.diskNotes()
	assert.Equal(t, 2, cleared, "each volume's episode ends with one cleared note")
	assert.Empty(t, w.openOf(NDiskAlarm))

	// the thresholds are settings: an alarm of 90% is quiet at 85%, a hold of 99% deals at 97%
	w.s.Work.props = map[string]string{PropDiskAlarm: "90", PropDiskHold: "99"}
	w.s.Disks = map[string]Disk{"m1": volumeAt(w.s.Now, 85)}
	before, _ := w.diskNotes()
	tick(time.Second)
	after, _ := w.diskNotes()
	assert.Len(t, after, len(before), "85%% is under an alarm set to 90%%")
	w.s.Disks["m1"] = volumeAt(w.s.Now, 97)
	assert.Empty(t, w.s.DiskFull("m1"), "97%% is under a hold set to 99%%")
	w.s.Work.props = map[string]string{PropDiskAlarm: "nope", PropDiskHold: "0"}
	alarm, hold := w.s.DiskBounds()
	assert.Equal(t, [2]int{DiskAlarmDefault, DiskHoldDefault}, [2]int{alarm, hold}, "a setting a threshold does not take is its default")
}

// openOf is the world's open judgments of the type.
func (w *world) openOf(typ string) []Open {
	var out []Open
	for _, o := range w.s.Open {
		if o.Note.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

// A volume's inodes run out before its bytes: its use is the larger, so the alarm and the
// hold see it.
func TestAVolumeOutOfInodesIsFull(t *testing.T) {
	t.Parallel()
	d := Disk{At: t0, Host: "h", Volume: "/v", Size: 100, Free: 90, Inodes: 1000, InodesFree: 10}
	assert.Equal(t, 10, d.BytesUsed())
	assert.Equal(t, 99, d.InodesUsed())
	assert.Equal(t, 99, d.Used())
	s := &Snapshot{Now: t0, Work: NewTable(Work), Disks: map[string]Disk{"m1": d}}
	assert.NotEmpty(t, s.DiskFull("m1"))
}

// The meter reads the volume's figures each beat and walks the AI root at most every
// DiskScanEvery, within its bound; the reading round-trips through friend beat --disk.
func TestTheDiskMeterScansTheRootBoundedAndRarely(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"buds/a/x":  {Data: make([]byte, 3000)},
		"buds/a/y":  {Data: make([]byte, 3000)},
		"cache/z":   {Data: make([]byte, 1000)},
		"small/one": {Data: make([]byte, 10)},
		"file":      {Data: make([]byte, 99999)},
	}
	stats := 0
	m := &DiskMeter{Host: "studio", Dir: "/ai/buds", Root: "/ai", FS: fsys,
		Stat: func(string) (DiskStat, error) {
			stats++
			return DiskStat{Volume: "/ai", Size: 1000, Free: 100, Inodes: 50, InodesFree: 5}, nil
		}}
	d, err := m.Measure(t0)
	require.NoError(t, err)
	require.Len(t, d.Top, 3)
	assert.Equal(t, DirSize{Path: "buds", Bytes: 6000}, d.Top[0], "the largest first, a file at the top is no directory")
	assert.Equal(t, "cache", d.Top[1].Path)
	assert.Equal(t, 90, d.Used())

	fsys["buds/b"] = &fstest.MapFile{Data: make([]byte, 50000)}
	d, err = m.Measure(t0.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(6000), d.Top[0].Bytes, "within DiskScanEvery the last scan stands")
	d, err = m.Measure(t0.Add(DiskScanEvery))
	require.NoError(t, err)
	assert.Equal(t, int64(56000), d.Top[0].Bytes, "after DiskScanEvery the root is walked again")
	assert.Equal(t, 3, stats, "the figures are read every beat")

	top, err := ScanTop(fsys, 3, DiskTop)
	require.NoError(t, err)
	partial := 0
	for _, ds := range top {
		if ds.Partial {
			partial++
		}
	}
	assert.Positive(t, partial, "a walk that reaches its bound marks what it did not finish: %+v", top)

	back, err := ParseDisk(m.Arg(t0.Add(DiskScanEvery)))
	require.NoError(t, err)
	assert.Equal(t, "studio", back.Host)
	assert.Equal(t, 90, back.Used())
	for _, bad := range []string{"", "{}", `{"at":"2030-01-02T03:04:05Z","host":"h","volume":"/","size":10,"free":11}`} {
		_, err := ParseDisk(bad)
		assert.Error(t, err, "ParseDisk(%q)", bad)
	}
}
