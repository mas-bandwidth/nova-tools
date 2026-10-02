package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// H1: the read verbs show a work card's dealt and taken and a read card's
// asked and begun.
func TestQueueShowsTheStamps(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	dealt := ta.a.now().UTC().Format(time.RFC3339)
	ta.a.sleep(time.Minute)
	ta.ok("take --as m1 s1-1.w1@1")
	taken := ta.a.now().UTC().Format(time.RFC3339)
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 1, "the work card's stamps: %+v", q.Cards)
	require.Equal(t, dealt, q.Cards[0].Dealt, "the work card's stamps: %+v", q.Cards)
	require.Equal(t, taken, q.Cards[0].Taken, "the work card's stamps: %+v", q.Cards)
	require.Contains(t, ta.ok("queue --as m1"), "dealt="+dealt+" taken="+taken, "queue")
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.ok("ask")
	asked := ta.a.now().UTC().Format(time.RFC3339)
	ta.a.sleep(time.Minute)
	ta.ok("read --as reader-a --begin s1-1.r1.reader-a")
	begun := ta.a.now().UTC().Format(time.RFC3339)
	ta.json("queue --as reader-a", &q)
	require.Len(t, q.Cards, 1, "the read card's stamps: %+v", q.Cards)
	require.Equal(t, asked, q.Cards[0].Asked, "the read card's stamps: %+v", q.Cards)
	require.Equal(t, begun, q.Cards[0].Begun, "the read card's stamps: %+v", q.Cards)
	require.Contains(t, ta.ok("queue --as reader-a"), "asked="+asked+" begun="+begun, "queue")
	out := ta.ok("card --fields s1-1")
	require.Contains(t, out, "dealt="+dealt, "card")
	require.Contains(t, out, "begun="+begun, "card")
	ta.clean()
}
