package decide

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"slices"

	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/rogpeppe/go-internal/lockedfile"
)

// The record (SPEC-NOVA-DECIDE section 4) is one JSON-lines file the caller
// names: a {"decision": ...} line per decision made, an {"outcome": ...}
// line per outcome attached, and an {"act": ...} line per step of applying a
// decision (a caller that acts on its decisions, as nova-sprint answer does). Lines are only appended, by a writer holding the
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
	Acts     []Act             `json:"acts,omitempty"`    // folded in on load, in order; never written on this line
}

// Outcome is what turned out to be true about a decision: a review's label, a
// gate's result. It is attached once; a second, different label is a conflict.
type Outcome struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	At    string `json:"at"`
}

// Act is one step of applying a decision, recorded around the verbs it runs: "applying"
// with the operation id those verbs carry, before them, then "applied" or "refused"
// after. A decision's acts are appended, never rewritten; its last says where applying
// it stands, so a writer stopped between the two finds "applying" and its op.
type Act struct {
	ID  string `json:"id"`
	Act string `json:"act"`
	Op  string `json:"op,omitempty"`
	At  string `json:"at"`
}

type line struct {
	Decision *Decision `json:"decision,omitempty"`
	Outcome  *Outcome  `json:"outcome,omitempty"`
	Act      *Act      `json:"act,omitempty"`
}

// ErrUnknown is an outcome for an id the record holds no decision for.
var ErrUnknown = errors.New("no decision with that id in the record")

// ConflictError is an id already recorded with other content: an op id reused
// over another state, or a decision already labelled otherwise.
type ConflictError struct{ What string }

func (e *ConflictError) Error() string { return e.What }

// Load reads the record; a record that does not exist yet is empty. A line
// that does not parse, a decision id seen twice, an outcome for an id with
// no decision before it, or an answer whose probability is outside [0, 1]
// or not a number, is an error naming the line (SPEC-NOVA-DECIDE section 4).
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
		l, _, err := RecordLineReader.Read(sc.Bytes())
		if err != nil {
			return nil, fmt.Errorf("%s:%d is not a record line: %w", path, n, err)
		}
		count := 0
		if l.Decision != nil {
			count++
		}
		if l.Outcome != nil {
			count++
		}
		if l.Act != nil {
			count++
		}
		if count > 1 {
			return nil, fmt.Errorf("%s:%d holds more than one of decision, outcome and act; a line is one record", path, n)
		}
		switch {
		case l.Decision != nil:
			if _, dup := at[l.Decision.ID]; dup {
				return nil, fmt.Errorf("%s:%d records decision %s a second time", path, n, l.Decision.ID)
			}
			if err := recordedProbabilities(path, n, l.Decision.Answers); err != nil {
				return nil, err
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
		case l.Act != nil:
			i, ok := at[l.Act.ID]
			if !ok {
				return nil, fmt.Errorf("%s:%d: an act for %s: %w", path, n, l.Act.ID, ErrUnknown)
			}
			out[i].Acts = append(out[i].Acts, *l.Act)
		default:
			return nil, fmt.Errorf("%s:%d is neither a decision, an outcome nor an act", path, n)
		}
	}
	return out, sc.Err()
}

// recordedProbabilities refuses an answer whose probability is outside [0, 1]
// or not a number. The record is the calibration's input, so a hand-edited
// line does not load (SPEC-NOVA-DECIDE section 4).
func recordedProbabilities(path string, line int, answers map[string]Answer) error {
	names := make([]string, 0, len(answers))
	for name := range answers {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		opts := make([]string, 0, len(answers[name].P))
		for opt := range answers[name].P {
			opts = append(opts, opt)
		}
		slices.Sort(opts)
		for _, opt := range opts {
			v := answers[name].P[opt]
			if math.IsNaN(v) {
				return fmt.Errorf("%s:%d: question %s gives %s a probability that is not a number", path, line, name, opt)
			}
			if v < 0 || v > 1 {
				return fmt.Errorf("%s:%d: question %s gives %s the probability %v, outside [0, 1]", path, line, name, opt, v)
			}
		}
	}
	return nil
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
	err = locked(path, func(ds []Decision) ([]line, error) {
		if have := Find(ds, d.ID); have != nil {
			recorded = have
			return nil, replays(*have, d.Decision, d.Schema, d.State)
		}
		d.Outcome, d.Acts = nil, nil
		return []line{{Decision: &d}}, nil
	})
	return recorded, err
}

// Attach records o against its decision and returns the decision with it. The
// same label again changes nothing (changed is false); another label is a
// ConflictError naming both.
func Attach(path string, o Outcome) (d Decision, changed bool, err error) {
	err = locked(path, func(ds []Decision) ([]line, error) {
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
		return []line{{Outcome: &o}}, nil
	})
	return d, changed, err
}

// RecordAct appends a's line against its decision: a step of applying it. A decision
// the record does not hold is ErrUnknown, and nothing is written.
func RecordAct(path string, a Act) error {
	return locked(path, func(ds []Decision) ([]line, error) {
		if Find(ds, a.ID) == nil {
			return nil, fmt.Errorf("%s: %w", a.ID, ErrUnknown)
		}
		return []line{{Act: &a}}, nil
	})
}

// locked runs plan over the record under the record file's exclusive lock
// and appends the lines it returns, if any, in one write before the lock is
// released.
func locked(path string, plan func([]Decision) ([]line, error)) (err error) {
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
	if err != nil || len(add) == 0 {
		return err
	}
	var out []byte
	for _, l := range add {
		line, err := recordLineWriter.Write(&l, schemaread.FormBase64)
		if err != nil {
			return err
		}
		out = append(out, append(line, '\n')...)
	}
	_, err = f.Write(out)
	return err
}

// setText writes the text of a table row field (S2, the table's text form).
func setText(w *decidewire.TableWorker, slot *int64, s string) {
	if s != "" {
		decidewire.TableStringEmplace(w, slot, []byte(s))
	}
}

// sortedKeys returns a map's keys in order, so a table image is the same on
// every run.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// fillDecisionRow builds a decision table row from the record's decision.
func fillDecisionRow(w *decidewire.TableWorker, r *decidewire.DecisionRow, d *Decision) {
	setText(w, &r.Id, d.ID)
	setText(w, &r.DecisionName, d.Decision)
	setText(w, &r.Schema, d.Schema)
	setText(w, &r.Backend, d.Backend)
	setText(w, &r.At, d.At)
	setText(w, &r.State, d.State)
	r.Usage = decidewire.UsageRow{InputTokens: int64(d.Usage.InputTokens), OutputTokens: int64(d.Usage.OutputTokens)}
	for _, k := range sortedKeys(d.Inputs) {
		in := decidewire.DecisionInputsAdd(w, &r.Inputs)
		setText(w, &in.Key, k)
		setText(w, &in.Value, d.Inputs[k])
	}
	for _, q := range sortedKeys(d.Answers) {
		a := d.Answers[q]
		na := decidewire.DecisionAnswersAdd(w, &r.Answers)
		setText(w, &na.Question, q)
		switch a.Type {
		case Noul:
			na.Answer.Kind = decidewire.AnswerTypeNoul
		default:
			na.Answer.Kind = decidewire.AnswerTypeChoice
		}
		setText(w, &na.Answer.Value, a.Value)
		for _, opt := range sortedKeys(a.P) {
			o := decidewire.AnswerPAdd(w, &na.Answer.P)
			setText(w, &o.Option, opt)
			o.P = a.P[opt]
		}
	}
}

// recordLineWire builds the record line's binary image from a line, in the
// generated table of pkg/decidewire that the reader reads.
func recordLineWire(l *line) []byte {
	var b decidewire.RecordLineBuilder
	if !b.Init() {
		return nil
	}
	defer b.Shutdown()
	w := b.GetRoot()
	switch {
	case l.Decision != nil:
		w.Kind = decidewire.LineKindDecision
		fillDecisionRow(&b.Main, decidewire.DecisionEmplace(&b.Main, &w.AsDecision), l.Decision)
	case l.Outcome != nil:
		w.Kind = decidewire.LineKindOutcome
		o := decidewire.OutcomeEmplace(&b.Main, &w.AsOutcome)
		setText(&b.Main, &o.Id, l.Outcome.ID)
		setText(&b.Main, &o.Label, l.Outcome.Label)
		setText(&b.Main, &o.Note, l.Outcome.Note)
		setText(&b.Main, &o.At, l.Outcome.At)
	case l.Act != nil:
		w.Kind = decidewire.LineKindAct
		a := decidewire.ActEmplace(&b.Main, &w.AsAct)
		setText(&b.Main, &a.Id, l.Act.ID)
		setText(&b.Main, &a.Step, l.Act.Act)
		setText(&b.Main, &a.Op, l.Act.Op)
		setText(&b.Main, &a.At, l.Act.At)
	}
	if !b.Lock() {
		return nil
	}
	root := b.AsConst()
	n := decidewire.RecordLineMeasure(root)
	buf := make([]byte, n)
	decidewire.RecordLineSave(root, buf)
	return buf
}

// recordLineWriter writes a record line in the table's form: the padded
// standard base64 of its binary image (S2). A value the table cannot hold is
// refused by name before a byte is written (S2, the schema's declared bounds).
var recordLineWriter = schemaread.NewReader(&schemaread.Codec[line]{
	Name:        "RECORD_LINE",
	Help:        "nova-decide record -h",
	Reset:       func(v *line) { *v = line{} },
	Constraints: recordLineCheck,
	SaveWire:    recordLineWire,
})
