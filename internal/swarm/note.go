package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A JOB HAS A NOTE FILE, AND ITS REPORT COUNTS THE NOTES IT READ (rule 10).
//
// On 2026-09-11 a running child could not be redirected: there was no path a message could
// take, and the only thing that could be done to a child doing the wrong thing was to kill
// it. So every job directory holds <job>/note, created empty before the worker starts and
// named in the prompt; the coordinator appends to it, one line per note, stamped by the
// tool; nothing else writes it.
//
// The COUNT is mandatory, because a job that ignored a note cannot be told apart from one
// that got none, and a coordinator who cannot tell the difference cannot redirect anything.
//
// A NOTE IS DATA TO THE WORKER AND NEVER AN INSTRUCTION TO THIS TOOL: nothing here reads a
// note back, and nothing in this package branches on what one says.
func AppendNote(jobDir, text string, now time.Time) (int, error) {
	path := NotePath(jobDir)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return 0, fmt.Errorf("the note file %s could not be opened: %s", path, redactedReason(err))
	}
	// One note is ONE LINE, whatever the caller wrote: a note holding a newline would be
	// two notes to a worker counting them, and the count is what rule 10 rests on.
	line := fmt.Sprintf("%s %s\n", Stamp(now), oneline.Escape(oneline.Cap(text, oneline.TailBytes)))
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return 0, fmt.Errorf("the note could not be appended to %s: %s", path, redactedReason(err))
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return countLines(path), nil
}

// FinalizeByHand is `finalize --task <id>`: the verb for a person, for one ended job whose
// runner died before doing it. It is REFUSED while the job's process group is alive, and it
// is a no-op with FINALIZE OK when the file already exists.
func FinalizeByHand(p *Pool, id string, now time.Time) (int, string) {
	sc, found := p.FindAnywhere(id)
	if !found {
		return 1, fmt.Sprintf("FINALIZE REFUSED id=%s: no such task in %s", oneline.Field(id), oneline.Field(p.Dir))
	}
	if sc.Job == "" {
		return 1, fmt.Sprintf("FINALIZE REFUSED id=%s: this task has no job directory, so it never launched", oneline.Field(id))
	}
	var pr PidRecord
	if err := ReadJSON(PidPath(sc.Job), &pr); err == nil {
		identify(pr.Pid, pr.PidStarted)
		identify(pr.JobPgid, pr.JobStarted)
		if Alive(pr.Pid) || GroupAlive(pr.JobPgid) {
			return 1, fmt.Sprintf("FINALIZE REFUSED id=%s: this job's process group is still alive; end it first, or let `nova-swarm run` adopt it", oneline.Field(id))
		}
	}
	rec := ExitRecord{RC: -1}
	end := EndUnknown
	if err := ReadJSON(ExitPath(sc.Job), &rec); err == nil {
		end = rec.End
		if end == "" {
			end = EndDone
		}
	}
	// A finalize by hand has no worker description to name a source, and the job's own
	// database is the only source there is: one that is not there reports nothing, and the
	// row is written with dashes rather than not written at all.
	usage, _ := ReadProviderUsage(UsageOpenCode, filepath.Join(sc.Job, "data"))
	fin, err := p.Finalize(Ending{
		Sidecar: sc, JobDir: sc.Job, End: end, RC: rec.RC,
		Started: parseStamp(sc.Started, time.Time{}), Ended: now, Usage: usage,
	})
	if err != nil {
		return 2, fmt.Sprintf("FINALIZE REFUSED id=%s: %s", oneline.Field(id), oneline.Err(err))
	}
	return 0, fmt.Sprintf("FINALIZE OK id=%s usage=%s existed=%t", oneline.Field(id), oneline.Field(fin.UsagePath), fin.UsageExisted)
}

// FindAnywhere finds a task's sidecar in whichever state it sits.
func (p *Pool) FindAnywhere(id string) (Sidecar, bool) { return p.findSidecar(id) }
