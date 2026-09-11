package swarm

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A JOB REPORTS EXACTLY ONCE: one RESULT.md, one RUN line, and never a second notification.
// Children that spawned background subtasks stopped to wait on them and reported twice
// (2026-09-11), which is what rule 11's group check and this one-line-per-job discipline
// close.

// finish ends one job: the group check, the classification, finalize in rule 12's order,
// the move, the slot, the one automatic re-queue, and the single line.
func (in RunInput) finish(r *running, retired map[int]bool, now time.Time) (string, string, string) {
	p := in.Pool
	sf, slotErr := p.ReadSlot(r.slot)

	// The group check of rule 11, made after the supervisor has gone: anything alive in the
	// job's own process group is a background subtask the prompt forbids.
	survivors := 0
	if slotErr == nil {
		if GroupAlive(sf.JobPgid) {
			survivors = 1
		}
		if Reap(sf.JobPgid, TerminateGrace) || Reap(sf.Pgid, TerminateGrace) {
			survivors++
		}
	}

	rec := ExitRecord{RC: -1}
	end := EndUnknown
	if slotErr == nil {
		var got ExitRecord
		if err := ReadJSON(ExitPath(r.jobDir), &got); err == nil && got.Nonce == sf.Nonce {
			rec, end = got, got.End
			if end == "" {
				end = EndDone
			}
			if got.Survivors > survivors {
				survivors = got.Survivors
			}
		}
	}

	// RATE LIMITS ARE THE DISPATCHER'S BUSINESS. A provider's 429 is not a failed task:
	// hold the slot, wait the interval the provider named or --backoff, and retry the SAME
	// task once. The status is read from the harness's own output because POSIX truncates
	// 429 to 173, and the row records the code the provider named.
	limited := false
	if harnessLog, readLogErr := os.ReadFile(r.jobDir + "/harness.log"); readLogErr == nil {
		limited = RateLimited(harnessLog)
	}
	if limited {
		rec.RC, end = 429, EndFailed
	}

	raw, readErr := os.ReadFile(ResultPath(r.jobDir))
	report := Report{Class: ClassNoResult}
	if readErr == nil {
		report = ParseReport(raw)
	}
	unpublished := false
	if _, err := os.Stat(ResultPath(r.jobDir) + ".tmp"); err == nil {
		unpublished = true
	}
	refusals := CountRefusals(r.jobDir + "/harness.log")
	notesSent := countLines(NotePath(r.jobDir))
	notesRead := Dash
	if report.HasNotesRead {
		notesRead = strconv.Itoa(report.NotesRead)
	}
	findings := report.FindingCount()

	sc := r.sc
	if fresh, err := p.ReadSidecar(Running, sc.ID); err == nil {
		sc = fresh
	}
	if survivors > 0 && end == EndDone {
		end = EndViolation
	}
	dest := destinationFor(end, report.Class, rec.RC)
	sc.Class, sc.End, sc.RC, sc.Ended, sc.Notes = report.Class, end, rec.RC, Stamp(now), notesSent
	if survivors > 0 {
		sc.Violation = "background"
	}
	if report.Class == ClassMalformed {
		sc.Malformed = report.MalformedLine
	}
	_ = p.WriteSidecar(Running, sc)

	// Rule 7's one automatic re-queue is decided BEFORE the files move, because the new
	// task's text is the old task's text and it is read from where the old task still is.
	requeued := false
	switch {
	case end == EndKilled:
		requeued = in.requeue(sc, now)
	case limited && sc.Requeued < 1:
		in.waitBackoff(r.jobDir)
		requeued = in.retry429(sc, now)
	}

	fin, usagePath := in.settle(sc, r.jobDir, rec, end, now)
	_ = usagePath
	_ = fin

	// The slot is released in the SAME step that finalizes the job -- never on a timer and
	// never by a scan of directories. A slot whose child survived the kill is RETIRED for
	// the rest of the run: a data home that may still have a writer in it is not free.
	if survivors > 0 {
		// RULE 11 QUARANTINES THE RESULT (SPEC-SWARM.md:154), and rule 17's QUARANTINE is
		// about a slot FILE. What this does to the slot is neither: it RETIRES it for the
		// rest of the run, because a data home that may still have a writer in it is not
		// free. The two are counted apart now; whether a retired slot makes the run exit 1
		// is a sentence the spec does not have, and the PR body proposes one.
		retired[r.slot] = true
	} else {
		_ = p.Free(r.slot)
	}
	_ = p.Claim(sc.ID, Running, dest)

	after := trimDuration(now.Sub(r.started))
	budget := budgetWord(sc, rec)
	switch {
	case end == EndViolation:
		return fmt.Sprintf("RUN VIOLATION id=%s slot=%d background=%d dest=failed: a process of this job's group outlived it; one task is one process",
			oneline.Field(sc.ID), r.slot, survivors), EndViolation, dest
	case end == EndUnverifiable:
		return fmt.Sprintf("RUN BUDGET-UNVERIFIABLE id=%s slot=%d samples=3 findings=%d: %s",
			oneline.Field(sc.ID), r.slot, findings, oneline.Escape(oneline.Cap(rec.Reason, oneline.TailBytes))), EndUnverifiable, dest
	case end == EndBudget:
		return fmt.Sprintf("RUN BUDGET id=%s slot=%d spent=%d of=%d findings=%d",
			oneline.Field(sc.ID), r.slot, rec.Spent, sc.Tokens, findings), EndBudget, dest
	case end == EndKilled:
		// A CHILD THAT SURVIVED THE KILL is `RUN KILLED … survived=true`
		// (SPEC-SWARM.md:1055), and NOT rule 11's background violation: a worker reaped at
		// its deadline broke no rule, and a process that outlived the kill is a fact about
		// the kill. Before this both ends printed RUN VIOLATION over one another.
		return fmt.Sprintf("RUN KILLED id=%s slot=%d after=%s deadline=%s findings=%d unpublished=%t budget=%s survived=%t requeued=%t reaped=%d",
			oneline.Field(sc.ID), r.slot, after, trimDuration(r.deadline), findings, unpublished, budget,
			survivors > 0, requeued, sc.Reaped+1), EndKilled, dest
	case report.Class == ClassMalformed:
		return fmt.Sprintf("RUN MALFORMED id=%s slot=%d line=%d dest=failed",
			oneline.Field(sc.ID), r.slot, report.MalformedLine), EndFailed, dest
	}
	// WHAT THE HARNESS SAID, on the line, when there is nothing else to go on: a job with
	// no report or a non-zero exit is one a person has to diagnose, and its only diagnosis
	// was a file no verb printed and `reclaim` deleted (the audit, F5).
	said := ""
	if report.Class == ClassNoResult || rec.RC != 0 {
		if tail := HarnessTail(r.jobDir); tail != "" {
			said = " log=" + oneline.Escape(oneline.Cap(tail, oneline.TailBytes))
		}
	}
	return fmt.Sprintf("RUN DONE id=%s slot=%d rc=%d after=%s result=%s findings=%d refusals=%d notes=%d/%s unpublished=%t budget=%s dest=%s%s",
		oneline.Field(sc.ID), r.slot, rec.RC, after, oneline.Field(report.Class), findings, refusals,
		notesSent, oneline.Field(notesRead), unpublished, budget, dest, said), end, dest
}

// destinationFor is WHERE A JOB LANDS, and it is one rule for every path that ends a job:
// the dispatcher's own `finish`, and the start-up pass that recovers a dead dispatcher's
// job (rule 17). It was written twice and then only once -- the recovery path moved nothing
// at all, and every recovered job stayed in running/ with no slot (DeepSeek, 2026-09-11).
func destinationFor(end, class string, rc int) string {
	switch {
	case end == EndViolation, end == EndKilled, end == EndUnverifiable, end == EndUnknown, end == EndFailed:
		return Failed
	case class == ClassMalformed, class == ClassPlanOnly, class == ClassNoResult:
		return Failed
	case rc != 0:
		return Failed
	}
	return Done
}

// settle is finalize in rule 12's order: the usage file, then the report copy or its marker,
// and only then anything else.
func (in RunInput) settle(sc Sidecar, jobDir string, rec ExitRecord, end string, now time.Time) (Finalized, string) {
	usage, _ := ReadProviderUsage(in.Worker.DataHome(sc.Slot, sc.ID))
	fin, err := in.Pool.Finalize(Ending{
		Sidecar: sc, JobDir: jobDir, Provider: in.Worker.Provider, Model: in.Worker.Model,
		End: end, RC: rec.RC, Started: parseStamp(sc.Started, time.Time{}), Ended: now, Usage: usage,
	})
	if err != nil {
		fmt.Fprintf(in.Stderr, "FINALIZE REFUSED id=%s: %s\n", oneline.Field(sc.ID), oneline.Escape(redactedReason(err)))
	}
	return fin, fin.UsagePath
}

// requeue is rule 7's ONE automatic retry: a job reaped at its deadline runs once more, and
// only once. It closes the case where a worker was silent because the provider was, and
// never the case where the task was too big, which a second identical run would only prove
// twice.
func (in RunInput) requeue(sc Sidecar, now time.Time) bool {
	if sc.Reaped >= 1 {
		return false
	}
	text, err := in.Pool.Text(Running, sc.ID)
	if err != nil {
		return false
	}
	next := sc
	next.ID = NewID(now, sc.Label)
	next.From, next.Requeued, next.Reaped = sc.ID, 1, 1
	next.Job, next.Slot, next.Started, next.Ended, next.End, next.Class = "", 0, "", "", "", ""
	if err := in.Pool.Add(text, next); err != nil {
		return false
	}
	return true
}

// waitBackoff holds the slot for the interval a 429 asks for: the provider's own if its
// output names one, otherwise --backoff. A wait past the cap is capped, because the
// machinery never waits forever.
func (in RunInput) waitBackoff(jobDir string) {
	delay := Backoff(1, in.Backoff)
	if raw, err := os.ReadFile(jobDir + "/harness.log"); err == nil {
		if named, ok := ProviderRetryAfter(raw); ok {
			delay = named
		}
	}
	switch {
	case delay <= 0:
		return
	case delay > MaxBackoff:
		delay = MaxBackoff
	}
	time.Sleep(delay)
}

// retry429 is the rate-limit retry: the same task text as a new attempt, marked so a second
// 429 is final. Unlike rule 7's re-queue it leaves Reaped alone, because the retry may
// still be reaped at a deadline of its own.
func (in RunInput) retry429(sc Sidecar, now time.Time) bool {
	if sc.Requeued >= 1 {
		return false
	}
	text, err := in.Pool.Text(Running, sc.ID)
	if err != nil {
		return false
	}
	next := sc
	next.ID = NewID(now, sc.Label)
	next.From, next.Requeued = sc.ID, 1
	next.Job, next.Slot, next.Started, next.Ended, next.End, next.Class = "", 0, "", "", "", ""
	if err := in.Pool.Add(text, next); err != nil {
		return false
	}
	return true
}

// budgetWord is rule 13's ceiling and what was observed under it: a dash for no observation,
// a plus for a partial one, so a job that ran under an unobservable budget is visible as
// such and never reported as under budget.
func budgetWord(sc Sidecar, rec ExitRecord) string {
	if sc.Unmetered {
		return "unmetered"
	}
	switch {
	case !rec.Observed:
		return fmt.Sprintf("-/%d", sc.Tokens)
	case rec.Partial:
		return fmt.Sprintf("%d+/%d", rec.Spent, sc.Tokens)
	}
	return fmt.Sprintf("%d/%d", rec.Spent, sc.Tokens)
}

// remedy is RUN NOTE: EXACTLY ONE line, naming the one thing to do next.
func remedy(p *Pool, failed, killed, pending, quarantined int) string {
	switch {
	case quarantined > 0:
		return fmt.Sprintf("%d slot file(s) are quarantined and out of the map; end any survivor and remove the file: ls %s", quarantined, p.Path(Slots))
	case killed > 1:
		return "a worker was killed at its deadline twice; re-queue it with a smaller file budget: nova-swarm requeue --pool " + p.Dir + " --task <id> --task-file <file> --files <n> --tokens <n>"
	case failed > 0 || killed > 0:
		return "something failed; read it down in one line: nova-swarm triage --pool " + p.Dir
	case pending > 0:
		return fmt.Sprintf("%d task(s) are still pending; run again with more hours: nova-swarm run --pool %s --workers <n> --hours <h> --worker <file>", pending, p.Dir)
	}
	return "the pool drained: nova-swarm triage --pool " + p.Dir
}

func countLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	body := strings.TrimRight(string(raw), "\n")
	if body == "" {
		return 0
	}
	return strings.Count(body, "\n") + 1
}

func parseStamp(s string, fallback time.Time) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fallback
	}
	return t
}

// trimDuration prints a duration the way a person writes one, to the second.
func trimDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

func trimFloat(f float64) string {
	if f == math.Trunc(f) {
		return strconv.Itoa(int(f))
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
