package friend

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// A card has one live lane (the night of 2026-10-05: the same card ran in two lanes at once
// more than once: a WHO-pinned audit dealt to one friend while another worked the same slot
// from her outbox, reruns of dead lanes beside a twin's lane, a friend's duplicate runners
// starting four jobs twice; the finish of one left the other running to no purpose). A lane
// claims the card's job before its first turn by its lane mark, jobs/<job>/LANE, created
// only when there is none (LaneMarkRunning: who runs it and when, refreshed while it runs);
// a daemon refuses to start a lane for a card whose mark names another lane running it, or
// whose row says another friend runs it (Daemon.Running, the beat's running list). When the
// card finishes or moves (its mark says another lane ended it, or it leaves her row with no
// report in her outbox), the daemon ends any lane of its own still running it, writes
// "ended: card finished by <who>" on the job's mark, and finishes nothing: the card's
// finish is the other lane's. A lane that ends its card writes the same line naming itself
// (docs/SPEC-FRIEND.md, one lane per card). The model is internal/friend/tla/OneLane.tla:
// OneLive and NoneLeft hold, and its reversed witness (MCOneLaneBrokenNoClaim.cfg, lanes
// that start without the mark, as before) breaks OneLive.

// LaneMarkFile is a job's lane mark, under jobs/<job>/.
const LaneMarkFile = "LANE"

// LaneMarkStale is how long a running mark stands unrefreshed: a lane refreshes its mark
// every LaneMarkEvery while its card runs, so one older than this is a lane gone with its
// daemon, and a new lane may claim the card.
const (
	LaneMarkEvery = 30 * time.Second
	LaneMarkStale = 5 * LaneMarkEvery
)

// The two words a lane mark begins with.
const (
	laneRunning = "running: "
	laneEnded   = "ended: card finished by "
)

// LaneMark is a job's lane mark as read: Who runs it (Ended false) or ended it (Ended
// true), and At, when a running mark was last refreshed.
type LaneMark struct {
	Who   string
	At    time.Time
	Ended bool
	RunID string
}

// LaneMarkRunning is the mark of a running lane: "running: <who> at <RFC3339>".
func LaneMarkRunning(who string, at time.Time) string {
	return laneRunning + who + " at " + at.UTC().Format(time.RFC3339) + "\n"
}

func LaneMarkRunningRun(who string, at time.Time, runID string) string {
	return laneRunning + who + " at " + at.UTC().Format(time.RFC3339) + " run " + runID + "\n"
}

// LaneMarkEnded is the mark of a finished card: "ended: card finished by <who>".
func LaneMarkEnded(who string) string { return laneEnded + who + "\n" }

// laneMarkPath is the job's lane mark in the friend's working directory dir.
func laneMarkPath(dir, job string) string {
	return filepath.Join(dir, "jobs", job, LaneMarkFile)
}

// ReadLaneMark is the job's lane mark; found is false when there is none or it says
// neither word.
func ReadLaneMark(dir, job string) (m LaneMark, found bool) {
	raw, err := os.ReadFile(laneMarkPath(dir, job))
	if err != nil {
		return LaneMark{}, false
	}
	line := strings.TrimSpace(string(raw))
	if who, ok := strings.CutPrefix(line, laneEnded); ok {
		return LaneMark{Who: who, Ended: true}, true
	}
	rest, ok := strings.CutPrefix(line, laneRunning)
	if !ok {
		return LaneMark{}, false
	}
	i := strings.LastIndex(rest, " at ")
	if i < 0 {
		return LaneMark{Who: rest}, true
	}
	atText, runID, _ := strings.Cut(rest[i+4:], " run ")
	at, err := time.Parse(time.RFC3339, atText)
	if err != nil {
		return LaneMark{Who: rest}, true
	}
	return LaneMark{Who: rest[:i], At: at, RunID: runID}, true
}

// bindRunLane records the immutable run ID before the child may execute.
func bindRunLane(dir, job, who, runID string, now time.Time) error {
	path := laneMarkPath(dir, job)
	_, err := withLaneLock(path, func() (string, error) {
		m, ok := ReadLaneMark(dir, job)
		if !ok || m.Ended || m.Who != who || (m.RunID != "" && m.RunID != runID) {
			return "", fmt.Errorf("lane owner changed before launch")
		}
		return "", atomicfile.WriteFile(path, []byte(LaneMarkRunningRun(who, now, runID)), 0o644)
	})
	return err
}

// heldBy is who the mark says holds the card against a lane who at now: the lane that ended
// it, or another lane whose running mark is fresh; "" when the card is free for who.
func (m LaneMark) heldBy(who string, now time.Time) string {
	switch {
	case m.Ended:
		return m.Who
	case m.Who == who, m.At.IsZero() && m.Who == "":
		return ""
	case !m.At.IsZero() && now.Sub(m.At) >= LaneMarkStale:
		return "" // a gone lane's: its daemon stopped refreshing it
	}
	return m.Who
}

// ClaimLane claims the job's card for the lane who at now: the mark is created when there is
// none (only one creator can win), taken over when the one there is stale, and kept when it
// is who's own. holder is the lane that holds it instead ("" when claimed).
func ClaimLane(dir, job, who string, now time.Time) (holder string, err error) {
	path := laneMarkPath(dir, job)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return withLaneLock(path, func() (string, error) { return claimLaneLocked(dir, job, who, now) })
}

func claimLaneLocked(dir, job, who string, now time.Time) (holder string, err error) {
	path := laneMarkPath(dir, job)
	mark := []byte(LaneMarkRunning(who, now))
	err = atomicfile.WriteFile(path, mark, 0o644, atomicfile.NoReplace())
	if err == nil || !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	m, _ := ReadLaneMark(dir, job)
	if h := m.heldBy(who, now); h != "" {
		return h, nil
	}
	if m.RunID != "" {
		if receipt, err := readRunReceipt(dir, job); err == nil && receipt.RunID == m.RunID &&
			runStillAlive(receipt.PID, receipt.Identity) {
			return m.Who, nil // stale daemon mark, but its run remains alive
		}
	}
	return "", atomicfile.WriteFile(path, mark, 0o644)
}

// transferLane gives a verified surviving run's old mark to the new daemon.
// A changed, ended, or foreign mark is never overwritten.
func transferLane(dir, job, runID, newWho string, now time.Time, processIdentity func(int) string, processAlive func(int) bool) (string, error) {
	path := laneMarkPath(dir, job)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return withLaneLock(path, func() (string, error) {
		m, ok := ReadLaneMark(dir, job)
		if !ok {
			return "", fmt.Errorf("lane mark missing")
		}
		if m.Ended || runID == "" || m.RunID != runID {
			return m.Who, nil
		}
		// A second daemon may start while the first still runs. The immutable
		// harness run alone does not prove its old lane owner is gone.
		pid, err := laneDaemonPID(m.Who)
		if err != nil || processIdentity == nil || processAlive == nil || processIdentity(pid) != "" || processAlive(pid) {
			return m.Who, nil
		}
		return "", atomicfile.WriteFile(path, []byte(LaneMarkRunningRun(newWho, now, runID)), 0o644)
	})
}

func laneDaemonPID(who string) (int, error) {
	_, tag, ok := strings.Cut(who, "(daemon ")
	if !ok {
		return 0, fmt.Errorf("lane mark has no daemon tag")
	}
	tag, ok = strings.CutSuffix(tag, ")")
	if !ok {
		return 0, fmt.Errorf("lane mark has no daemon tag")
	}
	pidText, _, ok := strings.Cut(tag, ".")
	if !ok {
		return 0, fmt.Errorf("lane mark has no daemon tag")
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("lane mark has invalid daemon pid")
	}
	return pid, nil
}

func refreshLane(dir, job, who string, now time.Time, write bool) (string, error) {
	path := laneMarkPath(dir, job)
	return withLaneLock(path, func() (string, error) {
		m, ok := ReadLaneMark(dir, job)
		if !ok {
			return "", fmt.Errorf("lane mark missing")
		}
		if m.Ended || m.Who != who {
			return m.Who, nil
		}
		if !write {
			return "", nil
		}
		if m.RunID != "" {
			return "", atomicfile.WriteFile(path, []byte(LaneMarkRunningRun(who, now, m.RunID)), 0o644)
		}
		return "", atomicfile.WriteFile(path, []byte(LaneMarkRunning(who, now)), 0o644)
	})
}

// endLaneMark writes "ended: card finished by <who>" on the job, unless its mark already
// says it ended.
func endLaneMark(dir, job, who string) error {
	path := laneMarkPath(dir, job)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := withLaneLock(path, func() (string, error) {
		if m, ok := ReadLaneMark(dir, job); ok && m.Ended {
			return "", nil
		}
		return "", atomicfile.WriteFile(path, []byte(LaneMarkEnded(who)), 0o644)
	})
	return err
}

// endOwnedLaneMark ends only this lane's mark. In particular, startup recovery
// must not overwrite a newer claimant's running mark while failing an old run.
func endOwnedLaneMark(dir, job, who string) error {
	path := laneMarkPath(dir, job)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := withLaneLock(path, func() (string, error) {
		m, ok := ReadLaneMark(dir, job)
		if ok && (m.Ended || m.Who != who) {
			return "", nil
		}
		return "", atomicfile.WriteFile(path, []byte(LaneMarkEnded(who)), 0o644)
	})
	return err
}

func endOtherMark(dir, job, oldWho, who string) error {
	path := laneMarkPath(dir, job)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := withLaneLock(path, func() (string, error) {
		m, ok := ReadLaneMark(dir, job)
		if ok && (m.Ended || m.Who == who || m.Who != oldWho) {
			return "", nil
		}
		return "", atomicfile.WriteFile(path, []byte(LaneMarkEnded(who)), 0o644)
	})
	return err
}

func releaseOwnedLane(dir, job, who string) error {
	path := laneMarkPath(dir, job)
	_, err := withLaneLock(path, func() (string, error) {
		m, ok := ReadLaneMark(dir, job)
		if !ok || m.Ended || m.Who != who {
			return "", nil
		}
		return "", os.Remove(path)
	})
	return err
}

// daemonSeq tells the daemons of one process apart in their lanes' names.
var daemonSeq atomic.Int64

// laneTag is the daemon's own tag in its lanes' names: its process and its run, so two
// daemons on one working directory (duplicate runners) never name the same lane.
func laneTag() string {
	return strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(daemonSeq.Add(1), 10)
}

// laneWho is lane n's name on a lane mark: "<friend> lane <n> (daemon <tag>)".
func (l *loop) laneWho(n int) string {
	return fmt.Sprintf("%s lane %d (daemon %s)", l.d.Friend, n, l.tag)
}

// runningElsewhere is the friend her row's running list says runs card c, when it is not
// her: the server's word on every friend's beat (Daemon.Running), keyed by card id or job.
func (d *Daemon) runningElsewhere(c Card) string {
	if d.Running == nil {
		return ""
	}
	running := d.Running()
	for _, k := range []string{c.ID, filepath.Base(c.Outbox)} {
		if who := running[k]; who != "" && who != d.Friend && who != "friend."+d.Friend {
			return who
		}
	}
	return ""
}

// laneHolder is who holds card c against a lane of this daemon at now, and how: her row's
// running list's other friend, else the lane its mark names running it or having ended it;
// "" when it is free.
func (l *loop) laneHolder(c Card, now time.Time) (who, how string) {
	if who := l.d.runningElsewhere(c); who != "" {
		return who, "runs it"
	}
	m, ok := ReadLaneMark(l.d.Dir, filepath.Base(c.Outbox))
	if !ok {
		return "", ""
	}
	if m.Ended {
		return m.Who, "ended it"
	}
	return m.heldBy("", now), "runs it" // any running lane is another's: this daemon's own are skipped by the hand
}

// refuseLane says once, while it stands, that a lane was refused card c because who runs it
// or ended it (how).
func (l *loop) refuseLane(n int, c Card, who, how string, now time.Time) {
	s, job := l.lanes, filepath.Base(c.Outbox)
	if s.refused[job] == who+" "+how {
		return
	}
	s.refused[job] = who + " " + how
	l.d.Record(fmt.Sprintf("%s lane %d: card %s refused: %s %s (one live lane per card)", now.UTC().Format(time.RFC3339), n, c.ID, who, how))
}

// oneLaneStep is each lane holding a card checked against its card's other lanes: a running
// mark refreshed every LaneMarkEvery; a lane whose mark says another lane ended the card or
// now runs it, or whose card left her row with no report in her outbox, is ended
// (endOtherLane). It runs before the lanes hand anything.
func (l *loop) oneLaneStep(now time.Time) {
	s, d := l.lanes, l.d
	for _, ln := range s.lanes {
		if ln.card == nil || ln.ended != "" {
			continue
		}
		c, job := *ln.card, filepath.Base(ln.card.Outbox)
		me := l.laneWho(ln.n)
		holder, err := refreshLane(d.Dir, job, me, now, now.Sub(ln.marked) >= LaneMarkEvery)
		if err != nil {
			d.Record(fmt.Sprintf("%s lane %d: card %s: the lane mark cannot be checked: %s", now.UTC().Format(time.RFC3339), ln.n, c.ID, oneLine(err.Error(), 300)))
			continue
		}
		if holder != "" {
			l.endOtherLane(ln, holder, now)
			continue
		}
		if who, gone := l.leftRow(c); gone {
			l.endOtherLane(ln, who, now)
			continue
		}
		if now.Sub(ln.marked) >= LaneMarkEvery {
			ln.marked = now
		}
	}
}

// leftRow says card c has left her row while the server has said what is on it, with no
// report in her outbox (a report there is her own lane's finish, ended by its own turn), and
// who it went to: the friend her row's running list names, else the server.
func (l *loop) leftRow(c Card) (who string, gone bool) {
	d := l.d
	if d.Held == nil || !d.status.HeldKnown || exists(c.Result()) || exists(c.Report()) {
		return "", false
	}
	job := filepath.Base(c.Outbox)
	for _, h := range d.heldCards {
		if h.Job == job {
			return "", false
		}
	}
	if who := d.runningElsewhere(c); who != "" {
		return who, true
	}
	return "the sprint server (the card left " + d.Friend + "'s row)", true
}

// endOtherLane ends lane ln's run of its card, which another lane finished or holds: its turn
// cancelled, "ended: card finished by <who>" written on the job, and the card set down
// unfinished by this lane when the turn comes back (laneDone).
func (l *loop) endOtherLane(ln *lane, who string, now time.Time) {
	d, c := l.d, *ln.card
	ln.ended = who
	words := ""
	job := filepath.Base(c.Outbox)
	if ln.t != nil && ln.t.adopted {
		st := l.lanes.state.Started[job]
		m, ok := ReadLaneMark(d.Dir, job)
		if ok && m.RunID == st.RunID && m.Who == who && !m.Ended {
			ln.t.cancel()
			d.Record(fmt.Sprintf("%s lane %d: card %s: tracking handed to %s for the same live run", now.UTC().Format(time.RFC3339), ln.n, c.ID, who))
			return
		}
		d.Record(fmt.Sprintf("%s lane %d: card %s: lane mark changed to %s; adopted run remains under watch until its group exits", now.UTC().Format(time.RFC3339), ln.n, c.ID, who))
		return
	}
	if err := endOtherMark(d.Dir, job, l.laneWho(ln.n), who); err != nil {
		words = fmt.Sprintf(" mark_error=%q", oneLine(err.Error(), 300))
	}
	if ln.t != nil && ln.t.cancel != nil {
		ln.t.cancel()
	}
	d.Record(fmt.Sprintf("%s lane %d: card %s ended: card finished by %s; its run is stopped and nothing is finished by this lane%s", now.UTC().Format(time.RFC3339), ln.n, c.ID, who, words))
	if ln.t == nil {
		l.setDown(ln, now)
	}
}

// setDown is a lane's card set down after another lane ended it: no longer started, never
// handed again, and the lane free.
func (l *loop) setDown(ln *lane, now time.Time) {
	s := l.lanes
	job := filepath.Base(ln.card.Outbox)
	delete(s.state.Started, job)
	if !s.given[job] {
		s.given[job] = true
		s.state.GivenUp = append(s.state.GivenUp, job)
	}
	l.saveLanes(now)
	ln.card, ln.attempts, ln.ended = nil, 0, ""
}
