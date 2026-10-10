package friend

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// The daemon writes every card she holds (the finding of 2026-10-05: from about 18:59 the
// friend daemons held cards on their rows, taken by the deal in batch mode or by the
// coordinator's friend take, and no inbox/<job>/BRIEF.md was written for them, so no lane
// ran them; the one writer was the coordinator's friend sync, and its loop had stopped).
// Each loop the daemon asks the sprint server which cards are on her row (Held: friend
// cards <friend>, each with its job and its brief as the server renders it) and reconciles
// her inbox both ways (SyncInbox): a held card with no inbox/<job>/BRIEF.md is written, and
// a sprint job in her inbox whose card left her row (dropped, returned, landed) is moved to
// inbox/retired/. Friend sync still writes the same file the same way; whichever comes
// first writes it, and the other finds it there (docs/SPEC-FRIEND.md, the inbox).

// HeldCard is one card on the friend's row as the server answers it: the card, the job it
// is delivered as (inbox/<job>), its column (ready or working) and its BRIEF.md whole, with
// its packet (its kind, work or read, the branch it is pushed to, its tier, attempt,
// generation and epoch, and the repository and base its checkout is staged from: PacketOf
// reads the brief's REPO: and BASE: lines when the server sends neither); Why is, for a card the answer sends no brief for, why not (the
// worker view's, ParseView).
type HeldCard struct {
	Card    string `json:"card"`
	Job     string `json:"job"`
	Col     string `json:"col"`
	Kind    string `json:"kind,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Tier    string `json:"tier,omitempty"`
	Stream  string `json:"stream,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	Gen     int    `json:"gen,omitempty"`
	Epoch   uint64 `json:"epoch"`
	Repo    string `json:"repo,omitempty"`
	Base    string `json:"base,omitempty"`
	Brief   string `json:"brief"`
	Why     string `json:"-"`
}

// Row is one answer of what is on her row: the cards, whether her reads are among them (a
// job no card names is retired only when the answer covers its kind: the worker view lists
// her work cards and none of her reads), where the answer came from, and Note, a line the
// daemon says with it ("" for none: the server's refusal of friend cards, once a ServedEvery).
type Row struct {
	Cards []HeldCard
	Reads bool
	From  string
	Note  string
}

// The sources of a Row.
const (
	FromCards = "friend cards"
	FromView  = "the worker view"
)

// ErrNotDue is a Held that did not ask this pass (the worker view is read once a ViewEvery):
// nothing is reconciled and nothing is said.
var ErrNotDue = errors.New("not due")

// Refused is the sprint server's refusal of a verb it answered (a non-zero exit), as against
// a server that did not answer: a refused friend cards is a server that does not serve it yet.
type Refused struct{ Why string }

func (r *Refused) Error() string { return r.Why }

// ServedBy is the card that adds friend cards to the sprint server: a daemon whose server
// refuses the verb names it, so whoever reads the line knows which server change is missing.
const ServedBy = "daemon-writes-every-taken-card3"

// NotServed is a sprint server that refused friend cards (Refused) where the daemon has no
// worker view to fall back on; it is said once a ServedEvery, each time the verb is asked again.
type NotServed struct{ Why string }

func (n *NotServed) Error() string {
	return "the sprint server does not serve friend cards (card " + ServedBy + " adds it), so no held card's brief is written: " + n.Why
}

// HeldAnswer is the server's answer to friend cards <friend>: every card on her row, the
// ready ones dealt behind her working ones and her reads among them.
type HeldAnswer struct {
	Friend string     `json:"friend"`
	Cards  []HeldCard `json:"cards"`
}

// FriendCardsArgv is the worker verb the daemon sends the sprint server for her held cards.
func FriendCardsArgv(friend string) []string { return []string{"friend", "cards", friend, "--json"} }

// ParseHeld reads the server's answer to FriendCardsArgv: its one JSON line, for this friend.
func ParseHeld(friend, out string) ([]HeldCard, error) {
	var a HeldAnswer
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &a); err != nil {
		return nil, fmt.Errorf("friend cards: the answer is not JSON: %v", err)
	}
	if a.Friend != friend {
		return nil, fmt.Errorf("friend cards: the answer is %q's, not %q's", a.Friend, friend)
	}
	return a.Cards, nil
}

// InboxEvery is how often the daemon reconciles her inbox with her row: every loop.
const InboxEvery = BeatEvery

// ViewEvery is how often the worker view is read while friend cards is refused: it runs on
// the server's line (serveView takes the tick's lock), so it is read about as often as friend
// sync's loop runs, never every loop. ServedEvery is how often friend cards is asked again.
const (
	ViewEvery   = 15 * time.Second
	ServedEvery = time.Minute
)

// RetiredDir is where a job whose card left her row goes, under her working directory.
const RetiredDir = "inbox/retired"

// InboxCounts is what one reconcile found: the cards held on her row, the sprint jobs in
// her inbox, and the held cards whose BRIEF.md is still not there after it.
type InboxCounts struct {
	Held, Inbox, Missing int
}

// jobRE is a job's name as it may stand in her inbox: a card id, its epoch and its gen
// (sprint.StoredID, friend sync's friendJobOf), one path element and never a dot file.
var jobRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.~-]*$`)

func validJob(job string) bool {
	return len(job) <= 200 && jobRE.MatchString(job) && !strings.Contains(job, "..") && job != "retired"
}

// sprintBrief says a BRIEF.md is the sprint's: a work card's (its STATUS line) or, when reads
// counts, a frontier read's (its WHO line). Only these are retired; a job the coordinator or
// the owner put in her inbox by any other hand is never moved.
func sprintBrief(head string, reads bool) bool {
	return strings.HasPrefix(head, "STATUS: nova-sprint card ") || (reads && strings.HasPrefix(head, "WHO: friend "))
}

// SyncInbox reconciles her inbox under dir with row, the cards on her row as the server
// answered an ask begun at asked: each held card's inbox/<job>/BRIEF.md is written when it
// is not there (written whole, never over a file there; a rework's with its fix first,
// ReworkedBrief), and each sprint job in the inbox
// that no held card names, that no lane runs (keep), whose kind the answer covers (row.Reads),
// and whose BRIEF.md was written before asked, is moved to inbox/retired/. A brief written since the ask began is friend sync's for
// a card dealt after the server answered, and the next pass decides it (without this, the
// TLA+ model InboxReconcile retires a held card's brief: docs/SPEC-FRIEND.md). asked is the wall clock, as the
// file times are; zero retires none. record gets one line per write and per retirement, and
// one per held card it could not write. The counts are what the inbox holds after the pass.
func SyncInbox(dir string, row Row, keep map[string]bool, asked, now time.Time, record func(string)) (InboxCounts, error) {
	at := now.UTC().Format(time.RFC3339)
	in := filepath.Join(dir, "inbox")
	held := row.Cards
	c := InboxCounts{Held: len(held)}
	want := map[string]bool{}
	var firstErr error
	for _, h := range held {
		if !validJob(h.Job) || filepath.Base(h.Job) != h.Job {
			record(fmt.Sprintf("%s inbox: refused card %s: its job %q is no inbox directory; nothing was written", at, h.Card, h.Job))
			c.Missing++
			continue
		}
		want[h.Job] = true
		job := filepath.Join(in, h.Job)
		if fi, err := os.Lstat(job); err == nil && !fi.IsDir() {
			record(fmt.Sprintf("%s inbox: refused card %s: inbox/%s is a symlink or a file, not a directory; nothing was written", at, h.Card, h.Job))
			c.Missing++
			continue
		}
		brief := filepath.Join(job, "BRIEF.md")
		if _, err := os.Lstat(brief); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			c.Missing++
			firstErr = cmpErr(firstErr, err)
			continue
		}
		if strings.TrimSpace(h.Brief) == "" {
			record(fmt.Sprintf("%s inbox: missing inbox/%s/BRIEF.md (card %s, %s on her row): %s; nothing was written", at, h.Job, h.Card, dash(h.Col), cmp.Or(h.Why, "the server sent no brief")))
			c.Missing++
			continue
		}
		if err := os.MkdirAll(job, 0o755); err != nil {
			c.Missing++
			firstErr = cmpErr(firstErr, err)
			continue
		}
		// a rework's brief is written with its fix first (rework.go, ReworkedBrief)
		switch err := atomicfile.WriteFile(brief, []byte(ReworkedBrief(h.Brief)), 0o644, atomicfile.NoReplace()); {
		case err == nil:
			record(fmt.Sprintf("%s inbox: wrote inbox/%s/BRIEF.md (card %s, %s on her row)", at, h.Job, h.Card, dash(h.Col)))
		case errors.Is(err, fs.ErrExist): // friend sync wrote it between the look and the write
		default:
			c.Missing++
			firstErr = cmpErr(firstErr, err)
		}
	}
	entries, err := os.ReadDir(in)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return c, cmpErr(firstErr, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || name == "retired" {
			continue
		}
		head, ok := briefHead(filepath.Join(in, name, "BRIEF.md"))
		if !ok || !sprintBrief(head, row.Reads) {
			continue
		}
		if want[name] || keep[name] || !writtenBefore(filepath.Join(in, name, "BRIEF.md"), asked) {
			c.Inbox++
			continue
		}
		to, err := retire(dir, name, now)
		if err != nil {
			c.Inbox++
			firstErr = cmpErr(firstErr, err)
			continue
		}
		record(fmt.Sprintf("%s inbox: retired inbox/%s to %s: its card is no longer on her row", at, name, to))
	}
	return c, firstErr
}

// jobOfWrote is the job a wrote line names: inbox/<job>/BRIEF.md (card ...).
func jobOfWrote(rest string) string {
	p, _, _ := strings.Cut(rest, " ")
	return path.Base(path.Dir(p))
}

// briefHead is the first line of a BRIEF.md, read no further than it needs.
func briefHead(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close() // ignored: a read-only file
	var buf [256]byte
	n, _ := f.Read(buf[:]) // ignored: a short or failed read is a head that names no sprint card
	head, _, _ := strings.Cut(string(buf[:n]), "\n")
	return head, true
}

// writtenBefore says the file at path was last written before t.
func writtenBefore(path string, t time.Time) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.ModTime().Before(t)
}

// retire moves inbox/<job> to inbox/retired/<job>, or <job>.<unix> when that is taken; it
// answers where it went, as a path under her working directory.
func retire(dir, job string, now time.Time) (string, error) {
	to := filepath.Join(dir, filepath.FromSlash(RetiredDir))
	if err := os.MkdirAll(to, 0o755); err != nil {
		return "", err
	}
	name := job
	if _, err := os.Lstat(filepath.Join(to, name)); err == nil {
		name = job + "." + strconv.FormatInt(now.Unix(), 10)
	}
	if err := os.Rename(filepath.Join(dir, "inbox", job), filepath.Join(to, name)); err != nil {
		return "", err
	}
	return RetiredDir + "/" + name, nil
}

func cmpErr(first, err error) error {
	if first != nil {
		return first
	}
	return err
}

// inboxStep is the daemon's reconcile, once an InboxEvery: Held asked, the inbox synced, the
// counts on the status file and the cards she holds given to the idle watch. An answer that
// does not come writes nothing and retires nothing, and is said once until it changes; a
// held card that could not be written is said once while it stays so (every loop would say
// it once a second), and a write or a retirement is always said.
func (l *loop) inboxStep(now time.Time) {
	d := l.d
	if d.Stage != nil {
		l.stageStep(nil, now) // every stage that ended is said each step, not once a reconcile
	}
	if d.Held == nil || (!d.inboxAt.IsZero() && now.Sub(d.inboxAt) < InboxEvery) {
		return
	}
	d.inboxAt = now
	asked := time.Now() // the file times' clock, never the loop's: a brief written after it is not retired this pass
	row, err := d.Held(l.ctx)
	if errors.Is(err, ErrNotDue) {
		return
	}
	var ns *NotServed
	if errors.As(err, &ns) {
		// asked again once a ServedEvery, so said once a minute, never once a loop
		d.Record(fmt.Sprintf("%s inbox: %s; nothing written or retired until it does", now.UTC().Format(time.RFC3339), oneLine(ns.Error(), 400)))
		d.status.InboxError = ns.Error()
		return
	}
	if err != nil {
		if why := err.Error(); why != d.status.InboxError {
			d.Record(fmt.Sprintf("%s inbox: the server did not say which cards are on her row: %s; nothing written or retired until it does", now.UTC().Format(time.RFC3339), oneLine(why, 300)))
			d.status.InboxError = why
		}
		return
	}
	if row.Note != "" {
		d.Record(fmt.Sprintf("%s inbox: %s", now.UTC().Format(time.RFC3339), oneLine(row.Note, 400)))
	}
	keep := map[string]bool{}
	for _, ln := range l.lanes.lanes {
		if ln.card != nil {
			keep[filepath.Base(filepath.Dir(ln.card.Brief))] = true
		}
	}
	said := map[string]bool{}
	byJob := map[string]HeldCard{}
	for _, h := range row.Cards {
		byJob[h.Job] = h
	}
	record := func(line string) {
		_, key, _ := strings.Cut(line, " ") // the line without its time
		if !strings.HasPrefix(key, "inbox: wrote ") && !strings.HasPrefix(key, "inbox: retired ") {
			said[key] = true
			if d.inboxSaid[key] {
				return
			}
		}
		d.Record(line)
		if _, rest, ok := strings.Cut(line, " inbox: wrote "); ok && l.mode == ModeBatch {
			// a batch session is told of each brief written, in a turn of its own (startDealt),
			// once its job is staged when it is one the daemon stages (stageStep)
			if h, ok := byJob[jobOfWrote(rest)]; ok && d.stageOwed(h) {
				d.stageDealt[h.Job] = rest
			} else {
				l.dealt = append(l.dealt, rest)
			}
		}
	}
	c, err := SyncInbox(d.Dir, row, keep, asked, now, record)
	d.inboxSaid = said
	d.status.InboxError = ""
	if err != nil {
		d.status.InboxError = err.Error()
	}
	d.status.HeldKnown, d.status.Held, d.status.InboxJobs, d.status.Missing = true, c.Held, c.Inbox, c.Missing
	d.status.HeldFrom = row.From
	if d.Stage != nil {
		l.stageStep(row.Cards, now)
	}
	l.pruneStep(row.Cards, keep, now)
	ids := make([]string, 0, len(row.Cards))
	for _, h := range row.Cards {
		ids = append(ids, h.Card)
	}
	d.heldIDs, d.heldCards = ids, row.Cards // in the server's order
	l.outboxStep(now)                       // every report in her outbox against the row just read
}

// nextCard is the next card a free lane is handed: while the server has said what is on
// her row, the first held card delivered and not done (its BRIEF.md there, no RESULT.md or
// REPORT.md in its outbox, not marked done in her queue file) and not skipped, in the
// server's order; before it has, the queue file's (NextCard).
func (d *Daemon) nextCard(skip func(Card) bool) (Card, bool, error) {
	if _, ok := d.heldFrom(); !ok {
		return NextCard(d.Dir, skip)
	}
	var q Queue
	path := filepath.Join(d.Dir, filepath.FromSlash(QueueFile))
	if _, err := read(path, &q); err != nil {
		_, _ = read(path, &q.Tasks) // ignored: a queue file that is no queue marks nothing done; her row is the truth
	}
	for _, h := range d.heldCards {
		if !validJob(h.Job) {
			continue
		}
		done := slices.ContainsFunc(q.Tasks, func(t Task) bool {
			return t.ID == h.Card && t.State == "done" && (t.Job == "" || t.Job == h.Job)
		})
		c := Card{ID: h.Card, Brief: filepath.Join(d.Dir, "inbox", h.Job, "BRIEF.md"), Outbox: filepath.Join(d.Dir, "outbox", h.Job)}
		if done || skip(c) || !exists(c.Brief) || exists(c.Result()) || exists(c.Report()) || d.stageOwed(h) {
			continue
		}
		return c, true, nil
	}
	return Card{}, false, nil
}

// startDealt is the batch session's turn naming the briefs the daemon wrote for her (the
// message friend sync sends when it writes one; wakeFriend in nova-sprint): a passive
// harness takes no turn, and its line is said on the record.
func (l *loop) startDealt(now time.Time) {
	d := l.d
	lines := l.dealt
	l.dealt = nil
	if l.passive {
		d.Record(fmt.Sprintf("%s not delivered: %s has no deliver command: %d card(s) dealt into her inbox", now.UTC().Format(time.RFC3339), d.Harness, len(lines)))
		return
	}
	for _, line := range lines {
		if c, ok := dealtCard(d.Dir, line); ok {
			d.hand(c, "batch", now) // a batch session reads its briefs straight from the inbox
		}
	}
	t := &turn{subjects: fmt.Sprintf("%q", fmt.Sprintf("%d card(s) dealt", len(lines)))}
	if d.m.Challenge != Quiet && d.PongCommand != nil {
		t.text = "Run this now, first, exactly as written: " + d.PongCommand(d.m.Nonce) + "\nThen read on.\n\n"
	}
	t.text += fmt.Sprintf("nova-friend: %d sprint card(s) dealt to you are in your inbox:\n", len(lines))
	for _, line := range lines {
		t.text += "- " + line + "\n"
	}
	t.text += "Read each BRIEF.md and start; its STATUS line says where to push and where to report.\n"
	l.busy = t
	l.startTurn(t, now, l.deliverBatch(t))
}

// heldFrom is the idle watch's cards when the server answered: what is on her row, never the
// queue file's tasks, which nothing prunes.
func (d *Daemon) heldFrom() ([]string, bool) {
	if d.Held == nil || !d.status.HeldKnown {
		return nil, false
	}
	return d.heldIDs, true
}

// HeldVia is the daemon's Held over the sprint server: friend cards <friend> (ask), its
// answer read by ParseHeld, which carries each card's brief. While the server refuses that
// verb (a server that does not serve it yet: Refused), the worker view (view, GET
// /api/view/worker?as=<friend>) says which work cards are on her row and the job each is
// delivered as, with no brief: her inbox is counted against it and a job whose work card
// left is retired, and a held card with no BRIEF.md is said missing and not written. The
// view is read once a ViewEvery (ErrNotDue between), and friend cards asked again once a
// ServedEvery, each refusal said in the Row's Note; with view nil a refusal is NotServed,
// once a ServedEvery (ErrNotDue between); an ask the server did not answer is its error.
func HeldVia(friend string, ask func(ctx context.Context, argv []string) (string, error), view func(ctx context.Context) (string, error), now func() time.Time) func(context.Context) (Row, error) {
	var refusedAt, viewAt time.Time
	return func(ctx context.Context) (Row, error) {
		at := now()
		note := ""
		if refusedAt.IsZero() || at.Sub(refusedAt) >= ServedEvery {
			out, err := ask(ctx, FriendCardsArgv(friend))
			var refused *Refused
			switch {
			case err == nil:
				refusedAt = time.Time{}
				cards, err := ParseHeld(friend, out)
				return Row{Cards: cards, Reads: true, From: FromCards}, err
			case !errors.As(err, &refused):
				return Row{}, err
			}
			refusedAt, viewAt = at, time.Time{}
			ns := &NotServed{Why: refused.Why}
			if view == nil {
				return Row{}, ns
			}
			note = ns.Error() + "; her row is read from the worker view, counted and retired"
		} else if view == nil {
			return Row{}, ErrNotDue
		}
		if !viewAt.IsZero() && at.Sub(viewAt) < ViewEvery {
			return Row{}, ErrNotDue
		}
		viewAt = at
		out, err := view(ctx)
		if err != nil {
			return Row{}, fmt.Errorf("friend cards is not served (card %s adds it), and the worker view did not answer: %w", ServedBy, err)
		}
		cards, err := ParseView(friend, out)
		return Row{Cards: cards, From: FromView, Note: note}, err
	}
}

// ViewWhy is why a card the worker view names has no brief written for it.
const ViewWhy = "the server does not serve friend cards, and the worker view names the card and its job with no brief"

// workerView is the part of the sprint server's worker view (nova-sprint view worker --as
// <friend> --json, schema 1) the daemon reads: her work cards, each with where its brief is
// delivered, ~/<friend>-working/inbox/<job>/BRIEF.md.
type workerView struct {
	View  string `json:"view"`
	As    string `json:"as"`
	Kind  string `json:"kind"`
	Cards []struct {
		ID    string `json:"id"`
		St    string `json:"st"`
		Brief string `json:"brief"`
	} `json:"cards"`
}

// ParseView reads the worker view's answer for this friend: each work card on her row, its job
// the inbox directory its brief path names, and no brief (Why says so).
func ParseView(friend, out string) ([]HeldCard, error) {
	var v workerView
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return nil, fmt.Errorf("the worker view is not JSON: %v", err)
	}
	if v.View != "worker" || v.As != friend || v.Kind != "friend" {
		return nil, fmt.Errorf("the worker view is %s %q's (%s), not friend %q's", cmp.Or(v.View, "-"), v.As, cmp.Or(v.Kind, "-"), friend)
	}
	cards := make([]HeldCard, 0, len(v.Cards))
	for _, c := range v.Cards {
		dir, file := path.Split(c.Brief)
		job := path.Base(dir)
		if file != "BRIEF.md" || path.Base(path.Dir(path.Clean(dir))) != "inbox" {
			return nil, fmt.Errorf("the worker view gives card %s a brief that is no inbox/<job>/BRIEF.md: %q", c.ID, c.Brief)
		}
		cards = append(cards, HeldCard{Card: c.ID, Job: job, Col: c.St, Why: ViewWhy})
	}
	return cards, nil
}
