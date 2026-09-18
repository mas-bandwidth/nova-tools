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
	Needs      []string `json:"needs,omitempty"`
	Acceptance []string `json:"acceptance,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Asks       []Ask    `json:"asks,omitempty"`
}

// WorkSet is the units file, read as data.
type WorkSet struct {
	Units []Unit `json:"units"`
}

// Load reads one work set under a byte ceiling. A file past the ceiling is refused
// naming the ceiling and the size, before a byte is parsed.
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
	var ws WorkSet
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ws); err != nil {
		return nil, fmt.Errorf("%s is not a work set: %w", path, err)
	}
	return &ws, nil
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
			rows = append(rows, Row{Ask: a, Age: now.Sub(a.Sent), Overdue: !a.Deadline.IsZero() && now.After(a.Deadline)})
		}
	}
	// oldest first: what has been out longest is what a coordinator owes an answer on.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].Sent.Before(rows[j-1].Sent); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
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
// this tool writes and a note a person writes by hand are the same file.
//
// It refuses rather than repairs. A title with a newline in it would forge a header
// line under the Subject, and a unit with no acceptance is a unit its owner cannot
// tell they have finished -- both are refusals here, before anything is sent,
// because a note is not a draft somebody is still editing once it is on the bus.
func Render(spec AskSpec) (string, error) {
	if strings.TrimSpace(spec.From) == "" {
		return "", errors.New("an ask needs a sender; refusing to guess one")
	}
	if strings.TrimSpace(spec.Owner) == "" {
		return "", errors.New("an ask needs an owner; refusing to guess one")
	}
	if err := ValidKind(spec.Kind); err != nil {
		return "", err
	}
	if strings.TrimSpace(spec.Unit.ID) == "" {
		return "", errors.New("an ask needs a unit id")
	}
	title := strings.TrimSpace(spec.Unit.Title)
	if title == "" {
		return "", fmt.Errorf("unit %q has no title; a note whose subject is empty says nothing", spec.Unit.ID)
	}
	if len(spec.Unit.Acceptance) == 0 {
		return "", fmt.Errorf("unit %q has no acceptance; an ask nobody can tell they have finished is not an ask", spec.Unit.ID)
	}
	if spec.Deadline.IsZero() {
		return "", errors.New("an ask needs a deadline; every ask has a written one")
	}
	subject := "ask " + spec.Kind + ": " + title
	if err := bus.OneLine("--subject", subject); err != nil {
		return "", err
	}
	for _, v := range []string{spec.Owner, spec.From, spec.Cc, spec.Branch, spec.Unit.ID} {
		if err := bus.OneLine("a header value", v); err != nil {
			return "", err
		}
	}

	var b strings.Builder
	b.WriteString(title + "\n\n")
	b.WriteString("Unit: " + spec.Unit.ID + "\n")
	b.WriteString("Kind: " + spec.Kind + "\n")
	if len(spec.Unit.Needs) > 0 {
		b.WriteString("Needs:\n")
		for _, n := range spec.Unit.Needs {
			b.WriteString("- " + strings.TrimSpace(n) + "\n")
		}
	}
	b.WriteString("Acceptance:\n")
	for _, a := range spec.Unit.Acceptance {
		b.WriteString("- " + strings.TrimSpace(a) + "\n")
	}
	b.WriteString("Deadline: " + spec.Deadline.UTC().Format(time.RFC3339) + "\n")
	if spec.Branch != "" {
		b.WriteString("Reply on branch: " + spec.Branch + "\n")
	}
	b.WriteString("\nThis ask was routed to you by machinery, not cut as a card: it is yours because\nthe unit names you as its owner. Reply on the bus, on that branch, by the deadline\nabove; if it cannot be done by then, say so before it rather than after it.\n")

	sk := bus.Skeleton{From: spec.From, To: spec.Owner, Cc: spec.Cc, Subject: subject}
	return sk.RenderWith(b.String()), nil
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
