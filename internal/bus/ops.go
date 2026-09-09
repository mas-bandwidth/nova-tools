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

	// Notices is what the send-side tolerances did to the draft on the way here, one
	// sentence each, in the order they were done. A tolerance nobody is told about is a
	// tool quietly rewriting what a person wrote, so send prints every one of these.
	Notices []string
}

// Prepare validates a draft, assigns its id and date, and works out where it goes. It
// writes nothing: every refusal here happens before the bus is touched.
//
// It is PrepareDraft with nobody named by --as, which is what a caller with a draft that
// already carries its own From line has.
func Prepare(t *Bus, text string, now time.Time, slugOverride string) (Prepared, error) {
	return PrepareDraft(t, text, now, slugOverride, "")
}

// PrepareDraft is Prepare with the send-side tolerances and the caller's own name.
//
// TWO THINGS IT DOES THAT THE OLD ONE DID NOT, and both come from one new line's first
// send. It TOLERATES the four shapes a house style arrives in -- see tolerate in draft.go,
// where each is written out with the reason it is not a guess -- and it says on a notice
// what it did. And it COLLECTS: a draft with three mistakes in it is three lines of
// refusal from one run, not one line three times, because the person at the terminal is
// reading their own draft and not playing twenty questions with a tool.
//
// What it still refuses is what it cannot read without guessing: a recipient the roster
// does not know, a header with no To line at all, a key nobody knows, a Re naming nothing.
// Those are the same refusals they always were, with the rest of the run's findings beside
// them.
func PrepareDraft(t *Bus, text string, now time.Time, slugOverride, as string) (Prepared, error) {
	var p Prepared
	c := t.Config
	tol := tolerate(c, text, as)
	n, parseProblems := parseLines("", tol.lines, tol.at, 0)
	problems := append(tol.problems, parseProblems...)
	// A header that would not PARSE has no From, To or Subject to check, and a run that
	// went on to check them would report a missing To line to somebody whose To line is
	// there and misspelled. The line-level findings are all reported; the rest waits for a
	// header.
	if len(parseProblems) > 0 {
		return p, problemsOf(problems)
	}
	if n.Header.ID != "" {
		problems = append(problems, fmt.Errorf("this draft already carries an %s line (%q); send assigns the id, and a note is sent once", KeyID, n.Header.ID))
	}
	header := n.Header.Problems(c)
	for i, e := range header {
		// The one refusal send says more about than a reader does: there is a flag that
		// writes this line, and a person who does not know that writes it by hand forever.
		if errors.Is(e, ErrNoFrom) {
			header[i] = fmt.Errorf("%w: write one, or pass --as <name> and send writes it for you", e)
		}
	}
	problems = append(problems, header...)
	sender, senderKnown := c.ResolveOne(n.Header.From)
	if senderKnown && sender.Lane == "" {
		problems = append(problems, fmt.Errorf("%s: %q has no lane on this bus, so has nowhere to send from", KeyFrom, sender.Name))
	}
	if strings.TrimSpace(NormalizeBody(n.Body)) == "" {
		problems = append(problems, errors.New("the note has no body"))
	}
	// THE RE LINES, WHICH ARE HOW A NOTE CLOSES ANOTHER. A target that names an id or a
	// path on the bus is what a Re line has always been. A target that names NOTHING used
	// to be the end of it; it is now tried as the SUBJECT of a note on this sender's own
	// open list, because the id is the one thing a line answering by hand does not have in
	// front of it and the subject is the one thing it does. Every resolution says so on a
	// notice, and a subject naming nothing is the refusal it always was. See reply.go.
	reNotices, reProblems := resolveReSubjects(t, sender, &n.Header)
	problems = append(problems, reProblems...)
	tol.notices = append(tol.notices, reNotices...)
	// And, for a draft with no Re line at all that reads like a reply, one sentence saying
	// what it will not do. It is a note and never a refusal; see answersNothingNotice.
	if notice := answersNothingNotice(c, t, sender, n.Header); notice != "" {
		tol.notices = append(tol.notices, notice)
	}
	if slugOverride != "" {
		if err := ValidSlug(slugOverride); err != nil {
			problems = append(problems, err)
		}
	}
	if len(problems) > 0 {
		return p, problemsOf(problems)
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
		Notices: tol.notices,
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
