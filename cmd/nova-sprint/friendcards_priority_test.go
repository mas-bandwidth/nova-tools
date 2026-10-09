package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The one-shot daemon consumes friend cards, rather than queue or Take. Observe
// the card handed to its runner after a promotion of already-dealt work.
func TestFriendCardsPriorityReachesTheOneShotRunner(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, level string
		fixPeers    bool
	}{
		{"blocker", sprint.PriorityBlocker, false},
		{"fix", sprint.PriorityFix, false},
		{"fix producer urgency", sprint.PriorityFix, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta, _ := takeApp(t, 2, nil, "friend-a")
			ta.ok("tick")
			before := friendCardsAnswer(t, ta, "friend-a")
			require.Len(t, before, 2)
			require.Equal(t, "s1-1.w1", before[0].Card)
			if tc.fixPeers {
				ta.ok("priority s1-1 --fix --reason 'queued repair'")
			}
			ta.ok("priority s1-2 --" + tc.level + " --reason 'next free lane'")
			if tc.fixPeers {
				ta.ok("priority s1-2 --blocker --reason 'urgency within fixed role'")
				assert.Equal(t, sprint.PriorityFix, sprint.QueuePriority(ta.fleetCard("s1-2.w1")))
				assert.Equal(t, sprint.PriorityBlocker, ta.fleetCard("s1-2.w1").F(sprint.FieldProducerPriority))
			}
			held := friendCardsAnswer(t, ta, "friend-a")
			require.Len(t, held, 2)
			assert.Equal(t, before[1], held[0], "ordering preserves the canonical job, generation, epoch and whole packet")
			assert.Equal(t, before[0], held[1])
			assert.Equal(t, "s1-2.w1", friendCardsFirstRun(t, held, nil).ID)
			ta.clean()
		})
	}
}

func TestFriendCardsPriorityPreservesWorkingRecoveryDebt(t *testing.T) {
	t.Parallel()
	ta, _ := takeApp(t, 3, nil, "friend-a")
	ta.ok("tick")
	ta.startFriend("friend-a", 1)
	ta.ok("priority s1-3 --fix --reason 'queued repair'")
	held := friendCardsAnswer(t, ta, "friend-a")
	require.Len(t, held, 3)
	assert.Equal(t, []string{"s1-1.w1", "s1-3.w1", "s1-2.w1"}, []string{held[0].Card, held[1].Card, held[2].Card})
	assert.Equal(t, sprint.Working, held[0].Col)
	assert.Equal(t, "s1-1.w1", friendCardsFirstRun(t, held, nil).ID, "an unfinished working job remains recovery debt before new admission")
	assert.Equal(t, "s1-3.w1", friendCardsFirstRun(t, held, []string{held[0].Job}).ID, "once working debt has a result, the newly free lane selects queued FIX")
	ta.clean()
}

func TestFriendCardsPriorityOrdersSharedWorkAndReaderUrgency(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "friend-a")
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "friend-a", map[string]string{"mode": "one-shot", "width": "8", "roles": "builder,reader"}, "tester")
	require.NoError(t, err)
	ta.ok("set --read-cards on")
	ta.ok("friend sync --root " + t.TempDir())
	ta.ok("fleet down m2")
	briefs := t.TempDir()
	for _, id := range []string{"s1-1", "s1-2"} {
		require.NoError(t, os.WriteFile(filepath.Join(briefs, id+".md"), []byte(passingBrief(id+": read this result\nREPO: mas-bandwidth/nova-tools\n")), 0o644))
	}
	ta.ok("add --stream s1 --brief-dir " + briefs)
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 2")
	for _, id := range []string{"s1-1", "s1-2"} {
		ta.ok("finish --as m1 " + id + ".w1@1 --head " + landHead + " --report done")
	}
	ta.beatUp("friend-a")
	ta.ok("tick")
	reads := friendCardsAnswer(t, ta, "friend-a")
	require.Len(t, reads, 2)
	readIDs := map[string]string{}
	for _, h := range reads {
		readIDs[ta.fleetCard(h.Card).F(sprint.PrimaryField)] = h.Card
	}
	earlierRead, laterRead := readIDs["s1-1"], readIDs["s1-2"]
	require.NotEmpty(t, earlierRead)
	require.NotEmpty(t, laterRead)
	ta.ok("priority s1-2 --blocker --reason 'review this producer first'")
	assert.Equal(t, sprint.PriorityNormal, ta.fleetCard(laterRead).F(sprint.FieldProducerPriority), "the existing Working lease retains its original urgency")
	ta.ok("stop --reason 'return idle read reservations' --until 1h")
	for _, h := range reads {
		require.Equal(t, "read", h.Kind)
		require.Equal(t, sprint.Working, h.Col, "the dealer first reserves an idle friend's read lane")
		require.Equal(t, 1, h.Gen)
		ta.ok("stop-return --as friend.friend-a " + h.Card + "@1 --epoch 0 --reason 'idle read reservation returned before launch'")
	}
	reads = friendCardsAnswer(t, ta, "friend-a")
	require.Len(t, reads, 2)
	for _, h := range reads {
		require.Equal(t, sprint.Ready, h.Col, "the consumer waits for admission; legacy Working reads are recovery debt")
		require.Equal(t, 2, h.Gen, "a returned reservation fences the preceding job")
	}
	ta.ok("start")
	work := t.TempDir()
	for _, id := range []string{"s2-1", "s2-2"} {
		require.NoError(t, os.WriteFile(filepath.Join(work, id+".md"), []byte(passingBrief(id+": queued work\nREPO: mas-bandwidth/nova-tools\nWHO: friend friend-a\n")), 0o644))
	}
	ta.ok("add --stream s2 --brief-dir " + work)
	ta.ok("tick")
	ta.ok("priority s2-2 --high --reason 'work before reader class'")
	held := friendCardsAnswer(t, ta, "friend-a")
	require.Len(t, held, 4)
	for _, h := range held {
		require.Equal(t, sprint.Ready, h.Col, "the shared admission comparison contains no active leases")
	}
	assert.Equal(t, []string{"s2-2.w1", laterRead, earlierRead, "s2-1.w1"}, []string{held[0].Card, held[1].Card, held[2].Card, held[3].Card})
	assert.Equal(t, sprint.PriorityReader, sprint.QueuePriority(ta.fleetCard(laterRead)))
	assert.Equal(t, sprint.PriorityBlocker, ta.fleetCard(laterRead).F(sprint.FieldProducerPriority))
	assert.Equal(t, "s2-2.w1", friendCardsFirstRun(t, held, nil).ID)
	assert.Equal(t, laterRead, friendCardsFirstRun(t, held, []string{held[0].Job}).ID, "reader urgency refines READER ahead of normal work after urgent work ends")
	ta.clean()
}

// This runner records the real daemon's selected card without a provider or
// process. Native launch/ACK behavior has its own joined boundary tests.
type friendCardsPriorityRunner struct {
	cancel context.CancelFunc
	first  chan friend.Card
}

func (*friendCardsPriorityRunner) Deliver(context.Context, string) (int, error) { return 0, nil }
func (*friendCardsPriorityRunner) Refusal() string                              { return "" }
func (r *friendCardsPriorityRunner) RunCard(_ context.Context, c friend.Card) (friend.LaneTurn, error) {
	r.first <- c
	r.cancel()
	return friend.LaneTurn{}, nil
}

func friendCardsFirstRun(t *testing.T, held []friend.HeldCard, done []string) friend.Card {
	t.Helper()
	var selected friend.Card
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		for _, job := range done {
			outbox := filepath.Join(dir, "outbox", job)
			require.NoError(t, os.MkdirAll(outbox, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("done\n"), 0o644))
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		runner := &friendCardsPriorityRunner{cancel: cancel, first: make(chan friend.Card, 1)}
		d := &friend.Daemon{
			Friend: "friend-a", Harness: "fake", Dir: dir, Width: 1,
			Store: bustest.NewFake(time.Now(), "friend-a", "coordinator"), Deliver: runner,
			Row:  func() (string, int) { return friend.ModeOneShot, 1 },
			Held: func(context.Context) (friend.Row, error) { return friend.Row{Cards: held, From: friend.FromCards}, nil },
			Now:  time.Now, Pause: func(context.Context, time.Duration) { synctest.Wait() },
			Record: func(string) {}, Status: func(friend.Status) error { return nil },
			Beat:      func(context.Context, time.Time) error { return nil },
			SaveLanes: func(friend.LaneState) error { return nil },
		}
		require.NoError(t, d.Run(ctx))
		select {
		case selected = <-runner.first:
		default:
			t.Fatal("the free one-shot lane never selected a held card")
		}
	})
	return selected
}
