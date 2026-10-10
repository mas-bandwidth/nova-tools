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

// The friend's files, one writer each. The state files live in the state
// directory (DefaultStateDir under the home directory, or --state-dir),
// never on the friend's volume: a background process on this platform may
// not touch a removable volume without the person's permission (measured
// 2026-10-04, "operation not permitted" on the mkdir). The daemon writes
// Status and the log; the pong verb writes Pong. The queue file is the
// coordinator's and the session's, under the working directory
// (SPEC-FRIEND.md, the files).
const (
	StatusFile = "status.json"
	PongFile   = "pong.json"
	LogFile    = "deliver.log"
	WatchFile  = "watch.json"
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
	LastPong   time.Time `json:"last_pong"` // the session's pong, the only answer that ends a challenge
	Pongs      int       `json:"pongs"`
	// LastDaemonPong is when the daemon last answered a ping itself: transport,
	// never the session (docs/SPEC-FRIEND.md, session-pong.w1).
	LastDaemonPong time.Time `json:"last_daemon_pong"`
	Delivered      int       `json:"delivered"` // messages acked this run
	Beats          int       `json:"beats"`
	LastBeat       time.Time `json:"last_beat"`
	BeatError      string    `json:"beat_error,omitempty"`
	Envelope       int       `json:"envelope"`       // messages the last envelope carried (docs/SPEC-FRIEND.md, the loop)
	EnvelopeBytes  int       `json:"envelope_bytes"` // its text's size: at most the deliverer's text limit, the first message always in
	// Push is the push proof this run (PushProved, or PushUnproven while the
	// session has not answered its check: nothing is delivered), PushNonce the
	// check's nonce and PushSince when the daemon started waiting, while unproven;
	// empty for a harness with no session to prove. ProofSent is the session's
	// proof (the presence file's last_heard) the sprint server last took on her
	// beat, zero before any (docs/SPEC-FRIEND.md, The push proof).
	Push      string    `json:"push,omitempty"`
	PushNonce string    `json:"push_nonce,omitempty"`
	PushSince time.Time `json:"push_since,omitzero"`
	ProofSent time.Time `json:"proof_sent,omitzero"`
	// HarnessSeen is what the harness check last read (HarnessRunning,
	// HarnessNotSeen, or empty: cannot tell). Advisory: it
	// never makes the friend down (alive.go, HarnessWatch).
	HarnessSeen string `json:"harness_seen,omitempty"`
	// HarnessAlive is the rule the harness check read it by: RuleSession (the
	// session's last turn) or RuleApp (the app in the process table); empty
	// for another signal, or before a check.
	HarnessAlive string `json:"alive,omitempty"`
	StoreError   string `json:"store_error,omitempty"`
	Width        int    `json:"width"`
	// Session is SessionOK, SessionBroken once the provider refused BrokenAfter
	// turns in a row the same way (or a turn could not be taken, or the
	// context was full, or the harness compacted in a loop, or a turn stuck),
	// or SessionRecovered once a fresh session has taken over; empty for a
	// passive harness.
	Session       string `json:"session,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	SessionReason string `json:"session_reason,omitempty"`
	// SessionFrom and SessionTo are the old and new session ids of the last
	// recovery, and RecoveredAt when it happened (session=recovered).
	SessionFrom string    `json:"session_from,omitempty"`
	SessionTo   string    `json:"session_to,omitempty"`
	RecoveredAt time.Time `json:"recovered_at,omitzero"`
	// SessionLive is the conversation a mailbox harness delivers into (Daemon.Mailbox):
	// the one that reads, as the daemon last followed it; empty for every other harness.
	SessionLive string `json:"session_live,omitempty"`
	// Queued is her harness's own queue of deliveries not yet taken, as the last delivery
	// read it (codex: the open chat's queue), when QueueKnown.
	Queued     int       `json:"queued,omitempty"`
	QueueKnown bool      `json:"queue_known,omitempty"`
	BrokenAt   time.Time `json:"broken_at,omitempty"`
	// While the harness is at its usage limit or out of credits Session is
	// SessionLimited, LimitKind says which (KindLimit or KindCredits) and
	// LimitUntil is the reset (docs/SPEC-FRIEND.md, limits-mean-down-w-r.w1~15).
	LimitKind  string    `json:"limit_kind,omitempty"`
	LimitUntil time.Time `json:"limit_until,omitzero"`
	// Mode is how the daemon delivers now (batch or one-shot), and Lanes the
	// one-shot lanes as n:session:card/turn, empty in batch.
	Mode  string `json:"mode,omitempty"`
	Lanes string `json:"lanes,omitempty"`
	// Paced is the lanes' effective width under the subscription windows' pacing
	// (pacing.go), nil before the lanes have stepped; Window is the windows' use
	// as the harness last reported it ("5h 62% 7d 31%", empty when none is live),
	// and Pacing the row's pacing as a percent.
	Paced  *int   `json:"paced,omitempty"`
	Window string `json:"window,omitempty"`
	Pacing string `json:"pacing,omitempty"`
	// Held, InboxJobs and Missing are the last inbox reconcile's counts (SyncInbox): the
	// cards on her row, the sprint jobs in her inbox, and the held cards with no BRIEF.md
	// after it; HeldKnown is false until the server has answered once, and InboxError is
	// why the last reconcile did not finish, empty when it did.
	HeldKnown  bool   `json:"held_known,omitempty"`
	Held       int    `json:"held"`
	InboxJobs  int    `json:"inbox"`
	Missing    int    `json:"missing"`
	InboxError string `json:"inbox_error,omitempty"`
	// HeldFrom is where the last answer came from: friend cards, or the worker view while the
	// server does not serve it (no brief is written from the view).
	HeldFrom string `json:"held_from,omitempty"`
	// DaemonVersion is the daemon's build stamp (the tool's Stamp) and Binary the
	// path it runs from, so a reader sees which binary each daemon runs
	// (docs/SPEC-FRIEND.md, daemon-supervised-r-b.w7).
	DaemonVersion string `json:"daemon_version,omitempty"`
	Binary        string `json:"binary,omitempty"`
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
	Gen int    `json:"gen,omitempty"` // assignment generation; absent means 1 (docs/FRIENDS.md)
	Job string `json:"job,omitempty"` // delivered inbox directory, when recorded

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

// DefaultStateDir is the home directory's state directory for friend,
// ~/.nova-friend/<friend>: where a daemon from before the state moved under
// --dir kept its files, and where one goes when its directory refuses them
// (DaemonStateDir).
func DefaultStateDir(home, friend string) string { return filepath.Join(home, ".nova-friend", friend) }

// StateDirIn is the daemon's state directory under the friend's working
// directory, <dir>/.nova-friend: inside the directory her session may write,
// so a sandboxed session's pong lands where the daemon reads it (the finding
// of 2026-10-05).
func StateDirIn(dir string) string { return filepath.Join(dir, ".nova-friend") }

// DaemonStateDir is where a daemon run with no --state-dir keeps its files:
// StateDirIn(dir), made by mkdir; when that is refused (a background process
// on macOS may not touch a removable volume without the person's permission),
// DefaultStateDir, and why names the refusal for the record.
func DaemonStateDir(home, dir, friend string, mkdir func(string) error) (state, why string) {
	if dir == "" {
		return DefaultStateDir(home, friend), "no --dir"
	}
	in := StateDirIn(dir)
	if err := mkdir(in); err != nil {
		return DefaultStateDir(home, friend), err.Error()
	}
	return in, ""
}

// FindStateDir is where a reader looks for friend's state when no
// --state-dir names it: StateDirIn(dir) when a daemon has written its status
// there, else the home directory's (a daemon from before the move, or one
// whose directory refused it).
func FindStateDir(home, dir, friend string) string {
	if dir != "" {
		if _, err := os.Stat(statusPath(StateDirIn(dir))); err == nil {
			return StateDirIn(dir)
		}
	}
	return DefaultStateDir(home, friend)
}

func statusPath(stateDir string) string { return filepath.Join(stateDir, StatusFile) }
func pongPath(stateDir string) string   { return filepath.Join(stateDir, PongFile) }

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

// Watch is the cursor of the coordinator's watch (docs/SPEC-FRIEND.md, Watch):
// the last stream entry id it has seen and the wake file's offset it has read
// to, so the next run misses nothing and takes no flag.
type Watch struct {
	After      string `json:"after"`
	WakeOffset int64  `json:"wake_offset"`
}

// WatchPath is the cursor file in the state directory.
func WatchPath(stateDir string) string { return filepath.Join(stateDir, WatchFile) }

// WriteWatch saves the cursor atomically (write a temporary file, rename it
// over the old), so a run killed in the middle leaves the old cursor whole.
func WriteWatch(stateDir string, w Watch) error { return write(WatchPath(stateDir), w) }

// ReadWatch is the cursor the last run saved; found is false when none ever
// has.
func ReadWatch(stateDir string) (w Watch, found bool, err error) {
	found, err = read(WatchPath(stateDir), &w)
	return w, found, err
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
	defer f.Close() // ignored: a read-only file, nothing was written through it
	_, err = f.WriteString(line + "\n")
	return err
}
