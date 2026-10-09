package friend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// NudgeDebtFile is the durable ledger of a batch work nudge. The loop's
// memory is not restart-safe. A record's phase decides whether a restart may
// tell the session to start again.
//
//	pending    staged or otherwise owed, not yet handed to the session; replay
//	deferred   the session explicitly did not take the turn; replay
//	inflight   delivery began and the outcome is unknown; hold, do not guess
//	accepted   tombstone written before any remove; do not replay
//	refused     an unparsable line, kept so a restart can see it; do not replay
//	superseded  a batch present exited 0 and named this job; do not replay. Not a native start.
//
// jobs/<job> exists after Stage and before native delivery, so it is not
// proof the work started. A lane claim, REPORT.md, or RESULT.md is.
const NudgeDebtFile = "nudge-debt.json"

const (
	nudgePending    = "pending"
	nudgeInflight   = "inflight"
	nudgeDeferred   = "deferred"
	nudgeAccepted   = "accepted"
	nudgeRefused    = "refused"
	nudgeSuperseded = "superseded"
)

type nudgeRecord struct {
	Attempt int      `json:"attempt"`
	Phase   string   `json:"phase"`
	Briefs  []string `json:"briefs"`
}

type nudgeLedger struct {
	NextAttempt int           `json:"nextAttempt"`
	Records     []nudgeRecord `json:"records"`
}

func (g nudgeLedger) clone() nudgeLedger {
	out := g
	out.Records = nil
	for _, r := range g.Records {
		r.Briefs = append([]string{}, r.Briefs...)
		out.Records = append(out.Records, r)
	}
	return out
}

func (l *loop) nudgeDebtPath() string {
	return filepath.Join(l.d.Dir, NudgeDebtFile)
}

// jobFromBriefLine reads the job out of an inbox line: inbox/<job>/BRIEF.md (...).
func jobFromBriefLine(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "inbox/")
	if !ok {
		return "", false
	}
	job, _, ok := strings.Cut(rest, "/BRIEF.md")
	if !ok || !validJob(job) || strings.Contains(job, "/") {
		return "", false
	}
	return job, true
}

func validateNudgeLedger(g nudgeLedger) error {
	if g.NextAttempt < 0 || (len(g.Records) > 0 && g.NextAttempt < 1) {
		return errors.New("nudge debt nextAttempt")
	}
	seen := map[int]bool{}
	maxAttempt := 0
	for _, r := range g.Records {
		if r.Attempt < 1 || seen[r.Attempt] {
			return errors.New("nudge debt attempt")
		}
		seen[r.Attempt] = true
		if r.Attempt > maxAttempt {
			maxAttempt = r.Attempt
		}
		switch r.Phase {
		case nudgePending, nudgeInflight, nudgeDeferred, nudgeAccepted, nudgeRefused, nudgeSuperseded:
		default:
			return errors.New("nudge debt phase")
		}
		if len(r.Briefs) == 0 {
			return errors.New("nudge debt briefs")
		}
		for _, line := range r.Briefs {
			if _, ok := jobFromBriefLine(line); !ok && r.Phase != nudgeRefused {
				return errors.New("nudge debt brief")
			}
			if r.Phase == nudgeRefused && strings.TrimSpace(line) == "" {
				return errors.New("nudge debt brief")
			}
		}
	}
	if maxAttempt > 0 && g.NextAttempt <= maxAttempt {
		return errors.New("nudge debt nextAttempt")
	}
	return nil
}

// briefFinished is evidence the work was claimed or finished. A jobs/<job>
// directory is not that evidence: Stage creates it before native delivery.
func (l *loop) briefFinished(job string) bool {
	if l.lanes != nil {
		if _, started := l.lanes.state.Started[job]; started {
			return true
		}
		for _, ln := range l.lanes.lanes {
			if ln != nil && ln.card != nil && filepath.Base(ln.card.Outbox) == job {
				return true
			}
		}
	}
	dir := l.d.Dir
	return exists(filepath.Join(dir, "outbox", job, "REPORT.md")) ||
		exists(filepath.Join(dir, "outbox", job, "RESULT.md"))
}

func (l *loop) blockNudgeDebt(now time.Time, err error) {
	if l.nudgeDebtBlocked {
		return
	}
	l.nudgeDebtBlocked = true
	if l.d.Record != nil {
		l.d.Record(fmt.Sprintf("%s nudge debt: blocked, file left unchanged: %s", now.UTC().Format(time.RFC3339), err.Error()))
	}
}

// loadNudgeLedger reads the ledger once. A read or parse failure blocks work
// nudges and does not overwrite the file. Missing is an empty ledger.
func (l *loop) loadNudgeLedger(now time.Time) bool {
	if l.nudgeDebtBlocked {
		return false
	}
	if l.nudgeDebtLoaded {
		return true
	}
	if l.d.Dir == "" {
		l.nudgeDebtLoaded = true
		l.nudgeLedger.NextAttempt = 1
		return true
	}
	var led nudgeLedger
	found, err := read(l.nudgeDebtPath(), &led)
	if err != nil {
		l.blockNudgeDebt(now, err)
		return false
	}
	if !found {
		l.nudgeDebtLoaded = true
		l.nudgeLedger.NextAttempt = 1
		return true
	}
	if err := validateNudgeLedger(led); err != nil {
		l.blockNudgeDebt(now, err)
		return false
	}
	l.nudgeLedger = led
	l.nudgeDebtLoaded = true
	if led.NextAttempt-1 > l.workAttempt {
		l.workAttempt = led.NextAttempt - 1
	}
	return true
}

func (l *loop) saveNudgeLedger() error {
	if l.nudgeDebtBlocked {
		return errors.New("nudge debt blocked")
	}
	if l.d.Dir == "" {
		return errors.New("nudge debt has no directory")
	}
	if err := validateNudgeLedger(l.nudgeLedger); err != nil {
		return err
	}
	return write(l.nudgeDebtPath(), l.nudgeLedger)
}

func (l *loop) nudgeJobKnown(job string) bool {
	for _, r := range l.nudgeLedger.Records {
		for _, line := range r.Briefs {
			if got, ok := jobFromBriefLine(line); ok && got == job {
				return true
			}
		}
	}
	return false
}

// appendNudgeRecord adds one record and saves it. A failed save restores the
// previous ledger. pending traces brief_staged. refused does not.
func (l *loop) appendNudgeRecord(phase string, briefs []string, now time.Time) error {
	if len(briefs) == 0 {
		return nil
	}
	if !l.loadNudgeLedger(now) {
		return errors.New("nudge debt blocked")
	}
	saved := l.nudgeLedger.clone()
	attempt := l.nudgeLedger.NextAttempt
	if attempt < 1 {
		attempt = 1
	}
	l.nudgeLedger.Records = append(l.nudgeLedger.Records, nudgeRecord{Attempt: attempt, Phase: phase, Briefs: append([]string{}, briefs...)})
	l.nudgeLedger.NextAttempt = attempt + 1
	if err := l.saveNudgeLedger(); err != nil {
		l.nudgeLedger = saved
		return err
	}
	if attempt > l.workAttempt {
		l.workAttempt = attempt
	}
	if phase == nudgePending {
		l.traceDeferred(attempt, "brief_staged", l.d.machineWord(), now)
	}
	return nil
}

// persistRefused writes unparsable lines as refused. They are not replayed.
// False leaves them in memory. A line already in the ledger is evidence enough.
func (l *loop) persistRefused(lines []string, now time.Time) bool {
	var fresh []string
	for _, line := range lines {
		if !l.nudgeLineKnown(line) {
			fresh = append(fresh, line)
		}
	}
	if len(fresh) > 0 {
		if err := l.appendNudgeRecord(nudgeRefused, fresh, now); err != nil {
			if l.d.Record != nil {
				l.d.Record(fmt.Sprintf("%s nudge debt: refusal not durable, lines kept in memory: %s", now.UTC().Format(time.RFC3339), err.Error()))
			}
			return false
		}
	}
	if !l.nudgeBadBriefSaid && l.d.Record != nil {
		l.nudgeBadBriefSaid = true
		l.d.Record(fmt.Sprintf("%s nudge debt: %d unparsable brief line(s) kept as refused", now.UTC().Format(time.RFC3339), len(lines)))
	}
	return true
}

// rememberStagedPending writes a staged job's brief as pending. It refuses a
// job whose JOB.md is not there, so an early ledger cannot nudge an unstaged
// job. Nil means the line is durable, already known, or finished.
func (l *loop) rememberStagedPending(line string, now time.Time) error {
	// Load before any known-row decision. A one-shot restart has an empty
	// memory ledger until this read. Deciding first would append a second
	// pending row on top of an accepted or inflight tombstone.
	if !l.loadNudgeLedger(now) {
		return errors.New("nudge debt blocked")
	}
	job, ok := jobFromBriefLine(line)
	if !ok {
		if l.persistRefused([]string{line}, now) {
			return nil
		}
		return errors.New("nudge debt refusal not durable")
	}
	if !Staged(l.d.Dir, job) {
		return errors.New("nudge debt job is not staged")
	}
	if l.nudgeJobKnown(job) || l.briefFinished(job) {
		return nil
	}
	return l.appendNudgeRecord(nudgePending, []string{line}, now)
}

// briefWaiting is a written brief the session has not been given. The authority
// is the ledger's replayable rows plus a dealt line not saved yet. owedBriefs
// is a cache and is not read here: it can outlive an accepted or superseded
// nudge and would open a present forever. A finished job is not replayable.
func (l *loop) briefWaiting(now time.Time) bool {
	if len(l.dealt) > 0 {
		return true
	}
	if !l.loadNudgeLedger(now) {
		return false
	}
	for _, r := range l.nudgeLedger.Records {
		if len(l.replayableBriefs(r)) > 0 {
			return true
		}
	}
	return false
}

// supersedeCoveredNudges retires pending briefs whose job the present named.
// One save covers every such brief. A failed save restores the ledger and
// retires nothing. Inflight, deferred, accepted, refused, and any pending
// job the present did not name stay. This is not native_start.
//
// Restart is at-least-once, not exactly-once. A crash after the present's
// exit 0 and before this save leaves the pending row with the same attempt
// id. The next process replays that row. The replay can repeat the advisory.
// It is not a second native start, and this path does not add a protocol
// to prevent it. A nonzero exit, a stopped turn, and SessionRefused do not
// retire the row.
func (l *loop) supersedeCoveredNudges(covered []string, now time.Time) error {
	want := map[string]bool{}
	for _, job := range covered {
		if job != "" {
			want[job] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	if !l.loadNudgeLedger(now) {
		return errors.New("nudge debt blocked")
	}
	saved := l.nudgeLedger.clone()
	changed := false
	var extra []nudgeRecord
	for i := range l.nudgeLedger.Records {
		r := &l.nudgeLedger.Records[i]
		if r.Phase != nudgePending {
			continue
		}
		var keep, drop []string
		for _, line := range r.Briefs {
			job, ok := jobFromBriefLine(line)
			if ok && want[job] {
				drop = append(drop, line)
				continue
			}
			keep = append(keep, line)
		}
		if len(drop) == 0 {
			continue
		}
		changed = true
		if len(keep) == 0 {
			r.Phase = nudgeSuperseded
			continue
		}
		r.Briefs = keep
		attempt := l.nudgeLedger.NextAttempt
		if attempt < 1 {
			attempt = 1
		}
		l.nudgeLedger.NextAttempt = attempt + 1
		extra = append(extra, nudgeRecord{Attempt: attempt, Phase: nudgeSuperseded, Briefs: drop})
	}
	if !changed {
		return nil
	}
	l.nudgeLedger.Records = append(l.nudgeLedger.Records, extra...)
	if err := l.saveNudgeLedger(); err != nil {
		l.nudgeLedger = saved
		return err
	}
	return nil
}

// retryPresentCoverage writes a coverage set whose first save failed.
// Nil coverHold, and a landed save, are not an error.
func (l *loop) retryPresentCoverage(now time.Time) error {
	if len(l.coverHold) == 0 {
		return nil
	}
	if err := l.supersedeCoveredNudges(l.coverHold, now); err != nil {
		return err
	}
	l.coverHold = nil
	return nil
}

func coverUnion(have, add []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, job := range append(append([]string{}, have...), add...) {
		if job == "" || seen[job] {
			continue
		}
		seen[job] = true
		out = append(out, job)
	}
	return out
}

func coverWithout(have, drop []string) []string {
	gone := map[string]bool{}
	for _, job := range drop {
		gone[job] = true
	}
	var out []string
	for _, job := range have {
		if job == "" || gone[job] {
			continue
		}
		out = append(out, job)
	}
	return out
}

// flushStageDealt moves a staged job's held line into the ledger before the
// map forgets it. One-shot and batch both record it. Only batch delivers it.
func (l *loop) flushStageDealt(now time.Time) {
	if l.d == nil || len(l.d.stageDealt) == 0 {
		return
	}
	for job, line := range l.d.stageDealt {
		if err := l.rememberStagedPending(line, now); err != nil {
			continue
		}
		delete(l.d.stageDealt, job)
	}
}

// reconstructStagedDebt finds a staged job whose brief is on disk and whose
// nudge was never written. A crash between JOB.md and the ledger uses this.
// A job that is not staged is left out.
func (l *loop) reconstructStagedDebt(now time.Time) error {
	entries, err := os.ReadDir(filepath.Join(l.d.Dir, "inbox"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var fresh []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		job := e.Name()
		line := "inbox/" + job + "/BRIEF.md"
		if _, ok := jobFromBriefLine(line); !ok || !Staged(l.d.Dir, job) || l.briefFinished(job) || l.nudgeJobKnown(job) {
			continue
		}
		if !exists(filepath.Join(l.d.Dir, "inbox", job, "BRIEF.md")) {
			continue
		}
		fresh = append(fresh, line)
	}
	sort.Strings(fresh)
	return l.appendNudgeRecord(nudgePending, fresh, now)
}

func (l *loop) nudgeLineKnown(line string) bool {
	for _, r := range l.nudgeLedger.Records {
		for _, have := range r.Briefs {
			if have == line {
				return true
			}
		}
	}
	return false
}

// persistStagedBriefs writes newly dealt lines as pending before anything
// clears them, including while another batch turn is busy. A failed write
// leaves the lines in memory and does not launch them.
func (l *loop) persistStagedBriefs(now time.Time) {
	if len(l.dealt) == 0 || !l.loadNudgeLedger(now) {
		return
	}
	var fresh, bad []string
	for _, line := range l.dealt {
		job, ok := jobFromBriefLine(line)
		if !ok {
			bad = append(bad, line)
			continue
		}
		// Same job, any spelling of the line. A reconstructed "inbox/<job>/BRIEF.md"
		// and the annotated dealt line are one nudge. A finished job is not owed.
		if l.nudgeJobKnown(job) || l.briefFinished(job) {
			continue
		}
		fresh = append(fresh, line)
	}
	if len(fresh) == 0 {
		if len(bad) == 0 || l.persistRefused(bad, now) {
			l.dealt = nil
		} else {
			l.dealt = bad
		}
		return
	}
	if err := l.appendNudgeRecord(nudgePending, fresh, now); err != nil {
		if l.d.Record != nil {
			l.d.Record(fmt.Sprintf("%s nudge debt: pending not durable, lines kept in memory: %s", now.UTC().Format(time.RFC3339), err.Error()))
		}
		return
	}
	if len(bad) == 0 || l.persistRefused(bad, now) {
		l.dealt = nil
		return
	}
	l.dealt = append([]string{}, bad...)
}

// markNudgePhase persists a phase before the caller clears memory or launches.
// On a write failure the in-memory ledger is restored and the caller must not
// clear or launch. inflight is uncertain and is not a start.
func (l *loop) markNudgePhase(attempt int, phase string, briefs []string, now time.Time) (int, error) {
	if !l.loadNudgeLedger(now) {
		return 0, errors.New("nudge debt blocked")
	}
	saved := l.nudgeLedger.clone()
	if attempt < 1 {
		attempt = l.nudgeLedger.NextAttempt
		if attempt < 1 {
			attempt = 1
		}
		l.nudgeLedger.NextAttempt = attempt + 1
	}
	found := false
	for i := range l.nudgeLedger.Records {
		if l.nudgeLedger.Records[i].Attempt != attempt {
			continue
		}
		// Partial delivery splits one current subset from retained debt in the
		// same durable write. Removed/unknown jobs are never marked accepted.
		if phase == nudgeInflight && len(briefs) > 0 {
			var keep []string
			for _, line := range l.nudgeLedger.Records[i].Briefs {
				if !slices.Contains(briefs, line) {
					keep = append(keep, line)
				}
			}
			if len(keep) > 0 {
				l.nudgeLedger.Records[i].Briefs = keep
				attempt = l.nudgeLedger.NextAttempt
				l.nudgeLedger.NextAttempt++
				break
			}
		}
		l.nudgeLedger.Records[i].Phase = phase
		if len(briefs) > 0 {
			l.nudgeLedger.Records[i].Briefs = append([]string{}, briefs...)
		}
		found = true
		break
	}
	if !found {
		if len(briefs) == 0 {
			l.nudgeLedger = saved
			return 0, errors.New("nudge debt attempt is not in the ledger")
		}
		if attempt >= l.nudgeLedger.NextAttempt {
			l.nudgeLedger.NextAttempt = attempt + 1
		}
		l.nudgeLedger.Records = append(l.nudgeLedger.Records, nudgeRecord{Attempt: attempt, Phase: phase, Briefs: append([]string{}, briefs...)})
	}
	if err := l.saveNudgeLedger(); err != nil {
		l.nudgeLedger = saved
		return 0, err
	}
	if attempt > l.workAttempt {
		l.workAttempt = attempt
	}
	return attempt, nil
}

func (l *loop) replayableBriefs(r nudgeRecord) []string {
	if r.Phase != nudgePending && r.Phase != nudgeDeferred {
		return nil
	}
	var out []string
	for _, line := range r.Briefs {
		job, ok := jobFromBriefLine(line)
		// A jobs/<job> directory without JOB.md is not a start. Stage creates
		// the directory before it publishes JOB.md. Dropping the brief here
		// would lose an owed nudge. A repo card that has not reached JOB.md
		// is still in stageDealt and is not in the ledger yet.
		if !ok || l.briefFinished(job) {
			continue
		}
		if !l.currentNudgeBrief(job) {
			continue // retain unavailable briefs in debt while other current work can proceed
		}
		out = append(out, line)
	}
	return out
}

// recoverNudgeDebt loads the ledger before inbox. It copies only pending and
// explicit-deferred lines whose work is not finished. Inflight stays held.
// A bad file blocks nudges and is not rewritten.
func (l *loop) recoverNudgeDebt(now time.Time) {
	if l.nudgeRecovered || l.nudgeDebtBlocked {
		return
	}
	if !l.loadNudgeLedger(now) {
		return
	}
	if err := l.reconstructStagedDebt(now); err != nil {
		if l.d.Record != nil {
			l.d.Record(fmt.Sprintf("%s nudge debt: staged jobs not durable yet: %s", now.UTC().Format(time.RFC3339), err.Error()))
		}
		return
	}
	l.nudgeRecovered = true
	var keep []string
	var held int
	for _, r := range l.nudgeLedger.Records {
		if r.Phase == nudgeInflight {
			held += len(r.Briefs)
			continue
		}
		keep = append(keep, l.replayableBriefs(r)...)
	}
	l.owedBriefs = keep
	for _, r := range l.nudgeLedger.Records {
		if lines := l.replayableBriefs(r); len(lines) > 0 {
			l.owedAttempt = r.Attempt
			break
		}
	}
	if l.d.Record == nil || (len(l.nudgeLedger.Records) == 0 && len(keep) == 0 && held == 0) {
		return
	}
	l.d.Record(fmt.Sprintf("%s deferred-start attempt=%d event=debt_recovered auth=%s kept=%d held_inflight=%d",
		now.UTC().Format(time.RFC3339), l.owedAttempt, l.d.machineWord(), len(keep), held))
}

// A configured server row is the source of assignment; recovered advisory
// debt cannot replace it. Unknown, failed refresh or retired brief holds debt.
func (l *loop) currentNudgeBrief(job string) bool {
	if l.d.Held == nil {
		return true
	}
	if !l.d.status.HeldKnown || l.d.status.InboxError != "" || !exists(filepath.Join(l.d.Dir, "inbox", job, "BRIEF.md")) {
		return false
	}
	for _, held := range l.d.heldCards {
		if held.Job == job {
			return true
		}
	}
	return false
}
