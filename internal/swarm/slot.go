package swarm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A SLOT IS DURABLE OWNERSHIP ON DISK (rules 17 and 18).
//
// The data home is the whole reason slots exist: the harness keeps one database per data
// home, and concurrent runs against one database lock each other out -- measured
// 2026-09-10 with six workers, `database is locked`, five of the six doing nothing while
// the pool reported six running. A pool that reports six running and does one task's work
// is worse than a pool of one, because the number is believed.
//
// So a slot is a FILE, and the file is the ownership rather than a cache of it. The runner
// reserves it before the fork; the supervisor identifies itself into it before it does
// anything else, by compare-and-swap on the reservation and its nonce; a dispatcher that
// finds slot files from a dead run adopts, reclaims or quarantines every one of them before
// it claims a single pending task. Nothing about a slot is ever inferred from a directory
// listing, a timer or an age.

// The states a slot file passes through.
const (
	SlotReserved = "reserved"
	SlotLaunched = "launched"
	SlotOrphaned = "orphaned"
)

// SlotFile is what <pool>/slots/<n>.json holds.
type SlotFile struct {
	Job           string `json:"job"`
	JobDir        string `json:"job_dir,omitempty"`
	State         string `json:"state"`
	Pid           int    `json:"pid,omitempty"`
	Pgid          int    `json:"pgid,omitempty"`
	JobPgid       int    `json:"job_pgid,omitempty"`
	PidStarted    string `json:"pid_started,omitempty"`
	RunnerPid     int    `json:"runner_pid"`
	RunnerStarted string `json:"runner_started,omitempty"`
	ReservedAt    string `json:"reserved_at,omitempty"`
	LaunchedAt    string `json:"launched_at,omitempty"`
	Nonce         string `json:"nonce"`
}

// PidRecord is <job>/pid: the same identity, beside the job it belongs to.
type PidRecord struct {
	Job        string `json:"job"`
	Slot       int    `json:"slot"`
	State      string `json:"state"`
	Pid        int    `json:"pid"`
	Pgid       int    `json:"pgid"`
	JobPgid    int    `json:"job_pgid,omitempty"`
	PidStarted string `json:"pid_started"`
	RunnerPid  int    `json:"runner_pid"`
	Nonce      string `json:"nonce"`
	Started    string `json:"started"`
}

// ExitRecord is <job>/exit.json: the supervisor's durable completion evidence (rule 18,
// step 6). An outcome with none is `unknown`, never guessed.
type ExitRecord struct {
	RC        int    `json:"rc"`
	Signal    string `json:"signal,omitempty"`
	Ended     string `json:"ended"`
	Survivors int    `json:"survivors"`
	Nonce     string `json:"nonce"`
	End       string `json:"end,omitempty"`
	Spent     int    `json:"spent,omitempty"`
	Observed  bool   `json:"observed,omitempty"`
	Partial   bool   `json:"partial,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// AbortedRecord is <job>/aborted.json: the durable acknowledgement a supervisor that lost
// its identify writes BEFORE its own death can be observed, so that a launch's ABSENCE can
// be established rather than assumed.
type AbortedRecord struct {
	Nonce     string `json:"nonce"`
	Reason    string `json:"reason"`
	At        string `json:"at"`
	Survivors int    `json:"survivors"`
}

// Nonce is twelve hex characters from the OS random source, drawn once per launch and never
// reused. It is what makes a stale supervisor's identify fail against a reservation that
// has moved on.
func Nonce() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("the OS random source would not supply a launch nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (p *Pool) slotPath(n int) string { return p.Path(Slots, strconv.Itoa(n)+".json") }

// ReadSlot reads one slot file.
func (p *Pool) ReadSlot(n int) (SlotFile, error) {
	var sf SlotFile
	raw, err := os.ReadFile(p.slotPath(n))
	if err != nil {
		return sf, err
	}
	if err := json.Unmarshal(raw, &sf); err != nil {
		return sf, fmt.Errorf("%s: %w", p.slotPath(n), err)
	}
	return sf, nil
}

// SlotNumbers lists the slots that have a file, in number order. An unreadable name is
// returned too, as a number that will fail to parse into a decision and be quarantined --
// a file this tool cannot read is never a free slot.
func (p *Pool) SlotNumbers() ([]int, []string, error) {
	entries, err := os.ReadDir(p.Path(Slots))
	if err != nil {
		return nil, nil, err
	}
	var out []int
	var bad []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
		if err != nil {
			bad = append(bad, name)
			continue
		}
		out = append(out, n)
	}
	sort.Ints(out)
	return out, bad, nil
}

// Reserve writes a slot file in the `reserved` state, under slots.lock, and refuses a slot
// whose file exists in ANY state: the runner never allocates over an existing file, so the
// worker cap counts reserved slots as held.
func (p *Pool) Reserve(n int, job, jobDir, nonce string, runnerPid int, now time.Time) error {
	release, err := p.TakeLock(SlotsLock, SlotsWait)
	if err != nil {
		return err
	}
	defer release()
	if _, err := os.Stat(p.slotPath(n)); err == nil {
		return fmt.Errorf("slot %d already has a file at %s", n, p.slotPath(n))
	}
	return writeSlot(p.slotPath(n), SlotFile{
		Job: job, JobDir: jobDir, State: SlotReserved, Nonce: nonce,
		RunnerPid: runnerPid, RunnerStarted: StartStamp(runnerPid),
		ReservedAt: now.UTC().Format(time.RFC3339),
	})
}

// claimFree is allocation as ONE step against one authority: the scan for the lowest
// numbered free slot and the reservation that claims it happen under the SAME slots.lock,
// so two callers racing for the last free slot cannot both win it. The slot files are the
// authority; a slot whose file exists in any state is held. The quarantine set is passed in
// because a slot released badly enough to quarantine may have no file left to skip, and it
// is read on the dispatcher's single goroutine. jobDir resolves a slot to the job directory
// the reservation records, so the number chosen and the directory written are decided
// together, under the lock.
func (p *Pool) claimFree(workers int, quarantine map[int]bool, job, nonce string, runnerPid int, now time.Time, jobDir func(slot int) string) (int, error) {
	release, err := p.TakeLock(SlotsLock, SlotsWait)
	if err != nil {
		return 0, err
	}
	defer release()
	for n := 1; n <= workers; n++ {
		if quarantine[n] {
			continue
		}
		if _, err := os.Stat(p.slotPath(n)); err == nil {
			continue
		}
		dir := ""
		if jobDir != nil {
			dir = jobDir(n)
		}
		if err := writeSlot(p.slotPath(n), SlotFile{
			Job: job, JobDir: dir, State: SlotReserved, Nonce: nonce,
			RunnerPid: runnerPid, RunnerStarted: StartStamp(runnerPid),
			ReservedAt: now.UTC().Format(time.RFC3339),
		}); err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, fmt.Errorf("no free slot among %d: every one holds a file", workers)
}

// Identify is the supervisor's compare-and-swap (rule 18, step 3): under slots.lock, read
// the slot file, require that it STILL reads `reserved` with the nonce this supervisor was
// handed, and only then rename the identity into place. On any other content the supervisor
// aborts before it spawns anything.
func (p *Pool) Identify(n int, nonce string, id SlotFile) error {
	release, err := p.TakeLock(SlotsLock, SlotsWait)
	if err != nil {
		return err
	}
	defer release()
	cur, err := p.ReadSlot(n)
	if err != nil {
		return fmt.Errorf("reservation changed: the slot file is gone or unreadable (%s)", redactedReason(err))
	}
	if cur.State != SlotReserved || cur.Nonce != nonce {
		return fmt.Errorf("reservation changed: slot %d reads state=%s nonce=%s", n, cur.State, cur.Nonce)
	}
	id.Job, id.JobDir, id.Nonce, id.RunnerPid, id.RunnerStarted = cur.Job, cur.JobDir, cur.Nonce, cur.RunnerPid, cur.RunnerStarted
	id.ReservedAt, id.State = cur.ReservedAt, SlotLaunched
	return writeSlot(p.slotPath(n), id)
}

// Orphan rewrites a reservation whose runner is dead to `orphaned`, KEEPING THE NONCE --
// under slots.lock, after re-reading the file and rechecking that it still says `reserved`
// with the nonce first read. If it now reads `launched`, the supervisor's identify landed
// first and the caller follows the adopt path instead, which is what the bool reports: an
// orphaning never overwrites an identify that just completed.
func (p *Pool) Orphan(n int, nonce string) (adopted bool, sf SlotFile, err error) {
	release, lockErr := p.TakeLock(SlotsLock, SlotsWait)
	if lockErr != nil {
		return false, SlotFile{}, lockErr
	}
	defer release()
	cur, err := p.ReadSlot(n)
	if err != nil {
		return false, SlotFile{}, err
	}
	if cur.State == SlotLaunched {
		return true, cur, nil
	}
	if cur.State != SlotReserved || cur.Nonce != nonce {
		return false, cur, fmt.Errorf("slot %d changed under the recovery: state=%s nonce=%s", n, cur.State, cur.Nonce)
	}
	cur.State = SlotOrphaned
	return false, cur, writeSlot(p.slotPath(n), cur)
}

// Free releases a slot: a rename made under slots.lock, and then the removal. Every release
// -- reclaim, unknown, launch-failed, unlaunched, free-on-finalize -- comes through here.
func (p *Pool) Free(n int) error {
	release, err := p.TakeLock(SlotsLock, SlotsWait)
	if err != nil {
		return err
	}
	defer release()
	path := p.slotPath(n)
	gone := path + ".freed"
	if err := os.Rename(path, gone); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.Remove(gone)
}

func writeSlot(path string, sf SlotFile) error {
	raw, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'), 0o644)
}

// WriteJSON writes one of this package's small records whole, through a temporary file,
// fsync and a rename, so that a reader sees a whole record or none.
func WriteJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'), 0o644)
}

// ReadJSON reads one of them back.
func ReadJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// The job directory's three durable records.
func PidPath(jobDir string) string     { return filepath.Join(jobDir, "pid") }
func ExitPath(jobDir string) string    { return filepath.Join(jobDir, "exit.json") }
func AbortedPath(jobDir string) string { return filepath.Join(jobDir, "aborted.json") }
func NotePath(jobDir string) string    { return filepath.Join(jobDir, "note") }
func ResultPath(jobDir string) string  { return filepath.Join(jobDir, "RESULT.md") }

// Decision is what a recovering dispatcher does about one slot file, and the words are the
// ones rule 17 uses.
type Decision struct {
	Slot      int
	Kind      string // adopt, reclaim, unknown, unlaunched, quarantine
	Reason    string
	File      SlotFile
	Exit      *ExitRecord
	Remaining time.Duration
}

// The decision words.
const (
	DecideAdopt      = "adopt"
	DecideReclaim    = "reclaim"
	DecideUnknown    = "unknown"
	DecideUnlaunched = "unlaunched"
	DecideQuarantine = "quarantine"
)

// Decide reads one slot file and decides it with NO GUESS. Every branch here is rule 17's,
// and the default is quarantine: a slot this tool cannot decide is never allocated, is
// named on STATUS OK quarantined=, and is a person's to clear.
func (p *Pool) Decide(n int) Decision {
	sf, err := p.ReadSlot(n)
	if err != nil {
		return Decision{Slot: n, Kind: DecideQuarantine, Reason: "slot file unreadable: " + redactedReason(err)}
	}
	d := Decision{Slot: n, File: sf}
	switch sf.State {
	case SlotReserved, SlotOrphaned:
		// A reservation is AMBIGUOUS while its launch is unproven: no child has identified
		// itself, and a spawned supervisor paused before its identify cannot be proven
		// absent by a dead runner. So launch absence must be ESTABLISHED, by an aborted.json
		// carrying this nonce with no survivors, by the runner's own kill, or by a person.
		if sf.State == SlotReserved && Alive(sf.RunnerPid) {
			d.Kind, d.Reason = DecideQuarantine, "reserved by a runner that is still alive (pid "+strconv.Itoa(sf.RunnerPid)+")"
			return d
		}
		var ab AbortedRecord
		if err := ReadJSON(AbortedPath(sf.JobDir), &ab); err == nil && ab.Nonce == sf.Nonce {
			if ab.Survivors > 0 {
				d.Kind, d.Reason = DecideQuarantine, fmt.Sprintf("aborted, survivors=%d", ab.Survivors)
				return d
			}
			d.Kind, d.Reason = DecideUnlaunched, "aborted before launch, survivors=0"
			return d
		}
		d.Kind, d.Reason = DecideQuarantine, "reserved, launch unproven"
		return d
	case SlotLaunched:
		switch {
		case Alive(sf.Pid) && StartStamp(sf.Pid) == sf.PidStarted:
			d.Kind, d.Reason = DecideAdopt, "pid alive under its recorded start stamp"
			return d
		case Alive(sf.Pid):
			d.Kind, d.Reason = DecideQuarantine, "pid "+strconv.Itoa(sf.Pid)+" is alive under a different start stamp (pid reuse)"
			return d
		case GroupAlive(sf.JobPgid) || GroupAlive(sf.Pgid):
			d.Kind, d.Reason = DecideQuarantine, "the leader is dead and a process in its group is alive"
			return d
		}
		var ex ExitRecord
		switch err := ReadJSON(ExitPath(sf.JobDir), &ex); {
		case err == nil && ex.Nonce == sf.Nonce:
			d.Kind, d.Reason, d.Exit = DecideReclaim, "the group is dead and exit.json carries this launch's nonce", &ex
			return d
		case err == nil:
			d.Kind, d.Reason = DecideQuarantine, "exit.json carries a nonce from another launch"
			return d
		}
		d.Kind, d.Reason = DecideUnknown, "the group is dead and there is no exit.json"
		return d
	}
	d.Kind, d.Reason = DecideQuarantine, "slot file holds an unknown state "+strconv.Quote(sf.State)
	return d
}
