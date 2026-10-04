package friend

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/filelock"
)

// The friend's files, one writer each. The state files live in the state
// directory (DefaultStateDir under the home directory, or --state-dir),
// never on the friend's volume: a background process on this platform may
// not touch a removable volume without the person's permission (measured
// 2026-10-04, "operation not permitted" on the mkdir). The daemon writes
// Status and the log; the pong verb writes Pong. The queue file is the
// coordinator's and the session's, under the working directory
// (SPEC-FRIEND.md, the files).
const (
	StatusFile  = "status.json"
	SessionFile = "session.json"
	PongFile    = "pong.json"
	LogFile     = "deliver.log"
	QueueFile   = "inbox/QUEUE.json"
)

// DaemonStale is how old the status file may be while the daemon counts
// as up: it rewrites the file at least every StatusEvery.
const (
	StatusEvery = 5 * time.Second
	DaemonStale = 30 * time.Second
)

// Status is what the daemon knows, as status reads it: one file, rewritten
// whole, so a reader never sees half a state.
type Status struct {
	Friend     string    `json:"friend"`
	Harness    string    `json:"harness"`
	Started    time.Time `json:"started"`
	At         time.Time `json:"at"` // when this was written
	Connection string    `json:"connection"`
	LastPing   time.Time `json:"last_ping"`
	Seat       string    `json:"seat"`
	SeatSince  time.Time `json:"seat_since"`
	Challenge  string    `json:"challenge"`
	Nonce      string    `json:"nonce"`
	LastPong   time.Time `json:"last_pong"`
	Pongs      int       `json:"pongs"`
	Delivered  int       `json:"delivered"` // messages acked this run
	Beats      int       `json:"beats"`
	LastBeat   time.Time `json:"last_beat"`
	BeatError  string    `json:"beat_error,omitempty"`
	StoreError string    `json:"store_error,omitempty"`
	Width      int       `json:"width"`
	Asleep     bool      `json:"asleep"`
}

// SessionState is the durable operator choice for one running friend. It is
// deliberately separate from Status: the daemon owns Status, while sleep and
// wake are synchronous commands which update this one small state record.
type SessionState struct {
	Coordinator string `json:"coordinator,omitempty"`
	Asleep      bool   `json:"asleep,omitempty"`
}

// Pong is the session's last answer, as the pong verb records it beside
// sending it: the proof the daemon forwards.
type Pong struct {
	Nonce   string    `json:"nonce"`
	At      time.Time `json:"at"`
	To      string    `json:"to"`
	Queue   int       `json:"queue"`
	Working int       `json:"working"`
	Width   int       `json:"width"`
}

// Queue is the friend's queue file: one record per task; the coordinator
// writes assignments into it, the session marks them working or done.
type Queue struct {
	Tasks []Task `json:"tasks"`
}

// Task is one record of the queue file.
type Task struct {
	ID          string `json:"id"`
	State       string `json:"state"` // queued, working, done
	Deliverable string `json:"deliverable,omitempty"`
}

// Counts is what the queue file says: tasks queued and tasks working.
func (q Queue) Counts() (queue, working int) {
	for _, t := range q.Tasks {
		switch t.State {
		case "queued", "":
			queue++
		case "working":
			working++
		}
	}
	return queue, working
}

// DefaultStateDir is where a friend's state files live unless --state-dir
// names another directory: ~/.nova-friend/<friend>.
func DefaultStateDir(home, friend string) string { return filepath.Join(home, ".nova-friend", friend) }

func statusPath(stateDir string) string      { return filepath.Join(stateDir, StatusFile) }
func sessionPath(stateDir string) string     { return filepath.Join(stateDir, SessionFile) }
func sessionLockPath(stateDir string) string { return filepath.Join(stateDir, "session.lock") }
func daemonLockPath(stateDir string) string  { return filepath.Join(stateDir, "daemon.lock") }
func pongPath(stateDir string) string        { return filepath.Join(stateDir, PongFile) }

// TakeDaemonLock makes the launch agent a singleton for this friend's state
// directory. The lock file is permanent; filelock owns its contents.
func TakeDaemonLock(stateDir, friend string) (*filelock.FileLock, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}
	return filelock.TryLock(daemonLockPath(stateDir), "nova-friend "+friend)
}

// WithSessionState serializes a read of the durable session choice with local
// sleep/wake commands and automatic coordinator wake. Callers must keep fn
// short and must not perform store or harness I/O while holding the lock.
func WithSessionState(stateDir string, fn func(SessionState) error) error {
	return sessionState(stateDir, func(s *SessionState) (bool, error) { return false, fn(*s) })
}

// UpdateSessionState serializes one durable read/modify/write. A stale command
// cannot be replayed after restart: only the current state is stored, never an
// outstanding sleep request.
func UpdateSessionState(stateDir string, update func(*SessionState) error) (SessionState, error) {
	var out SessionState
	err := sessionState(stateDir, func(s *SessionState) (bool, error) {
		if err := update(s); err != nil {
			return false, err
		}
		out = *s
		return true, nil
	})
	return out, err
}

func sessionState(stateDir string, fn func(*SessionState) (bool, error)) (retErr error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	l, err := filelock.Lock(sessionLockPath(stateDir), "nova-friend session state", time.Second)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, l.Unlock()) }()
	var s SessionState
	if _, err := read(sessionPath(stateDir), &s); err != nil {
		return err
	}
	shouldWrite, err := fn(&s)
	if err != nil {
		return err
	}
	if shouldWrite {
		return write(sessionPath(stateDir), s)
	}
	return nil
}

// ReadSessionState returns the committed operator choice, or the zero state
// before any sleep or wake command has run.
func ReadSessionState(stateDir string) (SessionState, error) {
	var out SessionState
	err := WithSessionState(stateDir, func(s SessionState) error { out = s; return nil })
	return out, err
}

// LogPath is the daemon's own log in the state directory: one line per
// delivery (launchd's own log is elsewhere: Plist).
func LogPath(stateDir string) string { return filepath.Join(stateDir, LogFile) }

// write writes v as JSON to path atomically, making the directory.
func write(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, append(raw, '\n'), 0o644)
}

// read reads JSON at path into v; missing is a nil error with found false.
func read(path string, v any) (found bool, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return true, fmt.Errorf("%s: %v", path, err)
	}
	return true, nil
}

// WriteStatus is the daemon's write of its state.
func WriteStatus(stateDir string, s Status) error { return write(statusPath(stateDir), s) }

// ReadStatus is what the daemon last wrote; found is false when it never has.
func ReadStatus(stateDir string) (s Status, found bool, err error) {
	found, err = read(statusPath(stateDir), &s)
	return s, found, err
}

// WritePong is the pong verb's record of the answer it sent.
func WritePong(stateDir string, p Pong) error { return write(pongPath(stateDir), p) }

// ReadPong is the session's last recorded answer.
func ReadPong(stateDir string) (p Pong, found bool, err error) {
	found, err = read(pongPath(stateDir), &p)
	return p, found, err
}

// ReadQueue is the queue file's counts; a file that is not there counts
// zero, and a file that is no queue is an error the caller shows.
func ReadQueue(dir string) (queue, working int, err error) {
	var q Queue
	path := filepath.Join(dir, filepath.FromSlash(QueueFile))
	if _, err := read(path, &q); err != nil {
		// the file may be a bare list of tasks
		if _, err2 := read(path, &q.Tasks); err2 != nil {
			return 0, 0, err
		}
	}
	queue, working = q.Counts()
	return queue, working, nil
}

// Record appends one line to the daemon's log; a log that cannot be
// written is not a reason to stop delivering, so the error is answered
// for the status file and nothing else.
func Record(stateDir, line string) error {
	path := LogPath(stateDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}
