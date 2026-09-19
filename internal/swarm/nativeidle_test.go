package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A card that stopped making progress used to cost its WHOLE deadline before anybody
// looked: `js-under-20-bytes` held a bench slot for eighteen silent minutes, returned no
// RESULT.md, and the coordinator had nothing to read until the reap. `batch` has watched for
// this since issue #593; `native` -- the verb every card on every bench runs through -- had
// no watch at all.

// fakeSnap is one reading of a process tree, handed to the watch instead of the machine's
// own table so a still card and a working one are both expressible in a test.
type fakeSnap struct {
	cpu uint64
	ok  bool
}

func (f *fakeSnap) TreeCPU(int) (uint64, bool) { return f.cpu, f.ok }

// idleBench is one watch under an injected clock: a log file the test grows or does not, a
// ticker the test drives, and a tree whose CPU the test sets.
type idleBench struct {
	job   string
	log   string
	ticks chan time.Time
	snap  *fakeSnap
	out   <-chan IdleEnd
	stop  chan struct{}
}

func newIdleBench(t *testing.T, idle time.Duration, reader *WallReader) *idleBench {
	t.Helper()
	job := t.TempDir()
	b := &idleBench{
		job:   job,
		log:   filepath.Join(job, "harness-output.log"),
		ticks: make(chan time.Time),
		snap:  &fakeSnap{},
		stop:  make(chan struct{}),
	}
	if err := os.WriteFile(b.log, []byte("start\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := IdleWatch{Log: b.log, Job: job, Pid: 4242, Idle: idle, Reader: reader}
	w.now = func() time.Time { return time.Unix(0, 0) }
	w.ticker = func(time.Duration) (<-chan time.Time, func()) { return b.ticks, func() {} }
	w.snapshot = func() activitySnapshot { return b.snap }
	b.out = WatchIdle(w, b.stop)
	t.Cleanup(func() { close(b.stop) })
	return b
}

// tick drives the watch one poll at t seconds past the start and returns whether the watch
// ended the card at it.
func (b *idleBench) tick(t *testing.T, at time.Duration) (IdleEnd, bool) {
	t.Helper()
	now := time.Unix(0, 0).Add(at)
	select {
	case b.ticks <- now:
	case end, open := <-b.out:
		return end, open
	case <-time.After(60 * time.Second): // a generous bound on a goroutine taking a poll; the green path never waits
		t.Fatal("the watch took no poll")
	}
	// The poll is taken. Ask for a verdict, and if there is none the watch takes ANOTHER
	// poll at the same instant -- which changes nothing and is how the test waits for the
	// first one to be finished with rather than racing it.
	select {
	case end, open := <-b.out:
		return end, open
	case b.ticks <- now:
		return IdleEnd{}, false
	case <-time.After(60 * time.Second): // a generous bound; by here the watch has either ended the card or is polling
		t.Fatal("the watch neither ended the card nor took another poll")
	}
	return IdleEnd{}, false
}

// TestWatchIdleEndsAStillCardAtItsIdleWindowAndNotAtItsDeadline: the whole point. A log that
// does not grow and a tree that spends nothing ends the card at --idle.
func TestWatchIdleEndsAStillCardAtItsIdleWindowAndNotAtItsDeadline(t *testing.T) {
	b := newIdleBench(t, 10*time.Second, NewWallReader("c", nil))
	if _, ended := b.tick(t, 9*time.Second); ended {
		t.Fatal("a card still for less than the window is not idle yet")
	}
	end, ended := b.tick(t, 11*time.Second)
	if !ended {
		t.Fatal("a card whose log and tree have both been still for the whole window is ended")
	}
	if end.Idle < 10*time.Second {
		t.Fatalf("the end names how long the card was still, got %v", end.Idle)
	}
	if end.Refused {
		t.Fatalf("a card that simply stopped talking hit no wall, got %+v", end)
	}
}

// TestWatchIdleCarriesTheRefusalTheCardNeverMovedPast: when the card's own output named a
// refusal and it never made another tool call, the end says so in one word and one path.
func TestWatchIdleCarriesTheRefusalTheCardNeverMovedPast(t *testing.T) {
	r := NewWallReader("c", nil)
	_, _ = r.Write([]byte("STEP 3\nsh: 1: cannot create /etc/hosts: Permission denied\n"))
	b := newIdleBench(t, 10*time.Second, r)
	end, ended := b.tick(t, 11*time.Second)
	if !ended {
		t.Fatal("the still card is ended")
	}
	if !end.Refused || end.Kind != "write" || end.Path != "/etc/hosts" || end.Step != "3" {
		t.Fatalf("the end carries the refusal the card never moved past, got %+v", end)
	}
}

// TestWatchIdleDoesNotCallAWorkingSilenceIdle: issue #593's own case, and the reason the
// watch reads the process tree at all. A `go test` prints nothing for minutes; its tree
// spends CPU the whole time, and a card like that is working, not dead.
func TestWatchIdleDoesNotCallAWorkingSilenceIdle(t *testing.T) {
	b := newIdleBench(t, 10*time.Second, NewWallReader("c", nil))
	b.snap.ok = true
	for at := time.Second; at <= 40*time.Second; at += 3 * time.Second {
		b.snap.cpu += uint64(3 * time.Second) // a tree spending the whole interval
		if end, ended := b.tick(t, at); ended {
			t.Fatalf("a card whose tree is spending CPU is working, not idle: %+v at %v", end, at)
		}
	}
}

// TestWatchIdleKeepsACardWhoseLogIsGrowing: the other reading. A card that is still talking
// is never idle, whatever its tree is doing.
func TestWatchIdleKeepsACardWhoseLogIsGrowing(t *testing.T) {
	b := newIdleBench(t, 10*time.Second, NewWallReader("c", nil))
	for at := time.Second; at <= 40*time.Second; at += 3 * time.Second {
		f, err := os.OpenFile(b.log, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString("another line\n")
		f.Close()
		if end, ended := b.tick(t, at); ended {
			t.Fatalf("a card that is still writing is not idle: %+v at %v", end, at)
		}
	}
}

// TestWatchIdleLeavesACardThatAlreadyPublished: issue #916's rule, kept. A card whose
// RESULT.md is on disk is finishing, and ending it would only race the write.
func TestWatchIdleLeavesACardThatAlreadyPublished(t *testing.T) {
	b := newIdleBench(t, 10*time.Second, NewWallReader("c", nil))
	if err := os.WriteFile(filepath.Join(b.job, "RESULT.md"), []byte("RESULT: done\nfindings: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if end, ended := b.tick(t, 11*time.Second); ended {
		t.Fatalf("a card that published its report is finishing, not idle: %+v", end)
	}
}

// TestWatchIdleWithNoWindowWatchesNothing: --idle 0 is the behaviour every run had before
// this file existed, and it is reachable by typing it.
func TestWatchIdleWithNoWindowWatchesNothing(t *testing.T) {
	out := WatchIdle(IdleWatch{Log: filepath.Join(t.TempDir(), "x"), Idle: NoIdleWindow}, make(chan struct{}))
	if out != nil {
		t.Fatal("a watch with no window hands back a nil channel, which a select waits on forever")
	}
	// AND IT IS NIL RATHER THAN CLOSED FOR A REASON: a closed channel is always ready, so
	// a run selecting on one would take a zero end -- and kill a card that was fine -- the
	// moment the watch decided there was nothing to report.
	select {
	case end := <-out:
		t.Fatalf("a nil channel never answers, got %+v", end)
	default:
	}
}

// TestCardIdleLineIsNotAWallLine: `js-under-20-bytes` died in a provider stall and was
// reported `WALL task=... path=/var/db/xcode_select_link`, and a shift went looking at the
// wall. A card that simply went still says THAT.
func TestCardIdleLineIsNotAWallLine(t *testing.T) {
	line := CardIdleLine("js-under-20-bytes", IdleEnd{Idle: 300 * time.Second, Step: "16"})
	want := "CARD IDLE task=js-under-20-bytes step=16 idle=300s"
	if line != want {
		t.Fatalf("want %q, got %q", want, line)
	}
}
