package sprint

import (
	"strings"
	"testing"
	"time"
)

func TestGoalDue(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	since := t0
	g := Goal{Name: "friend-a"}
	if !g.Due(t0, since, nil) {
		t.Fatal("a person never pushed to is due")
	}
	g.Last = t0
	if g.Due(t0.Add(RemindEvery-time.Second), since, nil) || !g.Due(t0.Add(RemindEvery), since, nil) {
		t.Fatal("five minutes of running time")
	}
	// A run of the machine that began after the last push pushes at once.
	if !g.Due(t0.Add(time.Minute), t0.Add(time.Minute+time.Second), nil) {
		t.Fatal("a start after the last push pushes at once")
	}
	// Time STOPPED does not count.
	stopped := func(from, to time.Time) time.Duration {
		if to.After(t0.Add(time.Hour)) {
			return time.Hour - time.Minute
		}
		return 0
	}
	if g.Due(t0.Add(time.Hour+2*time.Minute), since, stopped) {
		t.Fatal("an hour stopped counted as running time")
	}
	if !g.Due(t0.Add(time.Hour+5*time.Minute), since, stopped) {
		t.Fatal("five minutes of running time after the stop")
	}
	// A failed attempt counts as the attempt: it is retried each interval, not each tick.
	g.Tried = t0.Add(10 * time.Minute)
	if g.Due(t0.Add(14*time.Minute), since, nil) || !g.Due(t0.Add(15*time.Minute), since, nil) {
		t.Fatal("a failed attempt is retried after the interval")
	}
}

func TestGoalValidation(t *testing.T) {
	t.Parallel()
	for _, n := range []string{"friend-a", "a", "reader_1.x"} {
		if err := ValidGoalName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", "-a", "A", "a b", "a/b", strings.Repeat("a", 65)} {
		if ValidGoalName(n) == nil {
			t.Errorf("%q accepted", n)
		}
	}
	if ValidGoalText("ok", 2) != nil || ValidGoalText("abc", 2) == nil || ValidGoalText(" ", 9) == nil || ValidGoalText("a\x00", 9) == nil || ValidGoalText("\xff", 9) == nil {
		t.Fatal("text validation")
	}
	if k, p, err := ParseRoute("file:/x/y"); err != nil || k != RouteFile || p != "/x/y" {
		t.Fatalf("%v %v %v", k, p, err)
	}
	for _, r := range []string{"", "file:", "x", "mail:a"} {
		if _, _, err := ParseRoute(r); err == nil {
			t.Errorf("route %q accepted", r)
		}
	}
}
