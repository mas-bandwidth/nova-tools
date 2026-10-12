package member

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
	"github.com/stretchr/testify/require"
)

// TestABeatNeverGoesWithoutItsLoad: nova-sprint v1.2.11 refuses a beat without --load (the
// server cannot measure the worker's machine), so every beat carries one. A member whose meter
// has no sample still sends --load: the last sample it had, else 0. A beat is never sent
// without --load.
func TestABeatNeverGoesWithoutItsLoad(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1, Meter: hostload.NewSampler(hostload.Source{})})
	g.s.set("queue", 0, queueJSON(t, 7))
	require.NoError(t, g.m.Beat())
	require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0"}, g.s.lines("beat"), "no sample: the beat still carries --load 0")
}

// TestABeatCarriesLoadUnmeasuredWhenTheFlagIsListed: a beat that carries 0 for want of a
// reading of the machine also says so, with --load-unmeasured, when the installed sprint's
// `fleet beat -h` lists the flag (Config.LoadUnmeasured, the caller's one probe at start).
func TestABeatCarriesLoadUnmeasuredWhenTheFlagIsListed(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1, Meter: hostload.NewSampler(hostload.Source{}), LoadUnmeasured: func() bool { return true }})
	g.s.set("queue", 0, queueJSON(t, 7))
	require.NoError(t, g.m.Beat())
	require.Equal(t, []string{"fleet beat m --stop-returns 0 --load-unmeasured --load 0.0"}, g.s.lines("beat"), "the unmeasured 0 is said, not a bare 0")
}

// TestABeatSendsAPlainZeroWhenTheFlagIsNotListed: a sprint that does not know
// --load-unmeasured gets a plain --load 0, never a flag it would refuse.
func TestABeatSendsAPlainZeroWhenTheFlagIsNotListed(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1, Meter: hostload.NewSampler(hostload.Source{}), LoadUnmeasured: func() bool { return false }})
	g.s.set("queue", 0, queueJSON(t, 7))
	require.NoError(t, g.m.Beat())
	require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0"}, g.s.lines("beat"), "the flag is not listed, so the beat carries a plain 0")
}

// TestABeatProbesForTheFlagOnce: the caller's probe is asked once, not every beat, so a
// sprint that lists the flag is asked once even as the member beats unmeasured again.
func TestABeatProbesForTheFlagOnce(t *testing.T) {
	t.Parallel()
	var asked atomic.Int64
	g := newRig(Config{As: "m", Width: 1, Meter: hostload.NewSampler(hostload.Source{}), LoadUnmeasured: func() bool { asked.Add(1); return true }})
	g.s.set("queue", 0, queueJSON(t, 7))
	require.NoError(t, g.m.Beat())
	require.NoError(t, g.m.Beat())
	require.Equal(t, int64(1), asked.Load(), "the probe runs once at start, not every beat")
	require.Equal(t, []string{
		"fleet beat m --stop-returns 0 --load-unmeasured --load 0.0",
		"fleet beat m --stop-returns 0 --load-unmeasured --load 0.0",
	}, g.s.lines("beat"))
}

// overlapWriter is an out that says whether two writes were ever inside it at once. Its
// first write waits inside, up to holdFor, for a second to arrive: a member that writes
// one line at a time keeps the second outside until the first returns, so it never does.
type overlapWriter struct {
	inside  atomic.Int32
	overlap atomic.Bool
	first   sync.Once
	second  sync.Once
	entered chan struct{} // closed once the first write is inside
	arrived chan struct{} // closed once a write came in while another was inside
	mu      sync.Mutex
	b       bytes.Buffer
}

// holdFor bounds the first write's wait only so a passing run ends: a second write that
// came later than it would read as no overlap (a false pass), never as a false red.
const holdFor = time.Second

func newOverlapWriter() *overlapWriter {
	return &overlapWriter{entered: make(chan struct{}), arrived: make(chan struct{})}
}

func (w *overlapWriter) Write(p []byte) (int, error) {
	if w.inside.Add(1) > 1 {
		w.overlap.Store(true)
		w.second.Do(func() { close(w.arrived) })
	}
	defer w.inside.Add(-1)
	isFirst := false
	w.first.Do(func() { isFirst = true; close(w.entered) })
	if isFirst {
		select {
		case <-w.arrived:
		case <-time.After(holdFor):
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *overlapWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// TestTheBeatAndThePassSayThingsOneAtATime: the beat goes on its own goroutine apart from
// the pass (BeatLoop), and both say things on the member's one out, so the member writes
// it one line at a time whatever writer it was given (oneWriter). Beat's load note is held
// inside the writer while the pass's room refusal is said: a member that wrote the
// caller's writer as given lets the second line in beside the first, on every run (under
// -race the same two writes on a bytes.Buffer were TestAMemberWhosePassIsHungStopsBeating's
// data race, 1 run in 50 on dev 274339741).
func TestTheBeatAndThePassSayThingsOneAtATime(t *testing.T) {
	t.Parallel()
	out := newOverlapWriter()
	m := New(Config{As: "m", Width: 1, Room: func() (bool, string) { return false, "the disk is under its floor" }}, newScript(), newRunner(), &fakePusher{}, out)
	var both sync.WaitGroup
	both.Add(2)
	go func() { defer both.Done(); _ = m.Beat() }()            // no meter: "NOTE beat: load unmeasured", its first line
	<-out.entered                                              // the beat's line is inside the writer
	go func() { defer both.Done(); m.room(time.Unix(0, 0)) }() // no room: "take REFUSED"
	both.Wait()
	require.False(t, out.overlap.Load(), "the pass's line went into the writer while the beat's was inside it")
	require.Contains(t, out.String(), "NOTE beat: load unmeasured, sent 0\n")
	require.Contains(t, out.String(), "take REFUSED: the disk is under its floor\n")
}

// TestARefusedBeatIsLoggedOncePerMinute: a beat the server refuses (exit 2) is loud: the
// member logs the refusal with the exact argv it sent, at most once a minute.
func TestARefusedBeatIsLoggedOncePerMinute(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	g := newRig(Config{As: "m", Width: 1, Now: func() time.Time { return now }})
	g.s.set("queue", 0, queueJSON(t, 7))
	g.s.set("beat", 2, "a beat sent to the server is fleet beat <member> --stop-returns <n> --load <percent> and nothing more")
	require.Error(t, g.m.Beat())
	require.Equal(t, 1, strings.Count(g.out.String(), "BEAT REFUSED: fleet beat m --stop-returns 0 --load 0.0:"), "the first refusal is logged with its argv")
	require.Error(t, g.m.Beat())
	require.Equal(t, 1, strings.Count(g.out.String(), "BEAT REFUSED:"), "a second refusal inside the minute is not logged again")
	now = now.Add(time.Minute)
	require.Error(t, g.m.Beat())
	require.Equal(t, 2, strings.Count(g.out.String(), "BEAT REFUSED:"), "a refusal after the minute is logged again")
}

// TestARefusedBeatMarksTheMemberBeatRefused: a beat the server refuses (exit 2) also marks
// the member beat-refused in its own status file (Config.Status), so doctor and the
// dashboard row can show it; a beat the server takes clears the mark. The mark is written
// once, on the change, not every beat.
func TestARefusedBeatMarksTheMemberBeatRefused(t *testing.T) {
	t.Parallel()
	var marks []string
	g := newRig(Config{As: "m", Width: 1, Status: func(word string) error { marks = append(marks, word); return nil }})
	g.s.set("queue", 0, queueJSON(t, 7))
	g.s.set("beat", 2, "a beat sent to the server is fleet beat <member> --stop-returns <n> --load <percent> and nothing more")
	require.Error(t, g.m.Beat())
	require.Error(t, g.m.Beat())
	require.Equal(t, []string{"beat-refused"}, marks, "the refusal marks the member beat-refused, once")
	g.s.set("beat", 0, "")
	require.NoError(t, g.m.Beat())
	require.Equal(t, []string{"beat-refused", ""}, marks, "a beat taken clears the mark")
}
