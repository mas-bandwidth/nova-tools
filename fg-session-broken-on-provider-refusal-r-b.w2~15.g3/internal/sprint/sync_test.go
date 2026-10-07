package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// devSyncWorld is a world with streams s1 and s2 merging, for the pure half of the dev sync
// (DevSyncDue, DevSynced; docs/SPEC-SPRINT.md, "Dev sync every cycle").
func devSyncWorld(t *testing.T) *world {
	w := newWorld(t)
	w.s.Work.SetRows([]string{"s1", "s2"})
	w.s.Merge.SetRows([]string{"s1", "s2"})
	for _, st := range []string{"s1", "s2"} {
		w.s.Merge.Put(&Card{ID: CtlID(st), Row: st, Col: Ctl, Rev: 1, Fields: map[string]string{"state": StreamMerging}})
	}
	return w
}

func TestADevSyncIsDueOnALandingSinceTheLastOrAfterHalfAnHour(t *testing.T) {
	t.Parallel()
	w := devSyncWorld(t)
	due, why := DevSyncDue(w.s)
	assert.True(t, due, "no sync recorded")
	assert.Equal(t, "no dev sync recorded", why)

	w.do(DevSynced(w.s, DevSyncFacts{Base: "b", Dev: "dev", MergeSha: "abc1234"}, nil))
	due, _ = DevSyncDue(w.s)
	assert.False(t, due, "in sync, nothing landed since")

	w.tick(time.Minute)
	w.s.Work.Put(&Card{ID: "s1-1", Row: "s1", Col: Landed, Rev: 1, Fields: map[string]string{"kind": "primary", "landed": stamp(w.s.Now)}})
	assert.Equal(t, 1, LandedSinceSync(w.s))
	due, why = DevSyncDue(w.s)
	assert.True(t, due)
	assert.Equal(t, "1 landed since the last dev sync", why)

	w.do(DevSynced(w.s, DevSyncFacts{Base: "b", Dev: "dev", MergeSha: "abc1234"}, nil))
	assert.Equal(t, 0, LandedSinceSync(w.s))
	due, _ = DevSyncDue(w.s)
	assert.False(t, due)
	w.tick(DevSyncAge)
	due, _ = DevSyncDue(w.s)
	assert.True(t, due, "dev moves on its own: due after DevSyncAge with nothing landed")
}

func TestADevSyncConflictStopsEveryStreamWithOneJudgment(t *testing.T) {
	t.Parallel()
	w := devSyncWorld(t)
	f := DevSyncFacts{Base: "b", Dev: "dev", Conflict: true, Files: []string{"a.go", "b.go"}, Drift: DevDrift{BaseLacks: 3, DevLacks: 2}}
	w.do(DevSynced(w.s, f, nil))
	for _, st := range []string{"s1", "s2"} {
		assert.Equal(t, StreamStopped, w.s.StreamCtl(st).F("state"))
		assert.Equal(t, DevSyncCause, w.s.StreamCtl(st).F("cause"))
		assert.Equal(t, "a.go,b.go", w.s.StreamCtl(st).F(FieldConflictPaths))
		assert.Equal(t, "3", w.s.StreamCtl(st).F(FieldDevSyncBaseLacks))
		assert.False(t, CanLand(w.s, st))
	}
	js := w.notesOf(NDevSyncConflict)
	require.Len(t, js, 1)
	assert.Contains(t, js[0].What, "a.go, b.go")

	// the next cycle conflicts again: still one judgment, its text brought up to date
	f.Files = append(f.Files, "c.go")
	p := w.do(DevSynced(w.s, f, nil))
	assert.Len(t, w.notesOf(NDevSyncConflict), 1)
	require.Len(t, p.Updates, 1)
	assert.Contains(t, p.Updates[0].What, "c.go")

	// the base takes dev cleanly: closed, both streams resume
	w.tick(time.Minute)
	w.do(DevSynced(w.s, DevSyncFacts{Base: "b", Dev: "dev", Synced: true, MergeSha: "def5678", Drift: DevDrift{BaseLacks: 3, DevLacks: 2}}, nil))
	assert.Empty(t, w.s.Open)
	for _, st := range []string{"s1", "s2"} {
		assert.Equal(t, StreamMerging, w.s.StreamCtl(st).F("state"))
		assert.Empty(t, w.s.StreamCtl(st).F("cause"))
		assert.True(t, CanLand(w.s, st))
	}
	d := DevDriftOf(w.s)
	assert.Equal(t, DevDrift{BaseLacks: 0, DevLacks: 3, LastSync: w.s.Now, LastSha: "def5678"}, d)
	w.tick(7 * time.Minute)
	assert.Equal(t, "base lacks 0, dev lacks 3 (7m since sync)", DevDriftOf(w.s).String())
	assert.Len(t, w.notesOf(NDevSynced), 1)
}

func TestAStreamStoppedForAnotherCauseStaysStopped(t *testing.T) {
	t.Parallel()
	w := devSyncWorld(t)
	ctl := w.s.StreamCtl("s2")
	ctl.Fields["state"], ctl.Fields["cause"] = StreamStopped, "conflict"
	w.do(DevSynced(w.s, DevSyncFacts{Base: "b", Dev: "dev", Conflict: true, Files: []string{"a.go"}}, nil))
	assert.Equal(t, "conflict", w.s.StreamCtl("s2").F("cause"))
	w.do(DevSynced(w.s, DevSyncFacts{Base: "b", Dev: "dev", Synced: true, MergeSha: "abc1234"}, nil))
	assert.Equal(t, StreamStopped, w.s.StreamCtl("s2").F("state"), "a clean sync resumes only what it stopped")
	assert.Equal(t, StreamMerging, w.s.StreamCtl("s1").F("state"))
}
