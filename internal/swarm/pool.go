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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The four states a task's files sit in. A task is in exactly one of them, and the move
// between two of them is a rename, which is the claim: two dispatchers both try it and the
// kernel picks one.
const (
	Pending = "pending"
	Running = "running"
	Done    = "done"
	Failed  = "failed"
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
	for _, d := range []string{Pending, Running, Done, Failed, Reports, Scratch, Slots, Usage} {
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
	ID        string   `json:"id"`
	Label     string   `json:"label,omitempty"`
	Template  string   `json:"template,omitempty"`
	Files     int      `json:"files"`
	Tokens    int      `json:"tokens,omitempty"`
	Unmetered bool     `json:"unmetered,omitempty"`
	Deadline  string   `json:"deadline"`
	Batch     string   `json:"batch,omitempty"`
	From      string   `json:"from,omitempty"`
	Requeued  int      `json:"requeued,omitempty"`
	Reaped    int      `json:"reaped,omitempty"`
	Launch    string   `json:"launch,omitempty"`
	Malformed int      `json:"malformed,omitempty"`
	Violation string   `json:"violation,omitempty"`
	Job       string   `json:"job,omitempty"`
	Slot      int      `json:"slot,omitempty"`
	Started   string   `json:"started,omitempty"`
	Ended     string   `json:"ended,omitempty"`
	End       string   `json:"end,omitempty"`
	RC        int      `json:"rc"`
	Notes     int      `json:"notes,omitempty"`
	Class     string   `json:"class,omitempty"`
	Verdict   *Verdict `json:"verdict,omitempty"`
}

// BudgetWord is what a RUN line prints for this task's token budget ceiling.
func (s Sidecar) BudgetWord() string {
	if s.Unmetered {
		return "unmetered"
	}
	return fmt.Sprint(s.Tokens)
}

// NewID is the task id: a UTC stamp, the label, and a random half, so that two adds in one
// second cannot collide -- nova-bus's id lesson, which cost a lost note.
func NewID(now time.Time, label string) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// The OS random source failing is not a reason to write a colliding id; the
		// clock's nanoseconds are a poorer half, and they are named as such.
		return fmt.Sprintf("%s-%s-n%06d", now.UTC().Format("20060102T150405Z"), Slug(label), now.Nanosecond()%1000000)
	}
	return fmt.Sprintf("%s-%s-%s", now.UTC().Format("20060102T150405Z"), Slug(label), hex.EncodeToString(b[:]))
}

// Slug reduces a caller's label to the characters an id may hold. An id is a file name, a
// directory name and a field on an event line, so it holds no separator, no space and no
// "=" -- and never becomes nothing.
func Slug(label string) string {
	var b strings.Builder
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		case r == '-' || r == '_':
			b.WriteByte('-')
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		return "task"
	}
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// taskFile and sidecarFile are the two files a task is.
func (p *Pool) taskFile(state, id string) string    { return p.Path(state, id+".task") }
func (p *Pool) sidecarFile(state, id string) string { return p.Path(state, id+".json") }

// Add writes a task's text and its sidecar into pending/.
func (p *Pool) Add(text []byte, sc Sidecar) error {
	if err := writeAtomic(p.taskFile(Pending, sc.ID), text, 0o644); err != nil {
		return err
	}
	return p.WriteSidecar(Pending, sc)
}

// WriteSidecar writes a task's sidecar in the state it is in, whole, through a temporary
// file and a rename.
func (p *Pool) WriteSidecar(state string, sc Sidecar) error {
	raw, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(p.sidecarFile(state, sc.ID), append(raw, '\n'), 0o644)
}

// ReadSidecar reads one task's sidecar from a named state.
func (p *Pool) ReadSidecar(state, id string) (Sidecar, error) {
	var sc Sidecar
	raw, err := os.ReadFile(p.sidecarFile(state, id))
	if err != nil {
		return sc, err
	}
	if err := json.Unmarshal(raw, &sc); err != nil {
		return sc, fmt.Errorf("%s: %w", p.sidecarFile(state, id), err)
	}
	return sc, nil
}

// Text reads one task's text from a named state.
func (p *Pool) Text(state, id string) ([]byte, error) { return os.ReadFile(p.taskFile(state, id)) }

// List returns every task in a state, in id order -- which is time order, because the id
// begins with its UTC stamp.
func (p *Pool) List(state string) ([]Sidecar, error) {
	entries, err := os.ReadDir(p.Path(state))
	if err != nil {
		return nil, err
	}
	var out []Sidecar
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".task") {
			continue
		}
		id := strings.TrimSuffix(name, ".task")
		sc, err := p.ReadSidecar(state, id)
		if err != nil {
			// A task file with no readable sidecar is still a task, and saying so is
			// better than dropping it out of every count.
			sc = Sidecar{ID: id, RC: -1}
		}
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Where finds the state a task's files are in.
func (p *Pool) Where(id string) (string, bool) {
	for _, state := range []string{Running, Pending, Done, Failed} {
		if _, err := os.Stat(p.taskFile(state, id)); err == nil {
			return state, true
		}
	}
	return "", false
}

// Claim moves a task from one state to another, and the move IS the claim: rename is
// atomic within a directory, so two dispatchers racing for one pending task produce one
// winner and one ErrNotExist. The task text is read AFTER the rename, from the path this
// dispatcher renamed to, never from the pending path it no longer owns.
func (p *Pool) Claim(id, from, to string) error {
	if err := os.Rename(p.taskFile(from, id), p.taskFile(to, id)); err != nil {
		return err
	}
	// The sidecar follows its task. A failure here leaves the claim standing, which is
	// right: the task is this dispatcher's, and a sidecar it can rewrite.
	_ = os.Rename(p.sidecarFile(from, id), p.sidecarFile(to, id))
	return nil
}

// ClaimNext claims the oldest pending task, or reports that there is none.
func (p *Pool) ClaimNext() (Sidecar, []byte, bool, error) {
	pending, err := p.List(Pending)
	if err != nil {
		return Sidecar{}, nil, false, err
	}
	for _, sc := range pending {
		switch err := p.Claim(sc.ID, Pending, Running); {
		case err == nil:
			text, err := p.Text(Running, sc.ID)
			if err != nil {
				return Sidecar{}, nil, false, err
			}
			fresh, err := p.ReadSidecar(Running, sc.ID)
			if err == nil {
				sc = fresh
			}
			return sc, text, true, nil
		case errors.Is(err, os.ErrNotExist):
			continue // another dispatcher won the rename; take the next one
		default:
			return Sidecar{}, nil, false, err
		}
	}
	return Sidecar{}, nil, false, nil
}

// writeAtomic writes whole revisions: a temporary file beside the target, fsynced, then
// renamed over it. A reader sees the previous revision or the new one, never a prefix.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Stopped reports whether a person has asked this pool to stop.
func (p *Pool) Stopped() bool {
	_, err := os.Stat(p.Path(StopFile))
	return err == nil
}
