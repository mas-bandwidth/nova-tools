package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SlugMax is how long the human half of a filename may be. Long enough to read, short
// enough that a lane lists on one screen.
const SlugMax = 60

// Prepared is a note that has passed every check and is ready to be written.
type Prepared struct {
	Note   Note
	Sender Participant
	Path   string // repo-relative
	// Index is the line this note adds to its lane's catalogue, so that a later reader can
	// resolve the thread it starts, and a later send can refuse its id as taken, without
	// opening a single note.
	Index   IndexEntry
	Message string // the commit message
}

// Prepare validates a draft, assigns its id and date, and works out where it goes. It
// writes nothing: every refusal here happens before the bus is touched.
func Prepare(t *Bus, text string, now time.Time, slugOverride string) (Prepared, error) {
	var p Prepared
	c := t.Config
	n, err := ParseNote("", text)
	if err != nil {
		return p, err
	}
	if n.Header.ID != "" {
		return p, fmt.Errorf("this draft already carries an %s line (%q); send assigns the id, and a note is sent once", KeyID, n.Header.ID)
	}
	if n.Header.Date != "" {
		return p, fmt.Errorf("this draft already carries a %s line (%q); send pastes the date from the clock, and will not quietly replace yours -- remove the line", KeyDate, n.Header.Date)
	}
	if err := n.Header.Validate(c); err != nil {
		return p, err
	}
	sender, ok := c.ResolveOne(n.Header.From)
	if !ok {
		return p, fmt.Errorf("%s: %q names no one on this bus", KeyFrom, n.Header.From)
	}
	if sender.Lane == "" {
		return p, fmt.Errorf("%s: %q has no lane on this bus, so has nowhere to send from", KeyFrom, sender.Name)
	}
	if strings.TrimSpace(NormalizeBody(n.Body)) == "" {
		return p, errors.New("the note has no body")
	}
	for _, re := range n.Header.Re {
		if re == "new" {
			continue
		}
		if _, found := t.Resolve(re); !found {
			return p, fmt.Errorf("%s: %q is neither an id on this bus nor a note that exists; threads are named by id, and a slug is not a thread", KeyRe, re)
		}
	}
	n.Header.Date = now.UTC().Format(DateLayout)
	id, err := AssignID(c, sender, n.Header, n.Body, n.Header.Date)
	if err != nil {
		return p, err
	}
	if other, clash := t.NoteByID(id); clash {
		return p, fmt.Errorf("the id %q is already on this bus, on %s: the same sender, the same second, the same recipients, the same subject and the same body is the same note", id, other.Path)
	}
	n.Header.ID = id
	slug := Slugify(n.Header.Subject, SlugMax)
	if slugOverride != "" {
		if err := ValidSlug(slugOverride); err != nil {
			return p, err
		}
		slug = slugOverride
	}
	n.Lane = sender.Lane
	n.Path = sender.Lane + "/" + FileName(now, slug, id)
	if _, taken := t.NoteByPath(n.Path); taken {
		return p, fmt.Errorf("%s already exists", n.Path)
	}
	return Prepared{
		Note:    n,
		Sender:  sender,
		Path:    n.Path,
		Index:   IndexEntryFor(c, n),
		Message: sender.Slug() + ": " + n.Header.Subject,
	}, nil
}

// Paths is what a send commits: the note, and the line appended to the lane's catalogue.
// Both, in one commit, because a catalogue that can lag the notes by a commit is a
// catalogue a reader between the two would resolve wrongly.
func (p Prepared) Paths() []string { return []string{p.Path, IndexPath(p.Sender.Lane)} }

// AppendIndex adds this note to its lane's catalogue. It runs after Save, so a note that
// could not be written is never catalogued.
func (p Prepared) AppendIndex(root string) error { return AppendIndexLine(root, p.Index) }

// ValidSlug holds the one shape the human half of a filename may have, and is what
// --slug is checked against.
//
// It is not decoration. The slug is joined to the bus root and written into, so an
// override of "../../.ssh/authorized_keys" would put a note outside the bus entirely,
// and one of "a/b" would put it in a directory that is not a lane. The rule is the same
// charset as a lane's slug -- lower-case letters, digits and hyphens -- expressed as a
// ROUND TRIP through Slugify, so that the check and the generator cannot drift apart: an
// override is valid exactly when Slugify would have left it alone. A separator that
// Slugify would have dropped or folded is therefore a refusal rather than a quiet
// rewrite, because a caller who asked for one filename and got another has been lied to.
func ValidSlug(slug string) error {
	if slug == "" {
		return errors.New("--slug: empty")
	}
	if len(slug) > SlugMax {
		return fmt.Errorf("--slug %q: longer than %d characters", truncate(slug, SlugMax), SlugMax)
	}
	if clean := Slugify(slug, 0); clean != slug {
		return fmt.Errorf("--slug %q is not a slug: a slug is lower-case letters, digits and hyphens, and this one would have to be rewritten as %q", truncate(slug, SlugMax), truncate(clean, SlugMax))
	}
	return nil
}

// Save puts a prepared note on disk. It refuses to overwrite: a note once written is not
// rewritten, which is the bus's own rule and not this tool's invention.
//
// It also refuses to write OUTSIDE the bus root. Every path reaching here has been
// built from a validated lane and a validated slug, so this assertion should be
// unreachable -- which is exactly why it is here: the cost of it is one Rel call per note
// and the cost of being wrong about it is a tool that writes a file anywhere its input
// names.
//
// It is called Save rather than Write because it is not a writer, and the repo's escape
// audit reads a .Write call as bytes reaching a stream past the one-line escape.
func (p Prepared) Save(root string) error {
	full := filepath.Join(root, filepath.FromSlash(p.Path))
	if err := insideRoot(root, full); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(p.Note.Render()); err != nil {
		return err
	}
	return f.Close()
}

// insideRoot refuses a path that leaves the bus, whatever built it. filepath.Rel does
// the work: a relative path that begins with ".." is outside, and so is one Rel cannot
// compute at all (a different volume on Windows).
func insideRoot(root, full string) error {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return fmt.Errorf("%s is not inside the bus at %s", full, root)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%s is not inside the bus at %s", full, root)
	}
	return nil
}

// ReceiptPlan is what a receipt run will record.
type ReceiptPlan struct {
	Lane    string
	Path    string   // repo-relative path of the RECEIPTS file
	Record  []string // the canonical targets to append, in the order given
	Already []string // targets this lane has already recorded
	Stamp   string
}

// PlanReceipts resolves the targets a caller wants to mark heard.
//
// A receipt records the note's ID when it has one and its PATH when it does not, so a
// receipt for a legacy note is as good a receipt as any other. A note already answered by
// a reply is still receivable -- the two are different records and the answered rule
// accepts either -- but a target already in this lane's RECEIPTS is reported and not
// written twice.
func PlanReceipts(t *Bus, me Participant, targets []string, now time.Time) (ReceiptPlan, error) {
	plan := ReceiptPlan{
		Lane:  me.Lane,
		Path:  me.Lane + "/" + ReceiptsName,
		Stamp: now.UTC().Format(ReceiptStampLayout),
	}
	if me.Lane == "" {
		return plan, fmt.Errorf("%q has no lane on this bus, so has nowhere to record a receipt", me.Name)
	}
	if len(targets) == 0 {
		return plan, errors.New("no note named")
	}
	recorded := map[string]bool{}
	for _, r := range t.Receipts {
		if r.Lane == me.Lane {
			recorded[r.Target] = true
		}
	}
	seen := map[string]bool{}
	for _, target := range targets {
		n, ok := t.Resolve(target)
		if !ok {
			return plan, fmt.Errorf("%q is neither an id on this bus nor a note that exists", target)
		}
		if n.Lane == me.Lane {
			return plan, fmt.Errorf("%q is your own note; a receipt is for a note you heard from someone else", target)
		}
		canonical := n.Path
		if n.Header.ID != "" {
			canonical = n.Header.ID
		}
		if recorded[canonical] || recorded[n.Path] || (n.Header.ID != "" && recorded[n.Header.ID]) {
			plan.Already = append(plan.Already, canonical)
			continue
		}
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		plan.Record = append(plan.Record, canonical)
	}
	return plan, nil
}

// Append writes the planned receipt lines. The file is opened for append and created if
// absent: one file per lane, only ever added to, so two lines recording receipts in the
// same second touch different files and cannot conflict.
func (plan ReceiptPlan) Append(root string) error {
	if len(plan.Record) == 0 {
		return nil
	}
	full := filepath.Join(root, filepath.FromSlash(plan.Path))
	if err := insideRoot(root, full); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	var b strings.Builder
	for _, target := range plan.Record {
		b.WriteString(plan.Stamp + " " + target + "\n")
	}
	if _, err := f.WriteString(b.String()); err != nil {
		return err
	}
	return f.Close()
}

// Message is the commit message for a receipt run.
func (plan ReceiptPlan) Message(me Participant) string {
	return me.Slug() + ": receipt for " + strings.Join(plan.Record, ", ")
}
