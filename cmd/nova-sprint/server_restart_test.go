package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A server restart keeps every in-flight read whose lease is live, and only
// takes back reads whose lease has lapsed (tla/ServerLanes.tla, Restart;
// LiveLeaseNeverTakenBack, EveryLapsedReadTakenBack; docs/SPEC-SPRINT.md section 6).
// The restart is run's own start: the loop stops after its first tick (its binary
// is replaced under it, as in TestRunStopsWhenItsBinaryIsReplaced), and by that
// tick the lapsed read is taken back, which a tick alone never does.
func TestAServerRestartKeepsReadsWithLiveLeases(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(1)
	ta.ok("ask") // the pair: a card's reads are asked together, of reader-a and reader-b
	lapsed, live := "s1-1.r1.reader-a", "s1-1.r1.reader-b"
	require.Equal(t, []string{lapsed}, ta.askedOf("reader-a"))
	require.Equal(t, []string{live}, ta.askedOf("reader-b"))

	// reader-a begins its read 11 minutes before the restart, reader-b 5:
	// the 10 minute lease (sprint.DefaultReadLease) has lapsed only on reader-a's
	ta.ok("read --as reader-a --begin " + lapsed)
	ta.a.sleep(6 * time.Minute)
	ta.ok("read --as reader-b --begin " + live)
	ta.a.sleep(5 * time.Minute)

	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)
	inReading := func(reader string) []string {
		cards, err := st.ReadCells(context.Background(), sprint.Readers, reader, sprint.Reading)
		require.NoError(t, err)
		var ids []string
		for _, c := range cards {
			ids = append(ids, c.ID)
		}
		return ids
	}

	// a tick alone takes back no read: only the server's start does
	ta.ok("tick")
	require.Equal(t, []string{lapsed}, inReading("reader-a"), "a tick takes back no lapsed read")
	require.Equal(t, []string{live}, inReading("reader-b"))

	// the server starts: run, stopped after its first tick
	exe := filepath.Join(t.TempDir(), "nova-sprint")
	require.NoError(t, os.WriteFile(exe, []byte("the build the server started with"), 0o755))
	ta.a.executable = func() (string, error) { return exe, nil }
	// the balance and store round trip loops run once, then wait for ever
	ta.a.after = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
	ticks := 0
	var atFirstTick struct{ a, b []string }
	ta.a.ticked = func(n int, _ time.Time, _ string) {
		ticks++
		if n == 1 {
			atFirstTick.a, atFirstTick.b = inReading("reader-a"), inReading("reader-b")
			require.NoError(t, os.WriteFile(exe, []byte("the build installed under it, longer"), 0o755))
		}
	}
	var out, errb startBuffer
	ta.beat()
	code := ta.a.run([]string{"run", "--tick-deadline", "0"}, &out, &errb)
	require.Equal(t, exitReplaced, code, "%s%s", out.String(), errb.String())
	require.Equal(t, 1, ticks)

	// the read with a live lease is still in flight on reader-b; the lapsed one is
	// gone from reader-a's reading by the first tick
	assert.Equal(t, []string{live}, atFirstTick.b, "a read whose lease is live survives the restart")
	assert.Empty(t, atFirstTick.a, "a read whose lease lapsed is taken back at the restart")

	recs, err := st.Records(context.Background(), sprint.Readers, []string{lapsed, live})
	require.NoError(t, err)
	require.Len(t, recs, 2)
	require.NotNil(t, recs[0])
	assert.Equal(t, sprint.RetiredByLapsed, recs[0].F("retired_by"))
	require.NotNil(t, recs[1])
	assert.Empty(t, recs[1].F("retired_by"))
}

// startBuffer is a buffer the loops run starts beside its ticks may write to at once.
type startBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *startBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *startBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
