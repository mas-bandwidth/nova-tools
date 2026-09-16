package pulse

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// fakeRunTable is the runs half a test drives: canned runs, canned jobs, the cancels
// recorded rather than made, and a per-call error script so a test can make the API fail
// once the way it failed on 2026-09-16.
type fakeRunTable struct {
	inProgress []RunView
	recent     []RunView
	jobs       map[int64][]JobView

	cancelled []int64
	cancelErr error

	// failJobs[runID] is how many times Jobs on that run fails before it answers.
	failJobs    map[int64]int
	failRecent  int
	failInProg  int
	jobsAsked   int
	recentAsked int
}

func (f *fakeRunTable) InProgressRuns(repo string) ([]RunView, error) {
	if f.failInProg > 0 {
		f.failInProg--
		return nil, errors.New("API rate limit exceeded")
	}
	return f.inProgress, nil
}

func (f *fakeRunTable) RecentRuns(repo string, limit int) ([]RunView, error) {
	f.recentAsked++
	if f.failRecent > 0 {
		f.failRecent--
		return nil, errors.New("502 Bad Gateway")
	}
	if limit < len(f.recent) {
		return f.recent[:limit], nil
	}
	return f.recent, nil
}

func (f *fakeRunTable) Jobs(repo string, runID int64) ([]JobView, error) {
	f.jobsAsked++
	if n := f.failJobs[runID]; n > 0 {
		f.failJobs[runID] = n - 1
		return nil, errors.New("502 Bad Gateway")
	}
	return f.jobs[runID], nil
}

func (f *fakeRunTable) ForceCancel(repo string, runID int64) error {
	if f.cancelErr != nil {
		return f.cancelErr
	}
	f.cancelled = append(f.cancelled, runID)
	return nil
}

// theDayOfTheJam is 2026-09-16: two Studio runners read busy, one of them genuinely working
// on run 100, the other holding a job of run 200 that the API still calls in_progress on a
// run it already calls completed. 300 is somebody else's finished run and 400 is a run
// whose job finished on the phantom runner -- neither is anybody's to cancel.
func theDayOfTheJam() (*fakeRunnerTable, *fakeRunTable) {
	runners := &fakeRunnerTable{runners: []Runner{
		{Name: "studio-nova-1", Status: "online", Busy: true},
		{Name: "studio-nova-2", Status: "online", Busy: true},
		{Name: "space-nova-1", Status: "online", Busy: false},
	}}
	runs := &fakeRunTable{
		inProgress: []RunView{{ID: 100, Status: "in_progress"}},
		recent: []RunView{
			{ID: 100, Status: "in_progress"},
			{ID: 200, Status: "completed"},
			{ID: 300, Status: "completed"},
			{ID: 400, Status: "completed"},
		},
		jobs: map[int64][]JobView{
			100: {{ID: 1, RunID: 100, Status: "in_progress", RunnerNm: "studio-nova-2"}},
			200: {{ID: 2, RunID: 200, Status: "in_progress", RunnerNm: "studio-nova-1"},
				{ID: 3, RunID: 200, Status: "completed", RunnerNm: "studio-nova-1"}},
			300: {{ID: 4, RunID: 300, Status: "completed", RunnerNm: "space-nova-1"}},
			400: {{ID: 5, RunID: 400, Status: "completed", RunnerNm: "studio-nova-1"}},
		},
	}
	return runners, runs
}

// TestPhantomsFindsTheBusyRunnerWithNoRunAndCancelsExactlyItsRuns is the two hours of
// 2026-09-16: a runner the API calls busy that no in-progress job names, and the run still
// holding its job, force-cancelled.
//
// The mutations that matter: calling every busy runner a phantom would take studio-nova-2,
// which is working, and cancel run 100, which is live work; scanning only the runs the API
// calls in_progress would find nothing at all, because the phantom's run reads completed;
// and cancelling every run a phantom's name appears on would take run 400, whose job on it
// has finished.
func TestPhantomsFindsTheBusyRunnerWithNoRunAndCancelsExactlyItsRuns(t *testing.T) {
	runners, runs := theDayOfTheJam()
	var out, errs bytes.Buffer

	exit := Phantoms(PhantomsInput{
		Repo: "mas-bandwidth/nova-tools", Runners: runners, Runs: runs,
		ForceCancel: true, Stdout: &out, Stderr: &errs,
	})
	if exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, out.String(), errs.String())
	}
	if len(runs.cancelled) != 1 || runs.cancelled[0] != 200 {
		t.Errorf("cancelled %v, want exactly [200] -- the run whose job still sits on the phantom", runs.cancelled)
	}
	line := strings.TrimSpace(out.String())
	if n := len(strings.Split(line, "\n")); n != 1 {
		t.Errorf("phantoms printed %d lines, want one PHANTOMS line:\n%s", n, line)
	}
	for _, want := range []string{"PHANTOMS repo=mas-bandwidth/nova-tools", "runners=3", "busy=2", "phantom=1", "cancelled=1", "force=true", "complete=true"} {
		if !strings.Contains(line, want) {
			t.Errorf("PHANTOMS line = %q, want %s", line, want)
		}
	}
}

// TestPhantomsWithoutForceCancelChangesNothing: the default is a report. A verb that
// cancels runs because it was asked to look is a verb nobody dares run.
func TestPhantomsWithoutForceCancelChangesNothing(t *testing.T) {
	runners, runs := theDayOfTheJam()
	var out, errs bytes.Buffer
	if exit := Phantoms(PhantomsInput{Repo: "o/n", Runners: runners, Runs: runs, Stdout: &out, Stderr: &errs}); exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, out.String(), errs.String())
	}
	if len(runs.cancelled) != 0 {
		t.Errorf("cancelled %v without --force-cancel", runs.cancelled)
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "phantom=1") || !strings.Contains(line, "cancelled=0") || !strings.Contains(line, "force=false") {
		t.Errorf("PHANTOMS line = %q, want phantom=1 cancelled=0 force=false", line)
	}
	if runs.recentAsked != 0 {
		t.Errorf("a report paged through %d pages of recent runs; the scan is --force-cancel's cost", runs.recentAsked)
	}
}

// TestPhantomsNeverCallsAWorkingBenchAPhantom: every runner busy and every one of them on a
// job of a run in progress. Nothing to find, nothing to cancel.
func TestPhantomsNeverCallsAWorkingBenchAPhantom(t *testing.T) {
	runners := &fakeRunnerTable{runners: []Runner{
		{Name: "space-nova-1", Busy: true}, {Name: "space-nova-2", Busy: true},
	}}
	runs := &fakeRunTable{
		inProgress: []RunView{{ID: 700, Status: "in_progress"}},
		jobs: map[int64][]JobView{700: {
			{RunID: 700, Status: "in_progress", RunnerNm: "space-nova-1"},
			{RunID: 700, Status: "queued", RunnerNm: "space-nova-2"},
		}},
	}
	var out, errs bytes.Buffer
	if exit := Phantoms(PhantomsInput{Repo: "o/n", Runners: runners, Runs: runs, ForceCancel: true, Stdout: &out, Stderr: &errs}); exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, out.String(), errs.String())
	}
	if len(runs.cancelled) != 0 {
		t.Fatalf("cancelled %v on a bench where every busy runner is working", runs.cancelled)
	}
	if got := strings.TrimSpace(out.String()); !strings.Contains(got, "phantom=0") {
		t.Errorf("PHANTOMS line = %q, want phantom=0", got)
	}
}

// TestPhantomsRetriesOnceThenReportsWhatWasScanned: the API errs under load, which is
// exactly when this verb is run. One retry, then the line says what it got through and
// says it is not complete -- a person looking at a jammed queue is better served by a
// partial answer named as partial than by a refusal.
func TestPhantomsRetriesOnceThenReportsWhatWasScanned(t *testing.T) {
	// One failure, then the truth: the scan completes and the run is cancelled.
	runners, runs := theDayOfTheJam()
	runs.failJobs = map[int64]int{200: 1}
	var out, errs bytes.Buffer
	if exit := Phantoms(PhantomsInput{Repo: "o/n", Runners: runners, Runs: runs, ForceCancel: true, Stdout: &out, Stderr: &errs}); exit != 0 {
		t.Fatalf("one transient failure was not retried: exit %d: %s%s", exit, out.String(), errs.String())
	}
	if len(runs.cancelled) != 1 || runs.cancelled[0] != 200 {
		t.Errorf("cancelled %v after one retry, want [200]", runs.cancelled)
	}

	// Twice, and the verb stops trying and says so, still on one line.
	runners, runs = theDayOfTheJam()
	runs.failRecent = 2
	out.Reset()
	errs.Reset()
	exit := Phantoms(PhantomsInput{Repo: "o/n", Runners: runners, Runs: runs, ForceCancel: true, Stdout: &out, Stderr: &errs})
	if exit != 1 {
		t.Errorf("exit %d after the API refused twice, want 1 (a partial scan, named)", exit)
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "complete=false") || !strings.Contains(line, "phantom=1") || !strings.Contains(line, "cancelled=0") {
		t.Errorf("PHANTOMS line = %q, want phantom=1 cancelled=0 complete=false", line)
	}
	if runs.recentAsked != 2 {
		t.Errorf("the recent runs were asked for %d times, want 2 (once, then one retry)", runs.recentAsked)
	}
}

// TestPhantomsRefusesRatherThanCancelOnAGuess: if the in-progress runs cannot be read, every
// busy runner would look like a phantom and a whole healthy bench would be cancelled. That
// reading is a refusal, twice over.
func TestPhantomsRefusesRatherThanCancelOnAGuess(t *testing.T) {
	runners, runs := theDayOfTheJam()
	runs.failInProg = 2
	var out, errs bytes.Buffer
	if exit := Phantoms(PhantomsInput{Repo: "o/n", Runners: runners, Runs: runs, ForceCancel: true, Stdout: &out, Stderr: &errs}); exit != 2 {
		t.Fatalf("exit %d, want 2: %s%s", exit, out.String(), errs.String())
	}
	if len(runs.cancelled) != 0 {
		t.Errorf("cancelled %v without knowing what was running", runs.cancelled)
	}
	if !strings.Contains(errs.String(), "PHANTOMS REFUSED") {
		t.Errorf("stderr = %q, want a PHANTOMS REFUSED line", errs.String())
	}

	// And an unreadable job list of a live run is the same caution: nothing under it is
	// called a phantom.
	runners, runs = theDayOfTheJam()
	runs.failJobs = map[int64]int{100: 2}
	out.Reset()
	errs.Reset()
	if exit := Phantoms(PhantomsInput{Repo: "o/n", Runners: runners, Runs: runs, ForceCancel: true, Stdout: &out, Stderr: &errs}); exit != 1 {
		t.Errorf("exit %d, want 1 (partial)", exit)
	}
	if len(runs.cancelled) != 0 {
		t.Errorf("cancelled %v while a live run's jobs were unreadable", runs.cancelled)
	}
	if got := strings.TrimSpace(out.String()); !strings.Contains(got, "phantom=0") || !strings.Contains(got, "complete=false") {
		t.Errorf("PHANTOMS line = %q, want phantom=0 and complete=false", got)
	}
}

// TestPhantomsRefusesWithoutARepo: the repository is the whole scope of the scan, and a
// guessed one is somebody else's runs.
func TestPhantomsRefusesWithoutARepo(t *testing.T) {
	runners, runs := theDayOfTheJam()
	var out, errs bytes.Buffer
	if exit := Phantoms(PhantomsInput{Runners: runners, Runs: runs, Stdout: &out, Stderr: &errs}); exit != 2 {
		t.Fatalf("exit %d, want 2", exit)
	}
	if !strings.Contains(errs.String(), "--repo") {
		t.Errorf("stderr = %q, want a refusal naming --repo", errs.String())
	}
}
