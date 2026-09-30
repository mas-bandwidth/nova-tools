package main

import (
	"strings"
	"testing"
	"time"
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
	if len(q.Cards) != 1 || q.Cards[0].Dealt != dealt || q.Cards[0].Taken != taken {
		t.Fatalf("the work card's stamps: %+v", q.Cards)
	}
	if out := ta.ok("queue --as m1"); !strings.Contains(out, "dealt="+dealt+" taken="+taken) {
		t.Fatalf("queue: %s", out)
	}
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.ok("ask")
	asked := ta.a.now().UTC().Format(time.RFC3339)
	ta.a.sleep(time.Minute)
	ta.ok("read --as reader-a --begin s1-1.r1.reader-a")
	begun := ta.a.now().UTC().Format(time.RFC3339)
	ta.json("queue --as reader-a", &q)
	if len(q.Cards) != 1 || q.Cards[0].Asked != asked || q.Cards[0].Begun != begun {
		t.Fatalf("the read card's stamps: %+v", q.Cards)
	}
	if out := ta.ok("queue --as reader-a"); !strings.Contains(out, "asked="+asked+" begun="+begun) {
		t.Fatalf("queue: %s", out)
	}
	if out := ta.ok("card --fields s1-1"); !strings.Contains(out, "dealt="+dealt) || !strings.Contains(out, "begun="+begun) {
		t.Fatalf("card: %s", out)
	}
	ta.clean()
}
