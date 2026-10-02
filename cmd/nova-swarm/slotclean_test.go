package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// The slot's life (slotclean.go; tla/MemberSlot.tla): a launch the sprint has taken the word
// of is retired, apart from the pass: its checkout, tmp, data and results go, and what a
// later look needs (native.log, card.md, frame.json, RESULT.md) sits under <slots>/done/<launch>
// for a day. A report the sprint refused keeps its slot; the sweep retires what the queue no
// longer holds and nothing runs; the cap evicts the oldest done entries first and never a
// working slot, and refuses takes while working slots alone are over it. Every clock here is
// handed in: nothing waits.

// pool is a temporary pool: its slots directory, its results, and a runner over them.
type pool struct {
	t       *testing.T
	slots   string
	results string
	r       *nativeRunner
	errb    *bytes.Buffer
}

func newPool(t *testing.T) *pool {
	t.Helper()
	root := t.TempDir()
	p := &pool{t: t, slots: filepath.Join(root, "slots"), results: filepath.Join(root, "results"), errb: &bytes.Buffer{}}
	require.NoError(t, os.MkdirAll(p.slots, 0o755))
	require.NoError(t, os.MkdirAll(p.results, 0o755))
	p.r = &nativeRunner{root: root, slots: p.slots, resultsRoot: p.results, stderr: p.errb}
	// what a test keeps read-only is made writable again before the temp dir is removed
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.Type()&fs.ModeSymlink == 0 {
				_ = os.Chmod(path, 0o755) // ignored: best effort; the temp dir's own removal reports what is left
			}
			return nil
		})
	})
	return p
}

// launch stages a fake launch of card at gen as the member would leave it: the directory
// with a checkout holding a module cache as Go leaves one (directories 0555, files 0444),
// its tmp and data, the small files beside it, a pid file naming no process, and a result
// under the results root; its activity is at.
func (p *pool) launch(card string, gen int, at time.Time) string {
	p.t.Helper()
	return p.stage(member.Packet{Card: card, Kind: "work", Gen: gen, Epoch: 1}, at)
}

func (p *pool) stage(pk member.Packet, at time.Time) string {
	p.t.Helper()
	name := launchName(pk)
	write(p.t, filepath.Join(p.slots, name, "jobs", pk.Card, "repo", "main.go"), "package main\n")
	write(p.t, filepath.Join(p.slots, name, "tmp", pk.Card, "scratch"), "tmp\n")
	write(p.t, filepath.Join(p.slots, name, "data", "harness.db"), "state\n")
	mod := filepath.Join(p.slots, name, "jobs", pk.Card, "gomodcache", "example.com", "mod@v1.0.0")
	write(p.t, filepath.Join(mod, "mod.go"), "package mod\n")
	require.NoError(p.t, os.Chmod(filepath.Join(mod, "mod.go"), 0o444))
	for d := mod; d != filepath.Join(p.slots, name, "jobs", pk.Card); d = filepath.Dir(d) {
		require.NoError(p.t, os.Chmod(d, 0o555))
	}
	for _, sib := range []string{".native.log", ".card.md", ".frame.json"} {
		write(p.t, filepath.Join(p.slots, name+sib), "kept "+sib+"\n")
	}
	write(p.t, filepath.Join(p.slots, name+".pid"), "999999999\n")
	write(p.t, filepath.Join(p.results, name, pk.Card, "run1", "attempt-1", "RESULT.md"), "verdict: ok\n")
	p.touch(name, at)
	return name
}

// touch sets a launch's last activity: its directory's and its native log's times.
func (p *pool) touch(name string, at time.Time) {
	p.t.Helper()
	for _, path := range []string{filepath.Join(p.slots, name), filepath.Join(p.slots, name+".native.log")} {
		require.NoError(p.t, os.Chtimes(path, at, at))
	}
}

func (p *pool) exists(name string) bool {
	_, err := os.Lstat(filepath.Join(p.slots, name))
	return err == nil
}

// done is the done entry's path for a launch.
func (p *pool) done(name string) string { return filepath.Join(p.slots, doneDir, name) }

// retired stages a launch, ends it accepted and sets its done entry's time to at.
func (p *pool) retired(card string, at time.Time) string {
	p.t.Helper()
	name := p.launch(card, 1, at)
	p.r.started(name)
	p.r.Ended(member.Packet{Card: card, Kind: "work", Gen: 1, Epoch: 1}, true)
	require.DirExists(p.t, p.done(name))
	require.NoError(p.t, os.Chtimes(p.done(name), at, at))
	return name
}

// TestAnAcceptedLaunchIsRetiredToDone pins the end of an accepted launch: its directory (a
// read-only module cache and all), its pid file and its results are gone; its log, card,
// frame and RESULT.md sit under done/<launch>, a few KB where the slot was; one line says so.
func TestAnAcceptedLaunchIsRetiredToDone(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	name := p.launch("c1", 1, time.Now())
	before := treeSize(p.slots) + treeSize(p.results)
	p.r.started(name)
	p.r.Ended(member.Packet{Card: "c1", Kind: "work", Gen: 1, Epoch: 1}, true)
	assert.False(t, p.exists(name), "the launch's directory is removed")
	assert.NoFileExists(t, filepath.Join(p.slots, name+".pid"))
	assert.NoDirExists(t, filepath.Join(p.results, name), "the results went with it")
	for _, kept := range []string{"native.log", "card.md", "frame.json"} {
		b, err := os.ReadFile(filepath.Join(p.done(name), kept))
		require.NoError(t, err)
		assert.Equal(t, "kept ."+kept+"\n", string(b))
	}
	b, err := os.ReadFile(filepath.Join(p.done(name), "RESULT.md"))
	require.NoError(t, err)
	assert.Equal(t, "verdict: ok\n", string(b))
	entries, err := os.ReadDir(p.slots)
	require.NoError(t, err)
	require.Len(t, entries, 1, "nothing of the launch is left beside done/")
	assert.Equal(t, doneDir, entries[0].Name())
	after := treeSize(p.slots) + treeSize(p.results)
	t.Logf("a slot leaves behind %d B before and %d B after retirement", before, after)
	assert.Less(t, after, int64(64<<10), "what is kept is a few KB")
	assert.Regexp(t, `^CLEAN retired `+name+`: [0-9.]+ (B|KiB|MiB|GiB) freed; done/`+name+` kept 24h0m0s\n$`, p.errb.String())
	assert.Empty(t, p.r.live)
}

// TestARefusedReportKeepsTheSlotAndSaysSo pins the other end: the sprint refused the finish,
// so nothing of the launch is removed, and one NOTE says it is kept for the sweep.
func TestARefusedReportKeepsTheSlotAndSaysSo(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	name := p.launch("c1", 1, time.Now())
	p.r.started(name)
	p.r.Ended(member.Packet{Card: "c1", Kind: "work", Primary: "p-c1", Gen: 1, Epoch: 1}, false)
	assert.True(t, p.exists(name), "the slot is kept")
	assert.FileExists(t, filepath.Join(p.slots, name+".native.log"))
	assert.DirExists(t, filepath.Join(p.results, name))
	assert.NoDirExists(t, p.done(name))
	assert.Equal(t, "nova-swarm member: NOTE launch "+name+" kept: the sprint refused its report; the sweep retires it once the queue no longer holds its card; run: nova-sprint card p-c1\n", p.errb.String())
	assert.Empty(t, p.r.live, "the name is no longer claimed")
}

// TestTheSweepRetiresWhatTheQueueNoLongerHoldsAndNothingElse pins the sweep over a pool a
// crash or a refusal left: once a pass has told the runner the cards the queue holds, a
// launch of this loop's kind the queue does not hold, that nothing runs (no claim of ours, no
// live pid) and that has been still for leftoverIdle is retired; a held one, a claimed one,
// one whose pid is alive, one with recent activity, the other loop's kind, a directory that
// is not a launch's, a file and a link are never touched, and a link's target is never
// reached. Before the first queue the sweep does nothing.
func TestTheSweepRetiresWhatTheQueueNoLongerHoldsAndNothingElse(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	now := time.Now()
	old := now.Add(-time.Hour)
	landed := p.launch("landed", 1, old)
	refused := p.launch("refused", 1, old)
	p.r.started(refused)
	p.r.Ended(member.Packet{Card: "refused", Kind: "work", Gen: 1, Epoch: 1}, false)
	held := p.launch("held", 1, old)
	ours := p.launch("ours", 1, old)
	p.r.started(ours)
	running := p.launch("running", 1, old)
	write(t, filepath.Join(p.slots, running+".pid"), strconv.Itoa(os.Getpid())+"\n")
	recent := p.launch("recent", 1, now)
	read := p.stage(member.Packet{Card: "r1", Kind: "read", Attempt: 1, Epoch: 1}, old)
	notALaunch := filepath.Join(p.slots, "cache")
	require.NoError(t, os.MkdirAll(notALaunch, 0o755))
	require.NoError(t, os.Chtimes(notALaunch, old, old))
	write(t, filepath.Join(p.slots, "file.g1.e1"), "a file named like a launch\n")
	outside := t.TempDir()
	write(t, filepath.Join(outside, "precious"), "never removed\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(p.slots, "link.g1.e1")))

	assert.Zero(t, p.r.sweep(now), "no queue has been read: nothing is judged")
	p.r.Holds([]member.Packet{{Card: "held", Kind: "work", Gen: 1, Epoch: 1}})
	assert.Equal(t, 2, p.r.sweep(now))
	for _, name := range []string{landed, refused} {
		assert.False(t, p.exists(name), "%s: not in the queue, nothing runs it: retired", name)
		assert.FileExists(t, filepath.Join(p.done(name), "native.log"))
	}
	for _, name := range []string{held, ours, running, recent, read, "cache", "file.g1.e1", "link.g1.e1"} {
		assert.True(t, p.exists(name), "%s is never removed", name)
	}
	assert.FileExists(t, filepath.Join(outside, "precious"), "a link's target is never reached")
	assert.Zero(t, p.r.sweep(now), "a second round finds nothing")
	// a reader's sweep is over its own kind
	p.r.reader = true
	p.r.Holds(nil)
	assert.Equal(t, 1, p.r.sweep(now))
	assert.False(t, p.exists(read))
	assert.True(t, p.exists(held), "a reader never judges a work launch")
}

// TestDoneEntriesGoAfterADay pins the day: a done entry retired doneKeep ago is removed by
// the lazy round, a fresher one stays; nothing but a done entry is touched.
func TestDoneEntriesGoAfterADay(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	now := time.Now()
	old := p.retired("old", now.Add(-doneKeep-time.Minute))
	fresh := p.retired("fresh", now.Add(-doneKeep+time.Minute))
	write(t, filepath.Join(p.slots, doneDir, "notes.txt"), "a person's file\n")
	p.errb.Reset()
	p.r.lazy(now)
	assert.NoDirExists(t, p.done(old))
	assert.DirExists(t, p.done(fresh))
	assert.FileExists(t, filepath.Join(p.slots, doneDir, "notes.txt"))
	assert.Regexp(t, `^CLEAN done: removed 1 entries kept past 24h0m0s, [0-9.]+ (B|KiB|MiB|GiB) freed\n$`, p.errb.String())
	p.errb.Reset()
	p.r.lazy(now)
	assert.Empty(t, p.errb.String(), "a round that removes nothing says nothing")
}

// TestTheCapEvictsTheOldestDoneFirstAndNeverAWorkingSlot pins the cap: over it, the oldest
// done entries go first, one at a time until under; a working slot is never removed, and
// while working slots alone are over the cap the member's room says no and why; under it
// again, the room says yes. The measure is the cleaner's, once a capEvery.
func TestTheCapEvictsTheOldestDoneFirstAndNeverAWorkingSlot(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	now := time.Now()
	working := p.launch("working", 1, now)
	p.r.started(working)
	write(t, filepath.Join(p.slots, working, "jobs", "working", "big"), string(make([]byte, 3000)))
	var done []string
	for i := range 3 {
		name := p.retired("d"+strconv.Itoa(i), now.Add(time.Duration(i-3)*time.Hour))
		write(t, filepath.Join(p.done(name), "pad"), string(make([]byte, 1000-treeSize(p.done(name)))))
		done = append(done, name)
	}
	base := treeSize(p.slots) - 3000 - 3000 // the working slot's own files and the done entries
	p.r.cap = base + 4500
	p.errb.Reset()
	p.r.capRound(now)
	assert.NoDirExists(t, p.done(done[0]), "the oldest done entry goes first")
	assert.NoDirExists(t, p.done(done[1]), "then the next, until under")
	assert.DirExists(t, p.done(done[2]))
	assert.True(t, p.exists(working))
	ok, why := p.r.slotsRoom()
	assert.True(t, ok, why)
	assert.Contains(t, why, "under its cap")
	assert.Regexp(t, `^CLEAN cap: the slots directory holds [0-9.]+ (B|KiB|MiB|GiB) against its cap of [0-9.]+ (B|KiB|MiB|GiB); removed 2 done entries, [0-9.]+ (B|KiB|MiB|GiB) freed; under\n$`, p.errb.String())

	p.r.cap = base + 2000
	p.errb.Reset()
	p.r.capRound(now.Add(capEvery))
	assert.NoDirExists(t, p.done(done[2]), "the last done entry goes")
	assert.True(t, p.exists(working), "a working slot is never removed, cap or no cap")
	ok, why = p.r.slotsRoom()
	assert.False(t, ok)
	assert.Contains(t, why, "over its cap of")
	assert.Contains(t, why, "what is left is working slots, which are never removed; no card is started until they end and are retired")
	assert.Contains(t, p.errb.String(), "; full\n")

	p.errb.Reset()
	p.r.capRound(now.Add(capEvery + time.Second))
	assert.Empty(t, p.errb.String(), "a measure is made once a capEvery")
	p.r.Ended(member.Packet{Card: "working", Kind: "work", Gen: 1, Epoch: 1}, true)
	p.r.cap = base + 4500
	p.r.capRound(now.Add(3 * capEvery))
	ok, _ = p.r.slotsRoom()
	assert.True(t, ok, "under the cap once the working slot was retired")
}

// TestTheCapDefaultsToATenthOfTheVolume pins the flag's default and its refusal: 0 is a tenth
// of the volume the slots sit on, n is n GiB, a negative is refused naming what it wants.
func TestTheCapDefaultsToATenthOfTheVolume(t *testing.T) {
	t.Parallel()
	assert.Equal(t, int64(100*gib), slotsCap(0, 1000*gib))
	assert.Equal(t, int64(5*gib), slotsCap(5, 1000*gib))
	total, err := diskSize(t.TempDir())
	require.NoError(t, err)
	assert.Positive(t, total, "the real volume answers")
	var out, errb bytes.Buffer
	code := run(append(memberFull(t.TempDir()), "--slots-max-gb", "-1"), bytes.NewReader(nil), &out, &errb, time.Now())
	require.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--slots-max-gb is the GiB the slots directory may hold: 0 or more (0 is a tenth of its volume)")
}

// TestThePassNeverWaitsOnTheCleaner pins the separation: telling the queue's cards and ending
// a launch return at once while the cleaner holds a removal; a launch ended with a cleaner is
// tagged, not retired inline; and the lazy work yields while a tagged launch waits.
func TestThePassNeverWaitsOnTheCleaner(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	now := time.Now()
	old := p.retired("old", now.Add(-2*doneKeep))
	name := p.launch("c1", 1, now)
	p.r.started(name)
	p.r.tagged = make(chan string, cleanQueue) // a cleaner whose goroutine this test plays

	p.r.removing.Lock() // the cleaner mid-removal
	p.r.Holds([]member.Packet{{Card: "c1", Kind: "work", Gen: 1, Epoch: 1}})
	p.r.Ended(member.Packet{Card: "c1", Kind: "work", Gen: 1, Epoch: 1}, true)
	p.r.removing.Unlock()

	assert.True(t, p.exists(name), "the ended launch is tagged, not retired in the pass")
	p.r.lazy(now)
	assert.DirExists(t, p.done(old), "lazy work yields to a tagged launch")

	p.r.retire(<-p.r.tagged, now)
	assert.False(t, p.exists(name))
	p.r.lazy(now)
	assert.NoDirExists(t, p.done(old), "with nothing tagged, the lazy round runs")
}

// TestRemoveLaunchRefusesAnythingButALaunchDirectory pins the removal by name: a name that is
// not a launch's, a path that leaves the slots, a link and a file are refused, and nothing
// is removed.
func TestRemoveLaunchRefusesAnythingButALaunchDirectory(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "precious"), "never removed\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(p.slots, "link.g1.e1")))
	write(t, filepath.Join(p.slots, "file.g1.e1"), "a file\n")
	require.NoError(t, os.MkdirAll(filepath.Join(p.slots, "cache"), 0o755))
	for _, name := range []string{"cache", "../results", "..", "", "link.g1.e1", "file.g1.e1", "x/y.g1.e1", doneDir} {
		assert.Error(t, p.r.removeLaunch(name), "%q is refused", name)
	}
	assert.True(t, p.exists("link.g1.e1"))
	assert.True(t, p.exists("cache"))
	assert.FileExists(t, filepath.Join(outside, "precious"))
	assert.DirExists(t, p.results)
	assert.NoError(t, p.r.removeLaunch("gone.g1.e1"), "a launch already gone is nothing to remove")
}

// TestTheDiskFloorRefusesToStartAndSaysWhy pins the member's Room: under the floor, or a
// volume that cannot be read, starts nothing and says what was found and what to do; the
// rooms are asked in turn, the first no is the answer.
func TestTheDiskFloorRefusesToStartAndSaysWhy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		free uint64
		err  error
		ok   bool
		says string
	}{
		{"above the floor", 120 * gib, nil, true, "is 120.0 GiB, above the floor of 100 GiB"},
		{"at the floor", 100 * gib, nil, true, "above the floor"},
		{"under the floor", 42 * gib, nil, false, "is 42.0 GiB, under the floor of 100 GiB; no card is started until it is above; run: free disk on that volume, or start the member with a lower --disk-floor"},
		{"unreadable", 0, errors.New("statfs: no such file"), false, "could not be read (statfs: no such file); no card is started until it can"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok, why := diskRoom("/pool/slots", 100, func(string) (uint64, error) { return tc.free, tc.err })()
			assert.Equal(t, tc.ok, ok)
			assert.Contains(t, why, tc.says)
			assert.Contains(t, why, "/pool/slots")
		})
	}
	free, err := diskFree(t.TempDir())
	require.NoError(t, err)
	assert.Positive(t, free, "the real volume answers")
	yes := func() (bool, string) { return true, "yes" }
	no := func() (bool, string) { return false, "no" }
	ok, why := rooms(yes, no)()
	assert.False(t, ok)
	assert.Equal(t, "no", why)
	ok, why = rooms(yes, yes)()
	assert.True(t, ok)
	assert.Equal(t, "yes", why)
}

// TestMemberRefusesANegativeDiskFloor pins the flag's refusal, saying what it wants.
func TestMemberRefusesANegativeDiskFloor(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := run(append(memberFull(t.TempDir()), "--disk-floor", "-1"), bytes.NewReader(nil), &out, &errb, time.Now())
	require.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--disk-floor is the free GiB the slots' volume must keep for the member to start a card: 0 or more")
}

// TestAFiveLevelReadOnlyModuleCacheIsRemoved pins the shape that held a recursive remove on
// its prompt on a fleet machine: a Go module cache five directories deep, every directory
// 0555 and every file in it 0444, under a launch the member is done with. It is removed
// whole, and nothing beside it is touched.
func TestAFiveLevelReadOnlyModuleCacheIsRemoved(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	name := p.launch("deep", 1, time.Now())
	top := filepath.Join(p.slots, name, "jobs", "deep", "pkg", "mod")
	var dirs []string
	d := top
	for i := range 5 {
		d = filepath.Join(d, "level"+strconv.Itoa(i))
		dirs = append(dirs, d)
		write(t, filepath.Join(d, "file.go"), "package level\n")
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		require.NoError(t, os.Chmod(filepath.Join(dirs[i], "file.go"), 0o444))
		require.NoError(t, os.Chmod(dirs[i], 0o555))
	}
	fi, err := os.Stat(dirs[4])
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o555), fi.Mode().Perm())
	p.r.started(name)
	p.r.Ended(member.Packet{Card: "deep", Kind: "work", Gen: 1, Epoch: 1}, true)
	assert.False(t, p.exists(name), "the read-only tree is removed whole")
	assert.FileExists(t, filepath.Join(p.done(name), "native.log"))
}
