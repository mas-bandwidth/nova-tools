package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunnerTable is the runner list a test drives: no gh, no network, no restart.
type fakeRunnerTable struct {
	runners []Runner
	running int
	asked   []string
}

func (f *fakeRunnerTable) Runners(repo string) ([]Runner, error) {
	f.asked = append(f.asked, repo)
	return f.runners, nil
}
func (f *fakeRunnerTable) InProgress(repo string) (int, error) { return f.running, nil }

// fakeRestarter records the restarts instead of making them.
type fakeRestarter struct{ names []string }

func (f *fakeRestarter) Restart(name string) error {
	f.names = append(f.names, name)
	return nil
}

// runnerQueue is a queue directory with the busy record already seeded, which is how the
// rule sees a runner that has been busy-with-nothing-running since before this tick.
func runnerQueue(t *testing.T, since map[string]time.Time) string {
	t.Helper()
	queue := t.TempDir()
	if len(since) > 0 {
		writeBusySince(queue, since)
	}
	return queue
}

func reapRunners(t *testing.T, queue string, in ReapInput) (string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	in.Roots, in.Queue = t.TempDir(), queue
	in.Deadline = time.Hour
	in.Procs = &fakeProcs{live: map[int]bool{}}
	in.TempGlob = filepath.Join(t.TempDir(), "*swarmtest*")
	in.Stdout, in.Stderr = &out, &errs
	if exit := Reap(in); exit != 0 {
		t.Fatalf("reap exit %d: %s%s", exit, out.String(), errs.String())
	}
	return strings.TrimSpace(out.String()), errs.String()
}

// TestReapRestartsTheStuckRunnerAndOnlyThatOne is rule E2 (issue #828, class E, second
// rule): on 2026-09-16 sixteen runners showed busy with zero runs in progress for twenty
// minutes and nothing merged for forty. One runner here has been busy-with-nothing-running
// for twenty minutes and one for one minute, so exactly ONE restart happens.
//
// The mutations that matter: dropping the five-minute age check restarts both, which is a
// reaper that kills a runner the moment GitHub reports it busy; dropping the in-progress
// check restarts runners that are busy because they are working.
func TestReapRestartsTheStuckRunnerAndOnlyThatOne(t *testing.T) {
	now := time.Date(2026, 9, 16, 17, 35, 0, 0, time.UTC)
	queue := runnerQueue(t, map[string]time.Time{
		"space-nova-1": now.Add(-20 * time.Minute),
		"space-nova-3": now.Add(-1 * time.Minute),
	})
	restarter := &fakeRestarter{}
	table := &fakeRunnerTable{running: 0, runners: []Runner{
		{Name: "space-nova-1", Status: "online", Busy: true},
		{Name: "space-nova-3", Status: "online", Busy: true},
		{Name: "studio-nova-2", Status: "online", Busy: false},
	}}

	out, _ := reapRunners(t, queue, ReapInput{
		Repo: "mas-bandwidth/nova-tools", Runners: table, Restarter: restarter,
		RunnerIdle: 5 * time.Minute, Now: func() time.Time { return now },
	})

	if len(restarter.names) != 1 || restarter.names[0] != "space-nova-1" {
		t.Errorf("restarted %v, want exactly [space-nova-1] (busy 20 minutes with nothing running)", restarter.names)
	}
	if !strings.Contains(out, "restarted=1") {
		t.Errorf("REAP line = %q, want restarted=1", out)
	}
	if n := len(strings.Split(out, "\n")); n != 1 {
		t.Errorf("reap printed %d lines, want one REAP line:\n%s", n, out)
	}
	// The restarted runner's row is gone (it gets its own five minutes if it jams again);
	// the young one keeps the moment it was FIRST seen busy, or it is never old enough.
	since := readBusySince(queue)
	if _, ok := since["space-nova-1"]; ok {
		t.Errorf("the restarted runner is still recorded busy: %v", since)
	}
	if got, want := since["space-nova-3"], now.Add(-1*time.Minute); !got.Equal(want) {
		t.Errorf("space-nova-3 recorded at %v, want the moment it was first seen busy (%v)", got, want)
	}
	if _, ok := since["studio-nova-2"]; ok {
		t.Errorf("a runner that is not busy has a row: %v", since)
	}
}

// TestReapNeverRestartsWhileRunsAreInProgress: a busy runner with a run in progress is a
// working runner. It also pins the first sight: a runner nobody had seen busy is recorded
// and left alone, so the rule is always "for more than five minutes" and never "now".
func TestReapNeverRestartsWhileRunsAreInProgress(t *testing.T) {
	now := time.Date(2026, 9, 16, 17, 35, 0, 0, time.UTC)

	working := runnerQueue(t, map[string]time.Time{"space-nova-1": now.Add(-40 * time.Minute)})
	restarter := &fakeRestarter{}
	out, _ := reapRunners(t, working, ReapInput{
		Repo: "o/n", Runners: &fakeRunnerTable{running: 3, runners: []Runner{{Name: "space-nova-1", Busy: true}}},
		Restarter: restarter, Now: func() time.Time { return now },
	})
	if len(restarter.names) != 0 || !strings.Contains(out, "restarted=0") {
		t.Errorf("restarted %v on a bench with three runs in progress; REAP line = %q", restarter.names, out)
	}
	if len(readBusySince(working)) != 0 {
		t.Errorf("a runner busy on a live run keeps a stuck record: %v", readBusySince(working))
	}

	fresh := runnerQueue(t, nil)
	first := &fakeRestarter{}
	out, _ = reapRunners(t, fresh, ReapInput{
		Repo: "o/n", Runners: &fakeRunnerTable{running: 0, runners: []Runner{{Name: "space-nova-1", Busy: true}}},
		Restarter: first, Now: func() time.Time { return now },
	})
	if len(first.names) != 0 || !strings.Contains(out, "restarted=0") {
		t.Errorf("the first sight of a busy runner restarted it: %v, %q", first.names, out)
	}
	if got, ok := readBusySince(fresh)["space-nova-1"]; !ok || !got.Equal(now) {
		t.Errorf("the first sight was not recorded: %v", readBusySince(fresh))
	}
}

// TestReapDryRunCountsTheStuckRunnerAndRestartsNothing: --dry-run changes nothing and
// prints the same count, runners included, so the rule can be read before it is trusted.
func TestReapDryRunCountsTheStuckRunnerAndRestartsNothing(t *testing.T) {
	now := time.Date(2026, 9, 16, 17, 35, 0, 0, time.UTC)
	queue := runnerQueue(t, map[string]time.Time{"space-nova-1": now.Add(-20 * time.Minute)})
	restarter := &fakeRestarter{}
	out, _ := reapRunners(t, queue, ReapInput{
		Repo: "o/n", DryRun: true, Restarter: restarter,
		Runners: &fakeRunnerTable{running: 0, runners: []Runner{{Name: "space-nova-1", Busy: true}}},
		Now:     func() time.Time { return now },
	})
	if len(restarter.names) != 0 {
		t.Errorf("--dry-run restarted %v", restarter.names)
	}
	if !strings.Contains(out, "restarted=1") || !strings.Contains(out, "dry-run=true") {
		t.Errorf("REAP line = %q, want restarted=1 and dry-run=true", out)
	}
	if got, ok := readBusySince(queue)["space-nova-1"]; !ok || !got.Equal(now.Add(-20*time.Minute)) {
		t.Errorf("--dry-run rewrote the busy record: %v", readBusySince(queue))
	}
}

// TestReapWithoutARunnerTableIsUnchanged: a bench with no self-hosted runner has no jam,
// and the rule that does not run counts zero rather than refusing.
func TestReapWithoutARunnerTableIsUnchanged(t *testing.T) {
	out, _ := reapRunners(t, t.TempDir(), ReapInput{Now: func() time.Time { return time.Now().UTC() }})
	if !strings.Contains(out, "restarted=0") {
		t.Errorf("REAP line = %q, want restarted=0", out)
	}
}

// TestServiceRestarterRefusesARunnerWithNoRow: the real restarter never guesses a service.
// A wrong `systemctl restart` is worse than the jam it was meant to clear.
func TestServiceRestarterRefusesARunnerWithNoRow(t *testing.T) {
	queue := t.TempDir()
	err := ServiceRestarter{Queue: queue}.Restart("space-nova-1")
	if err == nil || !strings.Contains(err.Error(), RunnerServicesFile) {
		t.Fatalf("error = %v, want a refusal naming %s", err, RunnerServicesFile)
	}

	// And with a row, each kind is the command that machine's runners take.
	body := "space-nova-1\tspace\tsystemd\tactions.runner.mas-bandwidth-nova-tools.space-nova-1\n" +
		"studio-nova-2\t-\tsvc\t/Users/glenn/runner-nova-tools-2\n"
	if err := os.WriteFile(filepath.Join(queue, RunnerServicesFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := ReadRunnerServices(queue)
	if err != nil {
		t.Fatal(err)
	}
	if got := services["space-nova-1"]; got.Kind != "systemd" || got.Host != "space" {
		t.Errorf("space-nova-1 = %+v, want the systemd unit on space", got)
	}
	if got := services["studio-nova-2"]; got.Kind != "svc" || got.Host != "-" {
		t.Errorf("studio-nova-2 = %+v, want the svc.sh directory on this machine", got)
	}
}
