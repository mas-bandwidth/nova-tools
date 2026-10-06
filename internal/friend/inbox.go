package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
// is delivered as (inbox/<job>), its column (ready or working) and its BRIEF.md whole.
type HeldCard struct {
	Card  string `json:"card"`
	Job   string `json:"job"`
	Col   string `json:"col"`
	Brief string `json:"brief"`
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

// sprintBrief says a BRIEF.md is the sprint's: a work card's (its STATUS line) or a frontier
// read's (its WHO line). Only these are retired; a job the coordinator or Glenn put in her
// inbox by any other hand is never moved.
func sprintBrief(head string) bool {
	return strings.HasPrefix(head, "STATUS: nova-sprint card ") || strings.HasPrefix(head, "WHO: friend ")
}

// SyncInbox reconciles her inbox under dir with held, the cards on her row as the server
// answered an ask begun at asked: each held card's inbox/<job>/BRIEF.md is written when it
// is not there (written whole, never over a file there), and each sprint job in the inbox
// that no held card names, that no lane runs (keep), and whose BRIEF.md was written before
// asked, is moved to inbox/retired/. A brief written since the ask began is friend sync's for
// a card dealt after the server answered, and the next pass decides it (without this, the
// TLA+ model InboxReconcile retires a held card's brief: docs/SPEC-FRIEND.md). asked is the wall clock, as the
// file times are; zero retires none. record gets one line per write and per retirement, and
// one per job refused. The counts are what the inbox holds after the pass.
func SyncInbox(dir string, held []HeldCard, keep map[string]bool, asked, now time.Time, record func(string)) (InboxCounts, error) {
	at := now.UTC().Format(time.RFC3339)
	in := filepath.Join(dir, "inbox")
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
			record(fmt.Sprintf("%s inbox: refused card %s: the server sent no brief for inbox/%s; nothing was written", at, h.Card, h.Job))
			c.Missing++
			continue
		}
		if err := os.MkdirAll(job, 0o755); err != nil {
			c.Missing++
			firstErr = cmpErr(firstErr, err)
			continue
		}
		switch err := atomicfile.WriteFile(brief, []byte(h.Brief), 0o644, atomicfile.NoReplace()); {
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
		if !ok || !sprintBrief(head) {
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
// does not come writes nothing and retires nothing, and is said once until it changes.
func (l *loop) inboxStep(now time.Time) {
	d := l.d
	if d.Held == nil || (!d.inboxAt.IsZero() && now.Sub(d.inboxAt) < InboxEvery) {
		return
	}
	d.inboxAt = now
	asked := time.Now() // the file times' clock, never the loop's: a brief written after it is not retired this pass
	held, err := d.Held(l.ctx)
	if err != nil {
		if why := err.Error(); why != d.status.InboxError {
			d.Record(fmt.Sprintf("%s inbox: the server did not say which cards are on her row: %s; nothing written or retired until it does", now.UTC().Format(time.RFC3339), oneLine(why, 300)))
			d.status.InboxError = why
		}
		return
	}
	keep := map[string]bool{}
	for _, ln := range l.lanes.lanes {
		if ln.card != nil {
			keep[filepath.Base(filepath.Dir(ln.card.Brief))] = true
		}
	}
	record := d.Record
	if l.mode == ModeBatch {
		// a batch session is told of each brief written, in a turn of its own (startDealt)
		record = func(line string) {
			d.Record(line)
			if _, rest, ok := strings.Cut(line, " inbox: wrote "); ok {
				l.dealt = append(l.dealt, rest)
			}
		}
	}
	c, err := SyncInbox(d.Dir, held, keep, asked, now, record)
	d.status.InboxError = ""
	if err != nil {
		d.status.InboxError = err.Error()
	}
	d.status.HeldKnown, d.status.Held, d.status.InboxJobs, d.status.Missing = true, c.Held, c.Inbox, c.Missing
	ids := make([]string, 0, len(held))
	for _, h := range held {
		ids = append(ids, h.Card)
	}
	d.heldIDs, d.heldCards = ids, held // in the server's order
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
		if done || skip(c) || !exists(c.Brief) || exists(c.Result()) || exists(c.Report()) {
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

// HeldVia is the daemon's Held over a worker verb sent to the sprint server: friend cards
// <friend>, its answer read by ParseHeld.
func HeldVia(friend string, ask func(ctx context.Context, argv []string) (string, error)) func(context.Context) ([]HeldCard, error) {
	return func(ctx context.Context) ([]HeldCard, error) {
		out, err := ask(ctx, FriendCardsArgv(friend))
		if err != nil {
			return nil, err
		}
		return ParseHeld(friend, out)
	}
}
