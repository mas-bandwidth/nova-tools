package main

// The tick's profile at the owner's size (3 x 1000 cards, 8 members of width
// 64, 4 readers) on the in-memory store: go test -run TestTickProfile -v
// ./cmd/nova-sprint -args -cpuprofile-ticks <file>. It prints every tick's
// parts and times; the CPU profile covers the ticks only (label part=tick).
// Without the flag it is skipped: it is the bench of the tick's cost, run by
// hand, not a test of the tiers.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

var tickProfile = flag.String("cpuprofile-ticks", "", "write the ticks' CPU profile here")
var tickCount = flag.Int("ticks", 40, "ticks to run")

func TestTickProfile(t *testing.T) {
	t.Parallel()
	if *tickProfile == "" {
		t.Skip("the tick's profile is run by hand: -args -cpuprofile-ticks <file>")
	}
	ta := newTestApp(t)
	ta.live = []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	ta.ok("init --readers reader-a,reader-b,reader-c,reader-d --members m1:64,m2:64,m3:64,m4:64,m5:64,m6:64,m7:64,m8:64")
	for _, s := range []string{"a", "b", "c"} {
		ta.ok("add --stream " + s + " --count 1000")
	}
	ta.ok("start")
	st, err := ta.a.store(common{redis: "mem:0", actor: "machine"})
	if err != nil {
		t.Fatal(err)
	}
	if *tickProfile != "" {
		f, err := os.Create(*tickProfile)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			t.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}
	var slowest time.Duration
	var total time.Duration
	for i := 0; i < *tickCount; i++ {
		ta.beat()
		began := time.Now()
		var err error
		var line string
		pprof.Do(context.Background(), pprof.Labels("part", "tick"), func(ctx context.Context) {
			res, e := st.Tick(ctx)
			err = e
			var parts []string
			for _, p := range res.Times {
				parts = append(parts, fmt.Sprintf("%s/%s=%s", p.Table, p.Name, p.Took.Round(time.Millisecond)))
			}
			line = fmt.Sprintf("moved=%d %s", len(res.Moved()), strings.Join(parts, " "))
		})
		took := time.Since(began)
		total += took
		slowest = max(slowest, took)
		t.Logf("tick %d %s %s", i+1, took.Round(time.Millisecond), line)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		out := ta.ok("play --ticks 1 --every 10ms --broken 0 --fail 0 --stuck 0 --cross 0 --down 0 --up 1")
		_ = out
		if strings.Contains(ta.ok("where"), "DONE") {
			t.Logf("done at tick %d", i+1)
			break
		}
	}
	t.Logf("slowest %s total %s", slowest.Round(time.Millisecond), total.Round(time.Millisecond))
}
