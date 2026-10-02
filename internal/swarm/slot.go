package swarm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	JobStarted    string `json:"job_started,omitempty"`
	RunnerPid     int    `json:"runner_pid"`
	RunnerStarted string `json:"runner_started,omitempty"`
	ReservedAt    string `json:"reserved_at,omitempty"`
	LaunchedAt    string `json:"launched_at,omitempty"`
	Nonce         string `json:"nonce"`
	ExitAttest    string `json:"exit_attest,omitempty"`
}

// PidRecord is <job>/pid: the same identity, beside the job it belongs to. It carries the
// pids and the start stamps its readers want, and NO launch nonce: the job directory is the
// first --write of the wall, so a nonce kept here is readable and writable by the worker
// itself (packet 2 finding 2).
type PidRecord struct {
	Job        string `json:"job"`
	Slot       int    `json:"slot"`
	State      string `json:"state"`
	Pid        int    `json:"pid"`
	Pgid       int    `json:"pgid"`
	JobPgid    int    `json:"job_pgid,omitempty"`
	PidStarted string `json:"pid_started"`
	JobStarted string `json:"job_started,omitempty"`
	RunnerPid  int    `json:"runner_pid"`
	Started    string `json:"started"`
}

// ExitRecord is <job>/exit.json: the supervisor's durable completion evidence (rule 18,
// step 6). An outcome with none is `unknown`, never guessed. `Attest` is the per-launch
// secret the supervisor minted in its own memory, written here only inside endWith after the
// job's whole process group is dead; its hash lives in the slot file, and a reader accepts
// this record as the supervisor's own word only when both the nonce AND the attestation
// match.
type ExitRecord struct {
	RC        int    `json:"rc"`
	Signal    string `json:"signal,omitempty"`
	Ended     string `json:"ended"`
	Survivors int    `json:"survivors"`
	Nonce     string `json:"nonce"`
	Attest    string `json:"attest,omitempty"`
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
	if err := renameSteady(path, gone); err != nil {
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

// ReadJSON reads one of this package's small records back.
func ReadJSON(path string, v any) error {
	raw, err := readFileSteady(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// The job directory's durable records.
func ExitPath(jobDir string) string   { return filepath.Join(jobDir, "exit.json") }
func ResultPath(jobDir string) string { return filepath.Join(jobDir, "RESULT.md") }

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

