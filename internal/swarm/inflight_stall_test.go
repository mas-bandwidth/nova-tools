package swarm

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ISSUE #917: a route at its cap must not launch another task, STATUS must say where the
// route stands, and the cap is counted from the sidecars the route already has running.
func TestRouteStatusLineShowsInflightAndCap(t *testing.T) {
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	w.Route = "muse/zen"
	w.MaxInflight = 2
	route := w.RouteName()
	for _, id := range []string{"a", "b", "c"} {
		sc := Sidecar{ID: id, Route: route, MaxInflight: 2, Files: 1, Tokens: 100, RC: -1}
		if err := p.Add([]byte("task "+id), sc); err != nil {
			t.Fatal(err)
		}
	}
	// Two running and one pending: that is exactly what a route at its cap looks like.
	for _, id := range []string{"a", "b"} {
		if err := p.Claim(id, Pending, Running); err != nil {
			t.Fatal(err)
		}
	}
	found := false
	for _, r := range RouteStatus(p) {
		if r.Route == route && r.Inflight == 2 && r.Cap == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("STATUS ROUTE %s inflight=2 cap=2 is missing; got %+v", route, RouteStatus(p))
	}
	in := RunInput{Pool: p, Worker: w}
	if in.routeBelowCap(Sidecar{Route: route}) {
		t.Errorf("a route at 2/2 is not below its cap; it must launch nothing")
	}
	if err := p.Claim("a", Running, Done); err != nil {
		t.Fatal(err)
	}
	if !in.routeBelowCap(Sidecar{Route: route}) {
		t.Errorf("a route at 1/2 is below its cap; the third task may launch")
	}
}

// A fake harness that writes one line and then goes silent is ended `end=stall`, and the
// line names the last thing it said.
func TestStallEndsTheJobAndNamesTheLastLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stall fixture is a POSIX shell script")
	}
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	w.StallAfter = "80ms"
	w.Usage = UsageNone
	harness := filepath.Join(dir, "fake-harness")
	if err := os.WriteFile(harness, []byte("#!/bin/sh\necho 'working on it'\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.Harness = harness
	id := "stall-job"
	jobDir := w.JobDir(1, id)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nonce, err := Nonce()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Reserve(1, id, jobDir, nonce, os.Getpid(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	sc := Sidecar{ID: id, Files: 1, Tokens: 100, RC: -1, Route: w.RouteName()}
	var out, errb strings.Builder
	code := Supervise(SuperviseInput{
		Pool: p, Task: id, Slot: 1, Nonce: nonce, Worker: w, Sidecar: sc, Key: "k",
		Stdout: &out, Stderr: &errb, Now: func() time.Time { return time.Now().UTC() },
	})
	if code != 0 {
		t.Fatalf("supervise returned %d: %s", code, errb.String())
	}
	var rec ExitRecord
	if err := ReadJSON(ExitPath(jobDir), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.End != EndStall {
		t.Fatalf("the silent job ended %q, want %q:\n%s", rec.End, EndStall, errb.String())
	}
	if !strings.Contains(rec.Last, "working on it") {
		t.Errorf("the stall record must name the last log line, got %q", rec.Last)
	}
	if !strings.Contains(errb.String(), "STALL task="+id) {
		t.Errorf("the supervisor must print a STALL line naming the task:\n%s", errb.String())
	}
}

// A stalled task is requeued once and not twice.
func TestAStalledTaskIsRequeuedOnce(t *testing.T) {
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	w.Route = "muse/zen"
	w.MaxInflight = 2
	sc := Sidecar{ID: "first", Files: 1, Tokens: 100, RC: -1, Route: w.RouteName(), MaxInflight: 2}
	if err := p.Add([]byte("the task"), sc); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim("first", Pending, Running); err != nil {
		t.Fatal(err)
	}
	in := RunInput{Pool: p, Worker: w}
	if !in.routeBelowCap(sc) {
		t.Fatal("the route is below its cap, so the stall may be requeued")
	}
	if !in.requeueStall(sc, time.Now().UTC()) {
		t.Fatal("the first stall must requeue the task once")
	}
	pending, err := p.List(Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("the first stall wants one requeued task, got %d", len(pending))
	}
	if pending[0].Stalled != 1 || pending[0].From != "first" {
		t.Errorf("the descendant wants stalled=1 from=first, got stalled=%d from=%q", pending[0].Stalled, pending[0].From)
	}
	if in.requeueStall(pending[0], time.Now().UTC()) {
		t.Errorf("a task stalled a second time must not be requeued again")
	}
}
