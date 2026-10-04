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
)

// The friend's files, under the working directory --dir names: one writer
// each. The daemon writes Status; the pong verb writes Pong; the
// coordinator and the session write the queue file (SPEC-FRIEND.md, the
// files).
const (
	StateDir   = ".nova-friend"
	StatusFile = "status.json"
	PongFile   = "pong.json"
	LogFile    = "deliver.log"
	QueueFile  = "inbox/QUEUE.json"
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

func statusPath(dir string) string { return filepath.Join(dir, StateDir, StatusFile) }
func pongPath(dir string) string   { return filepath.Join(dir, StateDir, PongFile) }

// LogPath is the daemon's own log: one line per delivery, on the friend's
// volume (launchd's own log is elsewhere: Plist).
func LogPath(dir string) string { return filepath.Join(dir, StateDir, LogFile) }

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
func WriteStatus(dir string, s Status) error { return write(statusPath(dir), s) }

// ReadStatus is what the daemon last wrote; found is false when it never has.
func ReadStatus(dir string) (s Status, found bool, err error) {
	found, err = read(statusPath(dir), &s)
	return s, found, err
}

// WritePong is the pong verb's record of the answer it sent.
func WritePong(dir string, p Pong) error { return write(pongPath(dir), p) }

// ReadPong is the session's last recorded answer.
func ReadPong(dir string) (p Pong, found bool, err error) {
	found, err = read(pongPath(dir), &p)
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
func Record(dir, line string) error {
	path := LogPath(dir)
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
