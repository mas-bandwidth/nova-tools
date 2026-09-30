package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The streams are served fairly (the owner's ruling of 2026-09-30, errata 3
// amendment 10: "we should deal fairly from each work stream, perhaps with ...
// another index in that table"): the deal, the ask and the accept each take
// the streams in turn from their own index on the work table, so no stream's
// backlog is dealt, read or landed before another's.

// streamIndexes is the work table's stream indexes as the store holds them.
func (ta *testApp) streamIndexes() string {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	if err != nil {
		ta.t.Fatal(err)
	}
	s, err := st.Load(context.Background(), store.All, nil)
	if err != nil {
		ta.t.Fatal(err)
	}
	var out []string
	for _, p := range []string{sprint.PropStreamIndex, sprint.PropAskStreamIndex, sprint.PropAcceptStreamIndex} {
		v, _ := s.Work.Prop(p)
		out = append(out, p+"="+v)
	}
	return strings.Join(out, " ")
}

// spreadOf is the most and the fewest of a column over the streams.
func spreadOf(v tablesView, table, col string, streams []string) (lo, hi int) {
	lo = -1
	for _, s := range streams {
		n, _ := strconv.Atoi(v.Tables[table][s][col])
		if lo < 0 || n < lo {
			lo = n
		}
		hi = max(hi, n)
	}
	return lo, hi
}

// fairStreams runs three streams of count, eight machines of width, four
// readers and the world with no failures, the coordinator accepting what the
// readers passed, and fails unless after every tick the streams' review and
// landed counts are within a few of each other. The indexes are read back
// across a stop and a start of the machine.
func fairStreams(t *testing.T, count, width int) {
	t.Helper()
	members := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	streams := []string{"s1", "s2", "s3"}
	ta := newTestApp(t)
	ta.live = members
	var ms []string
	for _, m := range members {
		ms = append(ms, m+":"+strconv.Itoa(width))
	}
	ta.ok("init --readers reader-a,reader-b,reader-c,reader-d --members " + strings.Join(ms, ","))
	for _, s := range streams {
		ta.ok(fmt.Sprintf("add --stream %s --count %d", s, count))
	}
	ta.ok("start")
	ta.live = nil
	const few = 3
	for tick := 1; tick <= 200; tick++ {
		ta.ok("tick")
		play := ta.ok(fmt.Sprintf("play --simulation --fail 0 --broken 0 --stuck 0 --cross 0 --down 0 --seed %d --ticks 1", tick))
		var v tablesView
		ta.json("where", &v)
		for _, col := range []string{"review", "landed"} {
			if lo, hi := spreadOf(v, "work", col, streams); hi-lo > few {
				t.Fatalf("tick %d: the streams' %s counts run from %d to %d, more than %d apart: %v", tick, col, lo, hi, few, v.Tables["work"])
			}
		}
		if tick == 3 {
			before := ta.streamIndexes()
			ta.ok("stop")
			if after := ta.streamIndexes(); after != before || !strings.Contains(before, sprint.PropStreamIndex+"=s") {
				t.Fatalf("the indexes across a stop: %s, then %s", before, after)
			}
			ta.ok("start")
		}
		if strings.Contains(play, "every stream has landed") {
			return
		}
		ta.ok("accept --read-ok")
	}
	t.Fatal("not landed after 200 ticks")
}

func TestTheStreamsAreReadAndLandedTogether(t *testing.T) {
	t.Parallel()
	fairStreams(t, 60, 4)
}
