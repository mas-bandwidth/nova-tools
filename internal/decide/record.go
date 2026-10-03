package decide

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/rogpeppe/go-internal/lockedfile"
)

// The record (SPEC-NOVA-DECIDE section 4) is one JSON-lines file the caller
// names: a {"decision": ...} line per decision made, and an {"outcome": ...}
// line per outcome attached. Lines are only appended, by a writer holding the
// record file's own exclusive lock (go-internal/lockedfile); a reader takes the
// shared lock, so it never meets half a line and two writers never both take
// one id. Loading folds each outcome into its decision.

// Decision is one decision made: what was asked, over what, by which backend,
// and what it answered.
type Decision struct {
	ID       string            `json:"id"`
	Decision string            `json:"decision"` // the schema's name
	Schema   string            `json:"schema"`   // the schema's hash
	Backend  string            `json:"backend"`
	At       string            `json:"at"` // RFC 3339, UTC
	Inputs   map[string]string `json:"inputs"`
	State    string            `json:"state"` // the exact text asked over: the training input
	Answers  map[string]Answer `json:"answers"`
	Usage    Usage             `json:"usage"`
	Outcome  *Outcome          `json:"outcome,omitempty"` // folded in on load; never written on this line
}

// Outcome is what turned out to be true about a decision: a review's label, a
// gate's result. It is attached once; a second, different label is a conflict.
type Outcome struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	At    string `json:"at"`
}

type line struct {
	Decision *Decision `json:"decision,omitempty"`
	Outcome  *Outcome  `json:"outcome,omitempty"`
}

// ErrUnknown is an outcome for an id the record holds no decision for.
var ErrUnknown = errors.New("no decision with that id in the record")

// ConflictError is an id already recorded with other content: an op id reused
// over another state, or a decision already labelled otherwise.
type ConflictError struct{ What string }

func (e *ConflictError) Error() string { return e.What }

// Load reads the record; a record that does not exist yet is empty. A line
// that does not parse, a decision id seen twice, or an outcome for an id with
// no decision before it is an error naming the line.
func Load(path string) ([]Decision, error) {
	f, err := lockedfile.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parse(f, path)
}

// parse reads record lines from r; path names them in an error.
func parse(r io.Reader, path string) ([]Decision, error) {
	var out []Decision
	at := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 64<<20)
	for n := 1; sc.Scan(); n++ {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("%s:%d is not a record line: %w", path, n, err)
		}
		switch {
		case l.Decision != nil:
			if _, dup := at[l.Decision.ID]; dup {
				return nil, fmt.Errorf("%s:%d records decision %s a second time", path, n, l.Decision.ID)
			}
			at[l.Decision.ID] = len(out)
			out = append(out, *l.Decision)
		case l.Outcome != nil:
			i, ok := at[l.Outcome.ID]
			if !ok {
				return nil, fmt.Errorf("%s:%d: an outcome for %s: %w", path, n, l.Outcome.ID, ErrUnknown)
			}
			if out[i].Outcome != nil {
				return nil, fmt.Errorf("%s:%d records an outcome for %s a second time; an outcome is attached once", path, n, l.Outcome.ID)
			}
			o := *l.Outcome
			out[i].Outcome = &o
		default:
			return nil, fmt.Errorf("%s:%d is neither a decision nor an outcome", path, n)
		}
	}
	return out, sc.Err()
}

// Find is the decision with id, or nil.
func Find(ds []Decision, id string) *Decision {
	for i := range ds {
		if ds[i].ID == id {
			return &ds[i]
		}
	}
	return nil
}

// Append records d unless its id is already there: the same id over the same
// state returns the recorded decision (an op retried), over another state it
// is a ConflictError.
func Append(path string, d Decision) (recorded *Decision, err error) {
	err = locked(path, func(ds []Decision) (*line, error) {
		if have := Find(ds, d.ID); have != nil {
			recorded = have
			return nil, replays(*have, d.Decision, d.Schema, d.State)
		}
		d.Outcome = nil
		return &line{Decision: &d}, nil
	})
	return recorded, err
}

// Attach records o against its decision and returns the decision with it. The
// same label again changes nothing (changed is false); another label is a
// ConflictError naming both.
func Attach(path string, o Outcome) (d Decision, changed bool, err error) {
	err = locked(path, func(ds []Decision) (*line, error) {
		have := Find(ds, o.ID)
		switch {
		case have == nil:
			return nil, fmt.Errorf("%s: %w", o.ID, ErrUnknown)
		case have.Outcome != nil && have.Outcome.Label == o.Label:
			d = *have
			return nil, nil
		case have.Outcome != nil:
			return nil, &ConflictError{fmt.Sprintf("decision %s is labelled %s already (at %s), not %s; an outcome is attached once",
				o.ID, have.Outcome.Label, have.Outcome.At, o.Label)}
		}
		d, changed = *have, true
		d.Outcome = &o
		return &line{Outcome: &o}, nil
	})
	return d, changed, err
}

// locked runs plan over the record under the record file's exclusive lock
// and appends the line it returns, if any, before the lock is released.
func locked(path string, plan func([]Decision) (*line, error)) (err error) {
	f, err := lockedfile.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	ds, err := parse(f, path)
	if err != nil {
		return err
	}
	add, err := plan(ds)
	if err != nil || add == nil {
		return err
	}
	raw, err := json.Marshal(add)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	return err
}
