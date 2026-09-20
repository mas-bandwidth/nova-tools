package main

// Command-level two-dealer control for #2029 remainder: two `fill --once` ticks
// sharing one ready directory and one free seat must not both launch.

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFillTwoDealersObservingOneFreeSeatDoNotOverDispatch(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 2)
	machines := fillMachines(t, dir, "bench-a")
	launcher := filepath.Join(fakeBins(t), "nova-bus"+exeSuffix())

	type result struct {
		code int
		out  string
		errb string
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out, errb bytes.Buffer
			code := run([]string{"fill", "--ready", ready, "--launched", launched,
				"--machines", machines, "--bench", "bench-a",
				"--capacity", "1",
				"--launcher", launcher,
				"--launch-grace", "0", "--once",
			}, &out, &errb, time.Now().UTC())
			results[i] = result{code: code, out: out.String(), errb: errb.String()}
		}(i)
	}
	wg.Wait()

	if got := fillCount(t, launched); got != 1 {
		t.Fatalf("two CLI dealers observing free=1 launched %d cards, want 1; %#v", got, results)
	}
	if got := fillCount(t, ready); got != 1 {
		t.Fatalf("ready holds %d cards, want 1 (the loser stood down); %#v", got, results)
	}
}
