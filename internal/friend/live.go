package friend

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The beat names the jobs she runs (the seat's finding of 2026-10-07: the daemon's beat
// carried no --running and no --working, so the server never marked a friend's cards
// working and the tick's friend-take rule took them back while her lanes were mid-card).
// Every beat carries --running <job,...>, --working <n> and --width <n> from the daemon's
// own bookkeeping (BeatReport, Daemon.Report): the cards its lanes hold until their
// RESULT.md or REPORT.md is there or the lane ends them, and, for a harness whose friend
// runs children outside the daemon (a session, a runner script), the jobs whose lane mark
// says a lane runs them (jobs/<job>/LANE, fresh) and the jobs a runner's pid file says run
// (runner/<job>.pid beside or above the working directory, the one-shot runner scripts'
// marker, live while its pid answers) that have no REPORT.md yet.

// BeatReport is what the daemon's beat says of its work: the jobs live now, how many, the
// width the row gave, and the build this daemon runs.
type BeatReport struct {
	Running []string
	Working int
	Width   int
	Build   string
}

// RunnerDir is the one-shot runner scripts' state directory, beside the working directory
// or inside it: <job>.started, <job>.pid (live), logs/.
const RunnerDir = "runner"

// Report is the daemon's beat report as the loop set it before the beat: read inside Beat,
// on the loop's own goroutine, never beside it.
func (d *Daemon) Report() BeatReport { return d.report }

// liveJobs is every job live now, sorted, each once: the lanes' cards, the started cards
// whose lane is gone, and what the marks and pid files under Dir say (LiveJobsOn).
func (l *loop) liveJobs(now time.Time) []string {
	d, s := l.d, l.lanes
	seen := map[string]bool{}
	var jobs []string
	add := func(job string) {
		if job != "" && !seen[job] {
			seen[job] = true
			jobs = append(jobs, job)
		}
	}
	for _, ln := range s.lanes {
		if ln.card != nil {
			add(filepath.Base(ln.card.Outbox))
		}
	}
	for job := range s.state.Started {
		add(job)
	}
	for _, job := range LiveJobsOn(d.Dir, now, d.Alive) {
		add(job)
	}
	slices.Sort(jobs)
	return jobs
}

// LiveJobsOn is the jobs under dir that something outside the daemon runs: each job whose
// lane mark says a lane runs it and is fresh (LaneMarkStale), and each job a runner pid
// file names (dir/runner/<job>.pid, dir/../runner/<job>.pid) whose pid alive says runs,
// while outbox/<job>/REPORT.md is not there. alive nil reads no pid file. Sorted, each once.
func LiveJobsOn(dir string, now time.Time, alive func(pid int) bool) []string {
	if dir == "" {
		return nil
	}
	seen := map[string]bool{}
	var jobs []string
	if entries, err := os.ReadDir(filepath.Join(dir, "jobs")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			job := e.Name()
			if m, found := ReadLaneMark(dir, job); found && !m.Ended && !m.At.IsZero() && now.Sub(m.At) < LaneMarkStale && !reported(dir, job) {
				seen[job] = true
				jobs = append(jobs, job)
			}
		}
	}
	if alive != nil {
		for _, runner := range []string{filepath.Join(dir, RunnerDir), filepath.Join(filepath.Dir(dir), RunnerDir)} {
			entries, err := os.ReadDir(runner)
			if err != nil {
				continue
			}
			for _, e := range entries {
				job, ok := strings.CutSuffix(e.Name(), ".pid")
				if !ok || e.IsDir() || seen[job] {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(runner, e.Name()))
				if err != nil {
					continue
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
				if err != nil || pid <= 0 || !alive(pid) || reported(dir, job) {
					continue
				}
				seen[job] = true
				jobs = append(jobs, job)
			}
		}
	}
	slices.Sort(jobs)
	return jobs
}

// reported says the job's REPORT.md is in her outbox: the card is finished, whoever ran it.
func reported(dir, job string) bool {
	return exists(filepath.Join(dir, "outbox", job, "REPORT.md"))
}
