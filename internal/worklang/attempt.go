package worklang

// The WRITER for A3 and A4 (docs/SPEC-WORKLANG.md, Amendment 1). Every other
// file in this package reads; this one edits, and it edits ONE unit.
//
// A3 says an attempt is a record with a termination proof and A4 says
// `uncertain` is a state that keeps its reservation. Both had readers and no
// writer, which meant the only way a real work set could grow an `:attempts`
// list was a person typing s-expressions into a document by hand -- and the
// first hand edit that mis-nests a paren costs the whole file, because the
// reader refuses a set whole rather than half-reading it.
//
// The rule this file is built around: a work set is a PERSON'S DOCUMENT. Its
// comments, its blank lines, the order its author wrote the keys in and the
// column they line up at are the document, not incidental whitespace. So the
// writer never re-renders the file and never re-renders the units it is not
// touching: it splices bytes. Every byte outside the one edited unit comes back
// identical, and inside the unit every byte outside the edited key does too.
// TestRecordRoundTripsEveryByteButTheEditedUnit is the pin.
//
// The state machine, spelled in the grammar's own words rather than a second
// vocabulary beside it (A4's closed set is open | ready | live | blocked |
// uncertain | closed | refused | abandoned):
//
//	take           -> :state :live,      one attempt OPEN (:outcome :uncertain)
//	outcome green  -> :state :closed     the unit is done
//	outcome red    -> :state :open       it re-enters the ladder; the ladder IS
//	                                     the retry policy (SPEC-DECIDE)
//	outcome refused-> :state :refused
//	outcome abandoned -> :state :abandoned
//	outcome uncertain -> :state :uncertain, and the reservation is KEPT (A4)
//
// There is no `running` state and no `done` state, because the grammar names
// those `live` and `closed` and one thing does not get two names.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The outcome spellings a person types at the CLI, mapped onto the grammar's
// closed set. `ok` and `failed` are what a caller reaches for; `green` and
// `red` are what A3 fixed. Both are accepted and only the grammar's is ever
// written to the file, so the document holds one vocabulary.
var outcomeSpellings = map[string]string{
	"ok": "green", "green": "green",
	"failed": "red", "fail": "red", "red": "red",
	"refused":   "refused",
	"abandoned": "abandoned",
	"uncertain": "uncertain",
}

// CanonicalOutcome maps a caller's spelling onto the grammar's outcome. The
// second result is false for a word that is neither.
func CanonicalOutcome(s string) (string, bool) {
	out, ok := outcomeSpellings[strings.ToLower(strings.TrimSpace(s))]
	return out, ok
}

// OutcomeSpellings is every word --outcome accepts, in a stable order, for the
// refusal that names them.
func OutcomeSpellings() []string {
	out := make([]string, 0, len(outcomeSpellings))
	for k := range outcomeSpellings {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// StateAfter is the state an outcome leaves the unit in: A4's machine in one
// function, so the CLI and any later caller cannot disagree about it.
func StateAfter(outcome string) string {
	switch outcome {
	case "green":
		return "closed"
	case "red":
		// Not a state of its own: a failed attempt leaves the unit open, and
		// the next rung of the ladder is the retry policy.
		return "open"
	case "refused":
		return "refused"
	case "abandoned":
		return "abandoned"
	default:
		return "uncertain"
	}
}

// StateLive is the state a taken unit is in while its attempt runs.
const StateLive = "live"

// Proof is the termination proof A3 requires of every outcome but `uncertain`:
// what kind of evidence it is and the evidence itself. A3's own examples are an
// exit, a merge, a reaped pid and a fencing token; what a caller has at hand is
// a path, a sha or a url, and the kind is READ off the value rather than asked
// for twice.
type Proof struct {
	Kind  string
	Value string
}

var shaLike = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// ProofOf reads a --proof value and says what kind of evidence it is. A url is
// one with a scheme, a sha is 7 to 64 hex digits, and everything else is a
// path: three readings of the same bytes, none of them a guess a caller has to
// confirm. An empty value is no proof at all.
func ProofOf(value string) (Proof, bool) {
	v := strings.TrimSpace(value)
	switch {
	case v == "":
		return Proof{}, false
	case strings.HasPrefix(v, "https://"), strings.HasPrefix(v, "http://"):
		return Proof{Kind: "url", Value: v}, true
	case shaLike.MatchString(v):
		return Proof{Kind: "sha", Value: v}, true
	default:
		return Proof{Kind: "path", Value: v}, true
	}
}

// NewAttempt is one attempt to record: who ran it, when it started, how it
// ended, the proof that it ended, and the two optional pointers a run leaves
// behind -- the usage TSV it spent into and the PR it produced.
type NewAttempt struct {
	Rung    string
	Owner   string
	Started string
	Outcome string
	Proof   Proof
	Usage   string
	PR      int
}

// Recorded is what one edit did: the record as written, whether it closed an
// attempt that was already open rather than appending a new one, and the state
// the unit now carries.
type Recorded struct {
	N      int64
	Closed bool
	State  string
	// Rung, Owner and Started are the record AS WRITTEN. They are not always
	// what the caller passed: closing an open attempt keeps the rung, the owner
	// and the instant the try actually began, because that is when it began.
	Rung    string
	Owner   string
	Started string
	Outcome string
	Bytes   []byte
}

// Record appends -- or closes -- one attempt on one unit of a work set, editing
// the file's bytes in place.
//
// It CLOSES rather than appends when the unit's last attempt is still open: an
// `:outcome :uncertain` with no `:proof`, taken by this same owner. A try that
// started and then ended is ONE try (A3: "an attempt is a record, not a
// counter"), so the record that was opened by `next --take` is the record that
// is finished here, keeping its `:n`, its `:rung` and its `:started`. Any other
// last attempt is a new try and a new record.
//
// Every refusal is made BEFORE a byte is written, so a file is never left half
// edited: an outcome that is not one of A3's, an outcome other than `uncertain`
// with no proof, a unit the set does not hold, and a unit already in a terminal
// state are all refused with the document untouched.
func Record(file string, data []byte, unitID string, a NewAttempt, limits Limits) ([]byte, Recorded, error) {
	return record(file, data, unitID, a, limits, "")
}

// record is the one edit, with the state the caller wants the unit left in.
// Record leaves it in the state the outcome implies; Take leaves it `live`,
// because a try that has not finished starting has not stopped either.
func record(file string, data []byte, unitID string, a NewAttempt, limits Limits, state string) ([]byte, Recorded, error) {
	ws, err := ParseWorkSet(file, data, limits)
	if err != nil {
		return nil, Recorded{}, err
	}
	u, ok := ws.Unit(unitID)
	if !ok {
		return nil, Recorded{}, refuse(file, fmt.Sprintf(
			"the set holds no unit %q; an attempt is filed under the id it was run for, and an id is never guessed at", unitID))
	}
	outcome, ok := CanonicalOutcome(a.Outcome)
	if !ok {
		return nil, Recorded{}, refuse(file, fmt.Sprintf(
			"outcome %q is not one of %s", a.Outcome, strings.Join(OutcomeSpellings(), ", ")))
	}
	// A3's door, held here as well as in the reader: an outcome that claims to
	// have ended without proving it is refused NAMING THE WORD the author must
	// write instead.
	if outcome != "uncertain" && a.Proof.Value == "" {
		return nil, Recorded{}, refuse(file, fmt.Sprintf(
			"attempt on unit %q ends :%s with no :proof of termination; give --proof <path|sha|url>, or record --outcome uncertain, which keeps the reservation until termination is proved",
			unitID, outcome))
	}
	if state := u.State(); terminalStates[state] {
		return nil, Recorded{}, refuse(file, fmt.Sprintf(
			"unit %q is :%s; a terminal unit takes no further attempt, and a renamed or reopened piece of work is a NEW id carrying :was (A2)",
			unitID, state))
	}
	if strings.TrimSpace(a.Rung) == "" {
		return nil, Recorded{}, refuse(file, fmt.Sprintf(
			"attempt on unit %q names no rung; the rung that ran it is evidence the ladder reads", unitID))
	}

	attempts := u.Attempts()
	rec := Recorded{N: int64(len(attempts)) + 1, State: StateAfter(outcome)}
	if state != "" {
		rec.State = state
	}
	var edits []edit
	if open, isOpen := openAttempt(attempts); isOpen && sameOwner(open.Owner, a.Owner) {
		rec.N, rec.Closed = open.N, true
		filled := a
		filled.Rung, filled.Owner, filled.Started = open.Rung, open.Owner, open.Started
		rec.Bytes = renderAttempt(rec.N, filled, outcome)
		rec.Rung, rec.Owner, rec.Started, rec.Outcome = filled.Rung, filled.Owner, filled.Started, outcome
		edits = append(edits, edit{at: open.Offset, end: open.End, text: string(rec.Bytes)})
	} else {
		for _, at := range attempts {
			if at.N >= rec.N {
				rec.N = at.N + 1
			}
		}
		rec.Bytes = renderAttempt(rec.N, a, outcome)
		rec.Rung, rec.Owner, rec.Started, rec.Outcome = a.Rung, a.Owner, a.Started, outcome
		edits = append(edits, insertAttempt(data, u, string(rec.Bytes)))
	}
	edits = append(edits, setState(data, u, rec.State))
	return apply(data, edits), rec, nil
}

// terminalStates are the states no further attempt may be filed under. They are
// A4's three endings; `uncertain` is deliberately NOT one of them, because an
// unproved termination is exactly the state a later attempt closes.
var terminalStates = map[string]bool{"closed": true, "refused": true, "abandoned": true}

// Take opens an attempt: the record `next --take` writes when a mind picks a
// unit up. It is an ordinary attempt with the only outcome an unfinished try
// can honestly carry -- `uncertain`, which owes no proof precisely because
// nothing has terminated -- and it moves the unit to `live`, so the lane and
// the writes it holds are charged to it from this instant.
func Take(file string, data []byte, unitID string, a NewAttempt, limits Limits) ([]byte, Recorded, error) {
	ws, err := ParseWorkSet(file, data, limits)
	if err != nil {
		return nil, Recorded{}, err
	}
	// A unit already live or uncertain is holding its reservation (A4), and
	// taking it again would be the second reservation of the same capacity that
	// A8 calls a refusal rather than a wait.
	if u, ok := ws.Unit(unitID); ok {
		if state := u.State(); state == StateLive || state == "uncertain" {
			return nil, Recorded{}, refuse(file, fmt.Sprintf(
				"unit %q is :%s and still holds its reservation; record its attempt's outcome before taking it again",
				unitID, state))
		}
	}
	a.Outcome = "uncertain"
	a.Proof = Proof{}
	return record(file, data, unitID, a, limits, StateLive)
}

// openAttempt is the unit's last attempt when that attempt is still running: an
// `uncertain` outcome with no proof. Only the LAST one can be open, because a
// unit runs one attempt at a time -- it holds one reservation.
func openAttempt(attempts []Attempt) (Attempt, bool) {
	if len(attempts) == 0 {
		return Attempt{}, false
	}
	last := attempts[len(attempts)-1]
	return last, last.Outcome == "uncertain" && !last.HasProof
}

// sameOwner folds case, because a work set writes a mind's name the way a
// person does and a runner passes it the way a machine does. An attempt with no
// owner recorded is closable by anyone: there is nobody it would be taken from.
func sameOwner(recorded, by string) bool {
	if strings.TrimSpace(recorded) == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(recorded), strings.TrimSpace(by))
}

// renderAttempt writes one record in A3's own field order. Every field is
// written explicitly: a fixed shape costs a few bytes and saves every later
// reader an inference (the fixed-table rule -- no elision).
func renderAttempt(n int64, a NewAttempt, outcome string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "(:n %d :rung %s :owner %s :started %s :outcome :%s",
		n, quote(a.Rung), quote(a.Owner), quote(a.Started), outcome)
	if a.Proof.Value != "" {
		fmt.Fprintf(&b, " :proof (:kind :%s :value %s)", a.Proof.Kind, quote(a.Proof.Value))
	}
	if strings.TrimSpace(a.Usage) != "" {
		fmt.Fprintf(&b, " :usage %s", quote(a.Usage))
	}
	if a.PR > 0 {
		fmt.Fprintf(&b, " :pr %d", a.PR)
	}
	b.WriteString(")")
	return []byte(b.String())
}

// quote writes one string of the grammar. The reader's escape is a single
// backslash before the byte, so only the quote and the backslash need one.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// edit is one byte range of the document replaced by one string. An insertion
// is an edit whose range is empty.
type edit struct {
	at   int
	end  int
	text string
}

// apply splices the edits into the document, last byte first so that an earlier
// edit's offsets are still the offsets the reader gave. Nothing outside the
// ranges is touched: this is the whole of the byte stability guarantee.
func apply(data []byte, edits []edit) []byte {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].at > edits[j].at })
	out := append([]byte(nil), data...)
	for _, e := range edits {
		if e.text == "" && e.at == e.end {
			continue
		}
		tail := append([]byte(nil), out[e.end:]...)
		out = append(out[:e.at], append([]byte(e.text), tail...)...)
	}
	return out
}

// insertAttempt puts a rendered record into the unit's `:attempts`, adding the
// key itself when the unit carries none. A record joins the list at the column
// the records already sit at, so the document a person reads back looks like
// the document they wrote.
func insertAttempt(data []byte, u Unit, record string) edit {
	if list, ok := u.Fields["attempts"]; ok && list.Kind == List {
		if len(list.List) == 0 {
			return edit{at: list.End - 1, end: list.End - 1, text: record}
		}
		col := column(data, list.List[0].Offset)
		return edit{at: list.End - 1, end: list.End - 1,
			text: "\n" + strings.Repeat(" ", col) + record}
	}
	indent := keyIndent(data, u)
	return edit{at: u.End - 1, end: u.End - 1,
		text: "\n" + indent + ":attempts (" + record + ")"}
}

// setState writes `:state`, replacing the value when the unit carries one and
// appending the key when it does not. Only the VALUE's bytes move in the first
// case: the key, its spacing and everything around it stay as written.
func setState(data []byte, u Unit, state string) edit {
	if f, ok := u.Fields["state"]; ok && (f.Kind == Keyword || f.Kind == Symbol) {
		return edit{at: f.Offset, end: f.End, text: ":" + state}
	}
	return edit{at: u.End - 1, end: u.End - 1,
		text: "\n" + keyIndent(data, u) + ":state :" + state}
}

// keyIndent is the column the unit's own keys line up at, read off the document
// rather than chosen: a set written at one indentation and a set written at
// another both come back looking like themselves.
func keyIndent(data []byte, u Unit) string {
	for i := u.Offset; i < u.End && i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		j := i + 1
		for j < len(data) && data[j] == ' ' {
			j++
		}
		if j < len(data) && data[j] == ':' {
			return strings.Repeat(" ", j-i-1)
		}
	}
	// A unit written on one line has no key column to read. Two past the
	// unit's own opening paren is where the sets on the bench put them.
	return strings.Repeat(" ", column(data, u.Offset)+2)
}

// column is the byte offset's distance from the start of its line.
func column(data []byte, off int) int {
	if off > len(data) {
		off = len(data)
	}
	start := strings.LastIndexByte(string(data[:off]), '\n')
	return off - start - 1
}
