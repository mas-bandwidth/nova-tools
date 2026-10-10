package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// A card that works to its wall and is killed there can have committed real work and written
// no RESULT.md (fault 9, 2026-10-10: children on two direct routes; once a commit was pushed and nothing
// reported it). The
// card is told to finish at WallFinishShare of its wall (member.WallText); this is the
// backstop for the card that does not: at the deadline, a job that holds commits past its base
// and published no report gets one written FOR it, so the work is named and the harvester
// pushes it instead of scoring a silent `no-result`.

// DeadlineRun is what the decision is made from, all of it known when the child is gone.
type DeadlineRun struct {
	Deadlined bool // the deadline timer, not the child, the idle watch or a TERM, ended the run
	Published bool // a result was found where the gather looks for one (FindCardResult)
	Commits   int  // commits ./repo holds past its base, 0 when it holds none or is no repository
}

// DeadlineReports reports whether a deadline-killed run is given a report. ALL THREE are
// necessary, and each is a card this must not claim: a run the deadline did not end is some
// other end with its own report; a card that published owns its report and the machinery never
// overwrites it; a card with no commit has nothing to report, and its absence of a result is
// still the model's (`no-result`).
func DeadlineReports(run DeadlineRun) bool {
	return run.Deadlined && !run.Published && run.Commits > 0
}

// DeadlineResultPrefix opens line 1 of the report. It is a BLOCKED report like the idle end's:
// no findings head, so it is scored `plan-only` and never `ok` or `clean` -- a report the
// machinery wrote is never counted as work a worker did.
const DeadlineResultPrefix = "RESULT: BLOCKED "

// WriteDeadlineResult writes the report for a deadline-killed job that committed and published
// none, when DeadlineReports says so. It refuses to overwrite a result that exists, and says
// who wrote it on its own line. The report names the branch, the commit count and the head, so
// the work the card left in ./repo can be found and pushed. It returns the path and whether it
// wrote.
func WriteDeadlineResult(jobDir, task string, deadlined bool) (string, bool, error) {
	_, published := FindCardResult(jobDir)
	branch, n, _ := repoCommits(jobDir)
	if !DeadlineReports(DeadlineRun{Deadlined: deadlined, Published: published, Commits: n}) {
		return "", false, nil
	}
	head := gitOut(filepath.Join(jobDir, "repo"), "rev-parse", "HEAD")
	dest := filepath.Join(jobDir, BlockedResultName)
	body := strings.Join([]string{
		DeadlineResultPrefix + oneline.Field(task),
		fmt.Sprintf("deadline: %s was ended at its deadline with %d commit(s) on branch %s (head %s) in ./repo and no RESULT.md of its own; the work is committed there, not verified, and is for the harvester to find",
			oneline.Field(task), n, oneline.Field(branch), oneline.Field(dashOr(head))),
		"written-by: nova-swarm native (the card published no report of its own)",
		"",
	}, "\n")
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		return "", false, err
	}
	return dest, true, nil
}
