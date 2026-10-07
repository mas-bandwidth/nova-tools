package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// H1: the read verbs show a work card's dealt and taken.
func TestQueueShowsTheStamps(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
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
	out := ta.ok("card --fields s1-1")
	require.Contains(t, out, "dealt="+dealt, "card")
	ta.clean()
}
