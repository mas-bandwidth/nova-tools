// Package friends is the machinery's side of one rule (Glenn, 2026-09-18):
//
//	THE MACHINERY ROUTES TO FRIENDS. A unit of work whose owner is a friend is
//	delivered as a bus ask by machinery, never as a Flash card. A friend pulls
//	asks over the bus the way a bench pulls cards.
//
// A bench gets a card: a prompt, a slot, a deadline and a RESULT.md contract, and
// nova-swarm and nova-pulse carry it. A friend gets a NOTE: the same unit in the
// house shape a person reads -- To, Subject, the title, the needs, the acceptance,
// the deadline and the branch to reply on -- and the bus carries it. Nothing here
// invents a second transport: the note goes out through nova-bus's own send path,
// behind the Sender seam below, so the one thing a fake replaces in a test is the
// subprocess and never the shape of what was sent.
//
// A work set is read as DATA and written back atomically: the ask id is recorded
// on the unit it asked about, so `asks` can answer what is outstanding from the
// same file and nothing has to remember anything between runs.
//
// Every path this package touches comes from a caller's flag. It reads no
// environment, discovers no file and reaches no network of its own.
package friends

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// DefaultMaxBytes is the work set's byte ceiling, the same 64 KiB the bounded plan
// reader holds. A file past it is refused WHOLE rather than truncated: half a work
// set is a set of units somebody would be asked about and nobody would be told.
const DefaultMaxBytes = int64(65536)

// Kinds are what an ask may be. `work` is a unit to do; `read` is the read a bench
// gets as a `nova-pulse cut --kind read` card and a friend gets as a note.
var Kinds = []string{"work", "read"}

// ValidKind refuses anything else by name, rather than sending a note whose subject
// says a word the reader has never been given a meaning for.
func ValidKind(k string) error {
	for _, ok := range Kinds {
		if k == ok {
			return nil
		}
	}
	return fmt.Errorf("unknown --kind %q; it is one of %s", k, strings.Join(Kinds, ", "))
}

// Ask is one delivered ask, recorded on the unit it was cut from. Its ID is the
// bus's own note id: there is no second identifier, so a reply's Re line and this
// record name the same thing.
type Ask struct {
	ID       string    `json:"id"`
	Owner    string    `json:"owner"`
	Kind     string    `json:"kind"`
	Unit     string    `json:"unit"`
	Sent     time.Time `json:"sent"`
	Deadline time.Time `json:"deadline"`
	Lane     string    `json:"lane,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	By       string    `json:"by,omitempty"`
	Bus      string    `json:"bus,omitempty"`
	Answered bool      `json:"answered,omitempty"`
}

// Unit is one unit of the work set: what it is, who owns it, what it needs, what
// would make it done, and every ask that has carried it to a friend.
type Unit struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Owner      string   `json:"owner,omitempty"`
	Lane       string   `json:"lane,omitempty"`
	Needs      []string `json:"needs,omitempty"`
	Acceptance []string `json:"acceptance,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	// Deadline is the unit's OWN written deadline, which a coordinator states in the
	// work set rather than on every ask's command line. --deadline overrides it, and
	// a unit with neither is refused: a deadline is never guessed.
	Deadline Stamp  `json:"deadline,omitempty"`
	Asks     []Ask  `json:"asks,omitempty"`
	Source   string `json:"-"`
}

// WorkSet is the units file, read as data. Lisp is set when it was read from the
// SPEC-WORKLANG grammar rather than JSON: that file is a person's document, with its
// comments and its order, and this package reads it without ever writing it back.
type WorkSet struct {
	Units []Unit `json:"units"`
	Lisp  bool   `json:"-"`
}

// Load reads one work set under a byte ceiling, in EITHER form: the JSON shape this
// package writes, or the SPEC-WORKLANG Lisp a coordinator writes by hand
// (work/pitstop-2026-09-17.lisp). The form is READ, not guessed at from the file's
// name: the first byte that is not whitespace or a comment is `{` or `(`.
//
// A file past the ceiling is refused naming the ceiling and the size, before a byte
// is parsed.
func Load(path string, maxBytes int64) (*WorkSet, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, fmt.Errorf("%s is %d bytes, past the %d-byte ceiling; refused whole rather than truncated", path, info.Size(), maxBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch firstByte(raw) {
	case '(':
		return parseLisp(path, raw, maxBytes)
	case '{':
		var ws WorkSet
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&ws); err != nil {
			return nil, fmt.Errorf("%s is not a work set: %w", path, err)
		}
		for i := range ws.Units {
			ws.Units[i].Source = path
		}
		return &ws, nil
	default:
		return nil, fmt.Errorf("%s is neither a JSON work set (it would open `{`) nor a SPEC-WORKLANG one (it would open `(`); refusing to guess", path)
	}
}

// firstByte is the first byte that is not whitespace and not inside a `;` line
// comment, which is what says which of the two grammars this file is written in.
func firstByte(raw []byte) byte {
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f':
		case c == ';':
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
		default:
			return c
		}
	}
	return 0
}

// parseLisp reads a work set in the SPEC-WORKLANG grammar. It does NOT read it: it
// hands the bytes to worklang.ParseWorkSet, THE reader of that form, and maps what
// comes back onto this package's Unit.
//
// There used to be two readers of one grammar -- `nova-work set check` had worklang's
// and `nova-work ask` had its own, each with its own key list, its own idea of what a
// unit is and its own stamp parser. A unit one of them read and the other did not was
// a unit one verb could check and the other could not ask about, and nothing in either
// file said so. The reader is the language's; everything below is a mapping.
func parseLisp(path string, raw []byte, maxBytes int64) (*WorkSet, error) {
	limits := worklang.DefaultLimits()
	if maxBytes > 0 {
		limits.MaxBytes = int(maxBytes)
	}
	// The tolerant door: a member that is not a (unit "id" ...) is a FINDING of
	// `set check`, reported there in one pass, and it is skipped below. The
	// strict door refuses the whole file for it, which is right for a kernel
	// loading a set and wrong for an ask that only wants one unit of it.
	set, err := worklang.ParseWorkSetTolerant(path, raw, limits)
	if err != nil {
		return nil, err
	}
	ws := &WorkSet{Lisp: true}
	for _, u := range set.Units {
		if u.ID == "" {
			// A member that is not a (unit "id" ...) is a FINDING of `set check`, which is the
			// verb that reports on the content of a set. Here it is skipped: an ask needs a
			// unit to name, and there is no name.
			continue
		}
		unit := Unit{
			ID: u.ID, Title: u.Title(), Owner: u.Owner(), Lane: u.Lane(),
			Branch: u.Branch(), Needs: u.Needs(), Acceptance: u.AcceptanceText(), Source: path,
		}
		if u.Deadline() != "" {
			at, err := ParseStamp(u.Deadline())
			if err != nil {
				return nil, fmt.Errorf("%s: unit %q has a :deadline this reader cannot read: %w", path, u.ID, err)
			}
			unit.Deadline = Stamp{Time: at}
		}
		ws.Units = append(ws.Units, unit)
	}
	return ws, nil
}

// Stamp is an instant written the way a person writes one. The work set carries
// "2026-09-18T18:00Z" -- RFC3339 without the seconds -- and a reader that took only
// the full spelling refused a real unit, so both are read and the full one is written.
type Stamp struct{ time.Time }

// ParseStamp is worklang's own, named here so a caller of this package does not have
// to import the language to read a stamp this package handed it. There is one list of
// spellings and one parser: a second copy is how the work set came to have two
// readers that disagreed about which instants were instants.
func ParseStamp(s string) (time.Time, error) { return worklang.ParseStamp(s) }

// MarshalJSON writes the full RFC3339 spelling, and nothing at all for a zero stamp.
func (s Stamp) MarshalJSON() ([]byte, error) {
	if s.IsZero() {
		return []byte(`""`), nil
	}
	return json.Marshal(s.UTC().Format(time.RFC3339))
}

// UnmarshalJSON reads either spelling, so a work set a person edited by hand loads.
func (s *Stamp) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		s.Time = time.Time{}
		return nil
	}
	at, err := ParseStamp(text)
	if err != nil {
		return err
	}
	s.Time = at
	return nil
}

// Unit finds one unit by id, refusing an absent one BY NAME rather than answering a
// zero unit that would be asked about as if it existed.
func (w *WorkSet) Unit(id string) (*Unit, error) {
	for i := range w.Units {
		if w.Units[i].ID == id {
			return &w.Units[i], nil
		}
	}
	return nil, fmt.Errorf("no unit %q in this work set; its %d units are %s", id, len(w.Units), strings.Join(w.ids(), ", "))
}

func (w *WorkSet) ids() []string {
	out := make([]string, 0, len(w.Units))
	for _, u := range w.Units {
		out = append(out, u.ID)
	}
	return out
}

// Record writes one ask onto its unit. It is called only after the note has landed:
// an ask recorded for a note that never went out is a coordinator waiting on a
// deadline nobody was ever given.
func (w *WorkSet) Record(unitID string, a Ask) error {
	u, err := w.Unit(unitID)
	if err != nil {
		return err
	}
	for _, have := range u.Asks {
		if have.ID == a.ID {
			return fmt.Errorf("unit %q already records ask %q", unitID, a.ID)
		}
	}
	u.Asks = append(u.Asks, a)
	return nil
}

// Save writes the work set back through a temporary file in the SAME directory and
// one rename, so a reader between the two sees the old file whole or the new one and
// never half of either.
func Save(path string, w *WorkSet) error {
	raw, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Row is one open ask as `asks` prints it: the ask, how long it has been out, and
// whether its deadline has passed.
type Row struct {
	Ask
	Age     time.Duration
	Overdue bool
	// Source says which reader found this row: "units" for the work set's own record,
	// "bus" for the note the bus holds. A coordinator reading a mixed listing needs to
	// know which of the two it is looking at.
	Source string
}

// Open is every unanswered ask in the work set, oldest first, with its age measured
// against now and its deadline compared with it.
func (w *WorkSet) Open(now time.Time) []Row {
	var rows []Row
	for _, u := range w.Units {
		for _, a := range u.Asks {
			if a.Answered {
				continue
			}
			rows = append(rows, Row{Ask: a, Age: now.Sub(a.Sent), Overdue: !a.Deadline.IsZero() && now.After(a.Deadline), Source: "units"})
		}
	}
	// oldest first: what has been out longest is what a coordinator owes an answer on.
	SortOldestFirst(rows)
	return rows
}

// ---------------------------------------------------------------- the note

// AskSpec is everything one note is rendered from. Nothing here is discovered: the
// caller's flags and the unit supply every field.
type AskSpec struct {
	From     string
	Owner    string
	Cc       string
	Kind     string
	Branch   string
	Unit     Unit
	Deadline time.Time
}

// Render is the house shape, built by the bus's OWN header renderer so that a note
// this tool writes and a note a person writes by hand are the same file. It answers
// the note and the NOTICES: what it did that a reader could not otherwise check, one
// line each, the way nova-bus prints its own SEND NOTE lines.
//
// It refuses rather than repairs where a repair would be a guess -- a title with a
// newline in it would forge a header line under the Subject -- but a unit with no
// :acceptance is NOT one of those. Not one unit of the real work set carries an
// :acceptance, and refusing them all made the verb unusable on the only file it was
// written for; the title IS the acceptance in that grammar, so the note says
// "Acceptance: as titled" and a notice says the unit carried none.
func Render(spec AskSpec) (string, []string, error) {
	if strings.TrimSpace(spec.From) == "" {
		return "", nil, errors.New("an ask needs a sender; refusing to guess one")
	}
	if strings.TrimSpace(spec.Owner) == "" {
		return "", nil, errors.New("an ask needs an owner; refusing to guess one")
	}
	if err := ValidKind(spec.Kind); err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(spec.Unit.ID) == "" {
		return "", nil, errors.New("an ask needs a unit id")
	}
	title := strings.TrimSpace(spec.Unit.Title)
	if title == "" {
		return "", nil, fmt.Errorf("unit %q has no title; a note whose subject is empty says nothing", spec.Unit.ID)
	}
	if spec.Deadline.IsZero() {
		return "", nil, errors.New("an ask needs a deadline; every ask has a written one")
	}
	subject := "ask " + spec.Kind + ": " + title
	if err := bus.OneLine("--subject", subject); err != nil {
		return "", nil, err
	}
	cc := ccWithSelf(spec.Cc, spec.From, spec.Owner)
	for _, v := range []string{spec.Owner, spec.From, cc, spec.Branch, spec.Unit.ID, spec.Unit.Lane} {
		if err := bus.OneLine("a header value", v); err != nil {
			return "", nil, err
		}
	}

	var notices []string
	var b strings.Builder
	// The title is the Subject and is not repeated here: a note that opened with the
	// line directly above it read as a stutter to the first person who was sent one.
	b.WriteString("Unit: " + spec.Unit.ID + "\n")
	b.WriteString("Kind: " + spec.Kind + "\n")
	if spec.Unit.Lane != "" {
		b.WriteString("Lane: " + spec.Unit.Lane + "\n")
	}
	if len(spec.Unit.Needs) > 0 {
		b.WriteString("Needs:\n")
		for _, n := range spec.Unit.Needs {
			b.WriteString("- " + strings.TrimSpace(n) + "\n")
		}
	}
	if len(spec.Unit.Acceptance) > 0 {
		b.WriteString("Acceptance:\n")
		for _, a := range spec.Unit.Acceptance {
			b.WriteString("- " + strings.TrimSpace(a) + "\n")
		}
	} else {
		b.WriteString("Acceptance: as titled\n")
		notices = append(notices, fmt.Sprintf("unit %q carries no acceptance; the title stands as it, and the note says so", spec.Unit.ID))
	}
	b.WriteString("Deadline: " + spec.Deadline.UTC().Format(time.RFC3339) + "\n")
	where := "Reply on the bus"
	if spec.Branch != "" {
		b.WriteString("Reply on branch: " + spec.Branch + "\n")
		where = "Reply on the bus, on that branch,"
	} else {
		where = "Reply on the bus"
	}
	// WHY it is yours is a claim about the file, and the person least able to check the
	// file is the person being told. A unit that carries no :owner -- which is most of
	// the real work set -- was still told "the unit names you as its owner", which was
	// not true of any of them. The sentence now says which of the two it is.
	why := "the unit names you as its owner"
	switch owner := strings.TrimSpace(spec.Unit.Owner); {
	case owner == "":
		why = "it was assigned by " + strings.TrimSpace(spec.From)
		notices = append(notices, fmt.Sprintf("unit %q names no owner; the note says it was assigned by %s rather than claiming the unit named you", spec.Unit.ID, strings.TrimSpace(spec.From)))
	case !strings.EqualFold(owner, strings.TrimSpace(spec.Owner)):
		why = "it was assigned by " + strings.TrimSpace(spec.From)
		notices = append(notices, fmt.Sprintf("unit %q names %s as its owner, not %s; the note says it was assigned by %s", spec.Unit.ID, owner, strings.TrimSpace(spec.Owner), strings.TrimSpace(spec.From)))
	}
	b.WriteString("\nThis ask was routed to you by machinery, not cut as a card: it is yours because\n" + why + ". " + where + " by the deadline above; if it\ncannot be done by then, say so before it rather than after it.\n")

	sk := bus.Skeleton{From: spec.From, To: spec.Owner, Cc: cc, Subject: subject}
	return sk.RenderWith(b.String()), notices, nil
}

// ccWithSelf is the standing rule that a broadcast includes self: the sender is on
// the Cc line of every ask it sends, so its own bus holds what it asked for without
// anybody having to remember to add themselves. A sender already named on To or Cc
// is not named twice, and the caller's own --cc keeps its order.
func ccWithSelf(cc, self, to string) string {
	var out []string
	for _, part := range strings.Split(cc, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	named := func(who string) bool {
		if strings.EqualFold(strings.TrimSpace(to), who) {
			return true
		}
		for _, have := range out {
			if strings.EqualFold(have, who) {
				return true
			}
		}
		return false
	}
	if self = strings.TrimSpace(self); self != "" && !named(self) {
		out = append(out, self)
	}
	return strings.Join(out, ", ")
}

// ---------------------------------------------------------------- the send seam

// Sender is the ONE seam: what carries a rendered note to the bus, and what a test
// replaces. Everything above it is the shape of the ask; everything below it is
// nova-bus's own send path and nothing of this package's own.
type Sender interface {
	// Send hands one rendered note over and answers the bus's note id.
	Send(note string) (string, error)
	// Where is what the progress line says this sender sends to.
	Where() string
}

// BusSender runs `nova-bus send` -- the real send path, with its own locking, its
// own index append, its own push and its own refusals -- and reads the id off its
// SEND OK line. It is deliberately a subprocess and not a second copy of that path:
// two senders would be two shapes of note on one bus.
type BusSender struct {
	Bin      string // the nova-bus binary, from a flag: there is no discovery
	Bus      string
	As       string
	Remote   string
	Branch   string
	Attempts int
	Timeout  time.Duration
}

// Where names the bus this sender sends to.
func (s BusSender) Where() string { return s.Bus }

// Send writes the note to nova-bus's stdin and answers its id.
func (s BusSender) Send(note string) (string, error) {
	args := []string{"send", "--bus", s.Bus, "--stdin", "--as", s.As, "--remote", s.Remote, "--branch", s.Branch}
	if s.Attempts > 0 {
		args = append(args, "--attempts", fmt.Sprint(s.Attempts))
	}
	cmd := exec.Command(s.Bin, args...)
	cmd.Stdin = strings.NewReader(note)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { done <- cmd.Wait() }()
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("%s: %s", err, oneline.Cap(strings.TrimSpace(stderr.String()), oneline.TailBytes))
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return "", fmt.Errorf("nova-bus send did not finish inside %s", timeout)
	}
	return parseSendOK(stdout.String())
}

// parseSendOK reads the id off nova-bus's own SEND OK line. A run that printed no
// SEND OK is an error here even at exit 0: the caller is about to record an ask id,
// and a recorded id that names no note is worse than a refusal.
func parseSendOK(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "SEND OK ") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if id, ok := strings.CutPrefix(field, "id="); ok && id != "" {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("nova-bus printed no SEND OK line: %s", oneline.Cap(strings.TrimSpace(out), oneline.TailBytes))
}

// ---------------------------------------------------------------- the bus side

// askSubject is the one thing that makes a note an ask: its subject opens with it.
// The kind follows, and the colon ends it -- the same shape Render writes, read back.
const askSubject = "ask "

// OnBus is the open asks THE BUS ITSELF HOLDS, which is the source of truth for what
// was sent. A work set records an ask id, but a work set can be edited, lost or never
// written at all (a SPEC-WORKLANG file is never written back), and the note on the
// bus is the thing that actually went out.
//
// It reads only files: the sender's own lane directory for the asks, and the owner's
// for the replies that close them. An ask is OPEN unless a note in the owner's lane
// carries its id on a Re line. Nothing here fetches, pushes or reaches the network:
// what is on disk is what this run has.
//
// owner may be empty, which is every friend the sender has an ask out to.
//
// maxNotes bounds the FILES READ per lane and 0 -- the default -- is every one of
// them. It is NOT the row bound: `asks --max` says how many rows are printed, and
// passing it here read the OLDEST fifty files of a 1,734-note lane, found no open ask
// among them and printed `ASKS n=0`. A bound that hides open asks is worse than a
// long read, so the bound is separate, it takes the NEWEST notes, and every lane it
// actually bit is reported so the caller can say so in one line.
func OnBus(busDir, as, owner string, now time.Time, maxNotes int) ([]Row, []Bounded, error) {
	c, err := bus.LoadConfig(busDir)
	if err != nil {
		return nil, nil, err
	}
	me, ok := c.Lookup(as)
	if !ok {
		return nil, nil, fmt.Errorf("the roster at %s does not know %q; its names are %s", busDir, as, strings.Join(c.KnownNames(), ", "))
	}
	var them bus.Participant
	if owner != "" {
		if them, ok = c.Lookup(owner); !ok {
			return nil, nil, fmt.Errorf("the roster at %s does not know %q; its names are %s", busDir, owner, strings.Join(c.KnownNames(), ", "))
		}
	}
	var bounds []Bounded
	read := func(lane string) ([]bus.Note, error) {
		notes, bound, err := laneNotes(busDir, lane, maxNotes)
		if err != nil {
			return nil, err
		}
		if bound != nil {
			bounds = append(bounds, *bound)
		}
		return notes, nil
	}
	mine, err := read(me.Lane)
	if err != nil {
		return nil, nil, err
	}
	// The replies that close an ask: the owner's own lane, or every lane when no owner
	// was named. A Re naming the ask's id is the answer.
	answered := map[string]bool{}
	lanes := []string{them.Lane}
	if owner == "" {
		lanes = c.Lanes()
	}
	for _, lane := range lanes {
		if lane == "" || lane == me.Lane {
			continue
		}
		replies, err := read(lane)
		if err != nil {
			return nil, nil, err
		}
		for _, n := range replies {
			for _, re := range n.Header.Re {
				answered[bus.SlugOfID(re)+re] = true
				answered[re] = true
			}
		}
	}

	var rows []Row
	for _, n := range mine {
		kind, ok := askKind(n.Header.Subject)
		if !ok {
			continue
		}
		if owner != "" && !addresses(c, n.Header.To, them.Name) {
			continue
		}
		if answered[n.Header.ID] {
			continue
		}
		sent, err := time.Parse(time.UnixDate, strings.TrimSpace(n.Header.Date))
		if err != nil {
			// A note whose Date this reader cannot read still went out; it is listed with
			// no age rather than dropped, because dropping it hides an ask.
			sent = time.Time{}
		}
		due, _ := ParseStamp(bodyField(n.Body, "Deadline"))
		a := Ask{
			ID: n.Header.ID, Owner: firstName(c, n.Header.To), Kind: kind,
			Unit: bodyField(n.Body, "Unit"), Lane: bodyField(n.Body, "Lane"),
			Sent: sent.UTC(), Deadline: due, By: as, Bus: busDir,
		}
		row := Row{Ask: a, Source: "bus"}
		if !sent.IsZero() {
			row.Age = now.Sub(sent)
		}
		row.Overdue = !due.IsZero() && now.After(due)
		rows = append(rows, row)
	}
	SortOldestFirst(rows)
	return rows, bounds, nil
}

// Bounded is one lane whose notes were read only to a bound: which lane it was, how
// many note files it holds and how many of them this run read. A caller prints one
// line per bound, the way nova-bus's own since-walk names --max-commits, so a short
// count is never mistaken for a quiet bus.
//
// It says the bound BIT, and nothing else. A note that would not parse is skipped by
// every reader of this bus and is not a bound: reporting one made three lanes of the
// real bus claim they had been cut short when nobody had asked for a bound at all.
type Bounded struct {
	Lane  string
	Notes int
	Read  int
}

// laneNotes parses the note files in one lane directory and answers them with HOW MANY
// the lane holds, so a caller can tell a bounded read from a complete one. A file that
// will not parse is skipped rather than fatal: a bus with one bad note stays readable,
// which is the rule every other reader of this bus already keeps.
//
// max is the NEWEST max files, not the first max: a lane's names carry their stamp, so
// the directory's own order is oldest first and taking the head of it is taking the
// notes that have already been answered. 0 is every file.
func laneNotes(busDir, lane string, max int) ([]bus.Note, *Bounded, error) {
	if lane == "" {
		return nil, nil, nil
	}
	dir := filepath.Join(busDir, lane)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	// ReadDir sorts by name and a note's name opens with its stamp, so the tail is the
	// newest. A bounded read takes that tail, in the same order the whole lane is read in.
	var bound *Bounded
	if max > 0 && len(names) > max {
		bound = &Bounded{Lane: lane, Notes: len(names), Read: max}
		names = names[len(names)-max:]
	}
	var out []bus.Note
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, bound, err
		}
		n, err := bus.ParseNote(lane+"/"+name, string(raw))
		if err != nil {
			// A note this reader cannot parse is skipped, as every other reader of this bus
			// skips it. It is NOT a bound: the run read the whole lane.
			continue
		}
		out = append(out, n)
	}
	return out, bound, nil
}

// askKind reads the kind out of an ask's subject, and says no to anything that is not
// an ask. A subject naming a kind this package does not know is not an ask either:
// the shape is Render's own, read back exactly.
func askKind(subject string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(subject), askSubject)
	if !ok {
		return "", false
	}
	kind, _, ok := strings.Cut(rest, ":")
	if !ok {
		return "", false
	}
	kind = strings.TrimSpace(kind)
	if ValidKind(kind) != nil {
		return "", false
	}
	return kind, true
}

// bodyField reads one `Key: value` line out of an ask's body. It is the note's own
// shape read back, and a body that does not carry the key yields "" rather than a
// guess at where the value might otherwise be.
func bodyField(body, key string) string {
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+":"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// addresses reports whether a To line names one participant, resolved through the
// roster so an alias and a group both count.
func addresses(c *bus.Config, to, name string) bool {
	names, _ := c.ResolveList(to)
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// firstName is the first participant a To line names, which is an ask's owner.
func firstName(c *bus.Config, to string) string {
	names, _ := c.ResolveList(to)
	if len(names) > 0 {
		return names[0]
	}
	return strings.TrimSpace(to)
}

// SortOldestFirst is the one order both sources are printed in: what has been out
// longest is what a coordinator owes an answer on.
func SortOldestFirst(rows []Row) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].Sent.Before(rows[j-1].Sent); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}
