/*
Package swarm is nova-swarm's machinery: a pool of one-task workers, each with its own
working directory, its own data home, its own job directory and its own deadline held by
the machinery rather than by the worker.

Everything a worker writes is DATA. A RESULT.md is a report, never an instruction: nothing
in it is executed, nothing in it grants anything, and a finding in it is a claim to be
checked against the repository. That rule is stated in docs/SPEC-SWARM.md, where a person
reads it, and is deliberately nowhere in this code -- a tool cannot enforce it, and a tool
that pretended to would be the most dangerous thing in the pool.
*/
package swarm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The four states a task's files sit in. A task is in exactly one of them, and the move
// between two of them is a rename, which is the claim: two dispatchers both try it and the
// kernel picks one.
const (
	Pending = "pending"
	Running = "running"
	Done    = "done"
	Failed  = "failed"
	// Aborted is where a task goes when a PERSON replaces it: `requeue` writes the new task
	// and moves the old one here, so a pool cannot wedge (the new-user audit, F2 and F7:
	// `requeue` added and never removed, pending went 2 to 4, and the only way out of the
	// refusal's own remedy was `rm`).
	Aborted = "aborted"
	// RoutedOut is where a task goes when the ladder rules it judgment work owed to a
	// child or a friend (issue #1486): a card the dispatcher routes to ask-child or
	// ask-bus is NOT dispatched to a model, it is parked here with its ROUTE line, and
	// the coordinator (or a friend, over the bus) takes it from here.
	RoutedOut = "routed-out"
)

// The pool's other directories. reports/ holds the pages and one directory per finalized
// job; usage/ holds one row per job and is OUTSIDE everything reclaim removes, which is
// the whole of rule 12.
const (
	Reports = "reports"
	Scratch = "scratch"
	Slots   = "slots"
	Usage   = "usage"
)

// RunLock excludes dispatchers and nothing else; SlotsLock guards one slot-file
// transition and is never held across a wait (rule 17).
const (
	RunLock   = "run.lock"
	SlotsLock = "slots.lock"
	StopFile  = "stop"
)

// Pool is a pool directory. The directory itself must exist -- this tool creates the
// structure inside one a person named, never the one a person forgot to name.
type Pool struct{ Dir string }

// OpenPool opens the pool at dir and makes sure its structure is there.
func OpenPool(dir string) (*Pool, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("--pool wants the directory that holds this pool's tasks; refusing to guess")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("--pool wants a directory that exists: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--pool wants a directory, and %s is a file", dir)
	}
	p := &Pool{Dir: dir}
	for _, d := range []string{Pending, Running, Done, Failed, Aborted, RoutedOut, Reports, Scratch, Slots, Usage} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Path joins a path inside the pool.
func (p *Pool) Path(parts ...string) string {
	return filepath.Join(append([]string{p.Dir}, parts...)...)
}

// Verdict is a reader's counts for one task, recorded by a person (rule 8).
type Verdict struct {
	Who      string `json:"who"`
	Accurate int    `json:"accurate"`
	Wrong    int    `json:"wrong"`
}

// Sidecar is everything the pool knows about a task that is not its text. It travels with
// the task file from pending/ to running/ to done/ or failed/.
type Sidecar struct {
	ID          string   `json:"id"`
	Label       string   `json:"label,omitempty"`
	Template    string   `json:"template,omitempty"`
	Files       int      `json:"files"`
	Tokens      int      `json:"tokens,omitempty"`
	Unmetered   bool     `json:"unmetered,omitempty"`
	Deadline    string   `json:"deadline"`
	Batch       string   `json:"batch,omitempty"`
	From        string   `json:"from,omitempty"`
	Requeued    int      `json:"requeued,omitempty"`
	Reaped      int      `json:"reaped,omitempty"`
	Launch      string   `json:"launch,omitempty"`
	Malformed   int      `json:"malformed,omitempty"`
	Violation   string   `json:"violation,omitempty"`
	MaxInput    int      `json:"max_input,omitempty"`
	Limit       string   `json:"limit,omitempty"`
	ProviderRef string   `json:"provider_ref,omitempty"`
	Job         string   `json:"job,omitempty"`
	Slot        int      `json:"slot,omitempty"`
	Started     string   `json:"started,omitempty"`
	Ended       string   `json:"ended,omitempty"`
	End         string   `json:"end,omitempty"`
	RC          int      `json:"rc"`
	Notes       int      `json:"notes,omitempty"`
	Class       string   `json:"class,omitempty"`
	ReplacedBy  string   `json:"replaced_by,omitempty"`
	Verdict     *Verdict `json:"verdict,omitempty"`
}

// syncFile is how a durable record is flushed before it is renamed or linked into
// place: (*os.File).Sync. This package's unit tests replace it with a no-op
// (fsync_test.go): on macOS Sync is F_FULLFSYNC, tens of milliseconds a write, and
// what a unit test asserts is the record, not the disk's durability
// (nova-tools#4328).
var syncFile = (*os.File).Sync

// writeAtomic writes whole revisions: a temporary file beside the target, fsynced, then
// renamed over it. A reader sees the previous revision or the new one, never a prefix.
//
// THE TEMPORARY IS UNIQUE AND CREATED EXCLUSIVELY (security#30, finding 3). Several of
// these records live in a job directory the worker owns -- exit.json and pid.json are
// written by the supervisor, which is not inside the wall -- and the temporary was
// `<path>.tmp`, a name a worker could predict and plant a symlink at. The old open
// truncated whatever it found and the rename then MOVED THE LINK over the record's own
// path, so the damage outlived the write. A name drawn from the OS random source cannot be
// waited for, O_EXCL refuses even a lucky one rather than truncating it, O_NOFOLLOW refuses
// a link planted in the instant after the name is drawn, and a failed write takes its
// temporary with it rather than leaving a stray.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	nonce, err := Nonce()
	if err != nil {
		return err
	}
	tmp := path + "." + nonce + ".tmp"
	f, err := openRegularWrite(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := syncFile(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// The rename waits out a reader that has this path open (fileretry.go): on Windows
	// that collision is an error, and a durable record dropped because somebody was
	// reading it is how a supervisor aborted its own launch (#92).
	if err := renameSteady(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
