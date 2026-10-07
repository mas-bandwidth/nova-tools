package sprint

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAFullVolumeIsAJudgmentAndStartsNoLane is the volume watermark: a full
// volume is one judgment and the deal starts no lane on that machine, and a
// tick with no reading judges nothing.
func TestAFullVolumeIsAJudgmentAndStartsNoLane(t *testing.T) {
	t.Parallel()
	vol := "ai"
	full := DiskFullDefault + 1
	under := DiskWarnDefault - 1

	t.Run("no reading judges nothing and the deal starts a lane", func(t *testing.T) {
		w := setup(t, 1)
		p := diskPlan(w.s, TickReq{})
		require.Empty(t, p.Notes, "a tick with no reading raises no volume judgment")
		require.Empty(t, p.Units)
		w.must(Deal(w.s, DealReq{}))
		require.Equal(t, 1, w.s.Fleet.Count("m1", Ready)+w.s.Fleet.Count("m2", Ready), "the deal still starts a lane")
	})

	t.Run("a full volume holds the machine and the deal skips it", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, full, "jobs,reads")
		w.must(diskPlan(w.s, TickReq{}))
		require.Len(t, w.notesOf(NVolumeWatermark), 1)
		require.Len(t, w.openOn(VolumeSubject(vol)), 1)
		what := w.notesOf(NVolumeWatermark)[0].What
		require.Contains(t, what, "volume "+vol)
		require.Contains(t, what, "machine m1")
		require.Contains(t, what, "jobs")
		require.Contains(t, what, "reads")
		require.Contains(t, what, "the full line")
		require.Contains(t, what, "no new lane")
		ctl := w.s.MemberCtl("m1")
		require.Equal(t, Held, ctl.F("status"))
		require.Equal(t, DiskHoldReason(vol), ctl.F(FieldHeldReason))
		require.NotContains(t, w.s.UpMembers(), "m1")
		require.Contains(t, w.s.UpMembers(), "m2")
		line := DiskHeadline(w.s)
		require.Contains(t, line, DiskHoldReason(vol))
		require.Contains(t, line, "held")
		require.Contains(t, DiskFigure(ctl), "headroom")
		w.must(Deal(w.s, DealReq{}))
		require.Equal(t, 0, w.s.Fleet.Count("m1", Ready), "no new lane on the full machine")
		require.Equal(t, 1, w.s.Fleet.Count("m2", Ready), "the other machine still takes the lane")
		again := diskPlan(w.s, TickReq{})
		require.Empty(t, again.Notes, "the same reading is not a second judgment")
		require.Empty(t, again.Units, "the hold is not written again")
		require.Empty(t, again.Updates)
	})

	t.Run("two machines one volume are one judgment and neither starts a lane", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, full, "jobs")
		setVolume(w, "m2", vol, full, "reads")
		w.must(diskPlan(w.s, TickReq{}))
		require.Len(t, w.notesOf(NVolumeWatermark), 1)
		require.Len(t, w.openOn(VolumeSubject(vol)), 1)
		what := w.notesOf(NVolumeWatermark)[0].What
		require.Contains(t, what, "machine m1")
		require.Contains(t, what, "machine m2")
		require.Equal(t, Held, w.s.MemberCtl("m1").F("status"))
		require.Equal(t, Held, w.s.MemberCtl("m2").F("status"))
		p := Deal(w.s, DealReq{})
		require.False(t, planLandsOn(p, "m1") || planLandsOn(p, "m2"), "no new lane on either machine")
	})

	t.Run("the warn line judges and does not hold", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, DiskWarnDefault, "jobs")
		w.must(diskPlan(w.s, TickReq{}))
		require.Len(t, w.notesOf(NVolumeWatermark), 1)
		require.Contains(t, w.notesOf(NVolumeWatermark)[0].What, "the warn line")
		require.NotContains(t, w.notesOf(NVolumeWatermark)[0].What, "the full line")
		require.Equal(t, Up, w.s.MemberCtl("m1").F("status"))
		require.Contains(t, w.s.UpMembers(), "m1")
	})

	t.Run("the full line itself does not hold", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, DiskFullDefault, "jobs")
		w.must(diskPlan(w.s, TickReq{}))
		require.Len(t, w.openOn(VolumeSubject(vol)), 1)
		require.Equal(t, Up, w.s.MemberCtl("m1").F("status"))
		require.Empty(t, w.s.MemberCtl("m1").F(FieldHeldReason))
	})

	t.Run("inode use above the full line holds", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, under, "jobs")
		w.s.MemberCtl("m1").Fields[FieldDiskInode] = strconv.Itoa(full)
		w.must(diskPlan(w.s, TickReq{}))
		require.Equal(t, Held, w.s.MemberCtl("m1").F("status"))
		require.Contains(t, w.notesOf(NVolumeWatermark)[0].What, "the full line")
	})

	t.Run("under the warn line there is no judgment", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, under, "jobs")
		p := diskPlan(w.s, TickReq{})
		require.Empty(t, p.Notes)
		require.Empty(t, p.Units)
		require.Contains(t, w.s.UpMembers(), "m1")
	})

	t.Run("a fresh reading under the warn line closes the judgment and releases the hold", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, full, "jobs")
		w.must(diskPlan(w.s, TickReq{}))
		require.Equal(t, Held, w.s.MemberCtl("m1").F("status"))
		setVolume(w, "m1", vol, under, "jobs")
		w.must(diskPlan(w.s, TickReq{}))
		require.Empty(t, w.openOn(VolumeSubject(vol)))
		require.Equal(t, Up, w.s.MemberCtl("m1").F("status"))
		require.Empty(t, w.s.MemberCtl("m1").F(FieldHeldReason))
		require.Contains(t, w.s.UpMembers(), "m1")
	})

	t.Run("the judgment is raised again after the reraise interval", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, full, "jobs")
		w.must(diskPlan(w.s, TickReq{}))
		w.tick(DiskReraise - time.Second)
		early := diskPlan(w.s, TickReq{})
		require.Empty(t, notesOfType(early, NRaisedAgain))
		w.tick(time.Second)
		again := diskPlan(w.s, TickReq{})
		require.Len(t, notesOfType(again, NRaisedAgain), 1)
		require.NotEmpty(t, again.Updates)
		require.GreaterOrEqual(t, again.Updates[0].Before, 1)
	})

	t.Run("a stale reading does not clear the hold", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, full, "jobs")
		w.must(diskPlan(w.s, TickReq{}))
		w.s.MemberCtl("m1").Fields[FieldDiskAt] = stamp(w.s.Now.Add(-time.Hour))
		p := diskPlan(w.s, TickReq{})
		require.Empty(t, p.Units)
		require.Equal(t, Held, w.s.MemberCtl("m1").F("status"))
	})

	t.Run("work properties override the default lines", func(t *testing.T) {
		w := setup(t, 1)
		setVolume(w, "m1", vol, full, "jobs")
		props := w.s.Work.Props()
		props[PropDiskFull] = strconv.Itoa(full)
		w.s.Work.SetProps(props)
		w.must(diskPlan(w.s, TickReq{}))
		require.Equal(t, Up, w.s.MemberCtl("m1").F("status"), "the full line moved to the reading, so it warns and does not hold")
		require.Len(t, w.openOn(VolumeSubject(vol)), 1)
		setVolume(w, "m1", vol, full, "jobs")
		props = w.s.Work.Props()
		props[PropDiskWarn] = strconv.Itoa(full + 1)
		w.s.Work.SetProps(props)
		w.must(diskPlan(w.s, TickReq{}))
		require.Empty(t, w.openOn(VolumeSubject(vol)))
	})

	t.Run("a friend above the full line is not dealt and is not read", func(t *testing.T) {
		w := setup(t, 1)
		above := FriendSeat{Name: "amy", Status: Up, Width: 1, DiskVolume: vol, DiskUse: full, DiskDirs: "jobs"}
		below := FriendSeat{Name: "bea", Status: Up, Width: 1, DiskVolume: vol, DiskUse: under}
		require.False(t, friendCanRead(w.s, above))
		require.False(t, friendDealable(w.s, above))
		require.True(t, friendCanRead(w.s, below))
		require.True(t, friendDealable(w.s, below))
		w.must(diskPlan(w.s, TickReq{Friends: []FriendSeat{above}}))
		require.Len(t, w.notesOf(NVolumeWatermark), 1)
		require.Contains(t, w.notesOf(NVolumeWatermark)[0].What, "friend amy")
		require.Contains(t, w.notesOf(NVolumeWatermark)[0].What, "jobs")
	})

	t.Run("the disk part is the one before the deal", func(t *testing.T) {
		var names []string
		for _, p := range TickTables[0].Parts {
			names = append(names, p.Name)
		}
		deal := slices.Index(names, "deal")
		require.Greater(t, deal, 0)
		require.Equal(t, PartDisk, names[deal-1])
		rebalance := slices.Index(names, PartRebalance)
		require.Equal(t, "deal", names[rebalance-1])
		var frozen []string
		for _, p := range TickParts {
			frozen = append(frozen, p.Name)
		}
		require.Less(t, slices.Index(frozen, PartDisk), slices.Index(frozen, "deal"))
	})

	t.Run("a scan names the largest directory and a fake stat is the reading", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, "big"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "big", "f"), make([]byte, 64), 0o644))
		require.NoError(t, os.Mkdir(filepath.Join(root, "small"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "small", "f"), []byte("x"), 0o644))
		dirs, err := ScanLargest(root, 0)
		require.NoError(t, err)
		require.NotEmpty(t, dirs)
		require.Equal(t, "big", dirs[0])

		prev := VolumeStatOf
		t.Cleanup(func() { VolumeStatOf = prev })
		VolumeStatOf = func(string) (VolumeStat, error) {
			return VolumeStat{Bsize: 1, Blocks: 100, Bavail: 4, Files: 100, Ffree: 10}, nil
		}
		rd, err := MeasureVolume("/vol")
		require.NoError(t, err)
		require.Equal(t, full, rd.Use)
		require.Equal(t, DiskWarnDefault+10, rd.Inode)
		require.True(t, pastFull(rd.Level(), DiskFullDefault))
		got, ok := DiskOf(&Card{Fields: rd.Fields()}, t0)
		require.True(t, ok)
		require.Equal(t, rd.Use, got.Use)
		require.Equal(t, "/vol", got.Volume)
	})
}

// diskPlan is TickDisk's plan. The part also returns how many moves are due
// past its bound; this test keeps the plan.
func diskPlan(s *Snapshot, r TickReq) Plan {
	p, _ := TickDisk(s, r)
	return p
}

func setVolume(w *world, member, vol string, use int, dirs string) {
	w.t.Helper()
	ctl := w.s.MemberCtl(member)
	require.NotNil(w.t, ctl)
	if ctl.Fields == nil {
		ctl.Fields = map[string]string{}
	}
	ctl.Fields[FieldDiskVolume] = vol
	ctl.Fields[FieldDiskUse] = strconv.Itoa(use)
	ctl.Fields[FieldDiskInode] = strconv.Itoa(use)
	ctl.Fields[FieldDiskDirs] = dirs
	ctl.Fields[FieldDiskFree] = "headroom"
	ctl.Fields[FieldDiskInodeFree] = "inode-headroom"
	delete(ctl.Fields, FieldDiskAt)
}

func planLandsOn(p Plan, member string) bool {
	for _, u := range p.Units {
		for _, c := range u.Changes {
			if c.Table != Fleet {
				continue
			}
			if c.Entry.Create != nil && c.Entry.Create.Row == member {
				return true
			}
			if c.Entry.Move != nil && c.Entry.Move.Row == member {
				return true
			}
		}
	}
	return false
}

func notesOfType(p Plan, typ string) []Note {
	var out []Note
	for _, n := range p.Notes {
		if n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}
