// Package rebase cuts the nova-sprint rebase card (#3094, spec 4.9).
//
// ns_pr_eval calls OnEval with a PR record it already computed. This package
// does not read GitHub and it does not call a model. A PR that would be
// landable but is CONFLICTING, or whose stack parent has merged, gets one
// card keyed rebase-<pr>-<sha8>. The card is a shell script. A clean rebase
// pushes the new head and the reads carry only when git range-diff is empty
// of differences. A conflict is not resolved; it becomes one fix task for
// the author under <pr>:<head>:conflict, and that key is not carded again.
package rebase

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// KindScript is the card kind. The work is the script, not a model.
	KindScript = "script"
)

var (
	headRx   = regexp.MustCompile(`^[0-9a-f]{8,64}$`)
	authorRx = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,38}$`)
	repoRx   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	refPart  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// Read is one review bound to a head. A carry copies it onto the new head
// with the same who, verdict and score; it does not invent a read.
type Read struct {
	Who     string
	Verdict string
	Head    string
	Score   int
}

// Record is the PR hash ns_pr_eval already holds. Mergeable is the host's
// word (CONFLICTING, MERGEABLE); DIRTY is the old cutter's word and is not
// a rebase here. WouldLand means every land gate except the conflict or the
// merged stack parent has passed.
type Record struct {
	Repo              string
	Number            int
	Head              string
	Base              string
	Branch            string
	Author            string
	Mergeable         string
	WouldLand         bool
	StackParentMerged bool
	Reads             []Read
}

// Card is one rebase card. ModelCalls is zero: the script rebases, and a
// conflict comes back as a fix task instead of a prompt.
type Card struct {
	Key        string
	Kind       string
	ModelCalls int
	Repo       string
	Number     int
	Head       string
	Base       string
	Branch     string
	Author     string
	Script     string
}

// FixTask is the one task a conflict owes the author. Key is
// <pr>:<head>:conflict, the head the card tried to rebase.
type FixTask struct {
	Key    string
	Author string
	Repo   string
	Number int
	Head   string
}

// Book is the in-memory record of cards, fix tasks and carried reads.
// ns_pr_eval owns persistence (#3091); this book is what that call updates
// in the same breath as the eval, and what the tests drive without Redis.
type Book struct {
	order []string
	cards map[string]Card
	fixes []string
	fix   map[string]FixTask
	reads map[string][]Read
	heads map[int]string
}

// NewBook is an empty record.
func NewBook() *Book {
	return &Book{
		cards: map[string]Card{},
		fix:   map[string]FixTask{},
		reads: map[string][]Read{},
		heads: map[int]string{},
	}
}

// CardKey is rebase-<pr>-<sha8>. The same head cannot be carded twice.
func CardKey(pr int, head string) string {
	short := head
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("rebase-%d-%s", pr, short)
}

// ConflictKey is <pr>:<head>:conflict. Head is the full sha the conflict
// was seen at, so a later head is a different task and this head is not.
func ConflictKey(pr int, head string) string {
	return fmt.Sprintf("%d:%s:conflict", pr, head)
}

// Eligible is the cut rule: the PR would be landable, and it is CONFLICTING
// or its stack parent has merged. A clean MERGEABLE PR with its parent still
// open is not a rebase.
func Eligible(rec Record) bool {
	if !rec.WouldLand || rec.Number <= 0 {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(rec.Mergeable), "CONFLICTING") {
		return true
	}
	return rec.StackParentMerged
}

// OnEval cuts one card, or returns the card already cut for this key.
// ok is true only when this call stored a new card. An ineligible PR
// returns ok false and a nil error. An eligible PR whose ref would not
// survive quoting into the script is an error and stores nothing.
func OnEval(b *Book, rec Record) (Card, bool, error) {
	if b == nil {
		return Card{}, false, fmt.Errorf("rebase: nil book")
	}
	if !Eligible(rec) {
		return Card{}, false, nil
	}
	if err := validate(rec); err != nil {
		return Card{}, false, err
	}
	key := CardKey(rec.Number, rec.Head)
	if card, ok := b.cards[key]; ok {
		return card, false, nil
	}
	card := Card{
		Key:        key,
		Kind:       KindScript,
		ModelCalls: 0,
		Repo:       rec.Repo,
		Number:     rec.Number,
		Head:       rec.Head,
		Base:       rec.Base,
		Branch:     rec.Branch,
		Author:     rec.Author,
		Script:     renderScript(rec, key),
	}
	b.cards[key] = card
	b.order = append(b.order, key)
	if _, ok := b.reads[readKey(rec.Number, rec.Head)]; !ok {
		b.reads[readKey(rec.Number, rec.Head)] = bindReads(rec.Reads, rec.Head)
	}
	return card, true, nil
}

// Cards is the cards in cut order.
func (b *Book) Cards() []Card {
	if b == nil {
		return nil
	}
	out := make([]Card, 0, len(b.order))
	for _, key := range b.order {
		out = append(out, b.cards[key])
	}
	return out
}

// Fixes is the fix tasks in the order they were first recorded.
func (b *Book) Fixes() []FixTask {
	if b == nil {
		return nil
	}
	out := make([]FixTask, 0, len(b.fixes))
	for _, key := range b.fixes {
		out = append(out, b.fix[key])
	}
	return out
}

// ReadsAt returns the reads bound to this PR at this head. A carry copies
// them onto the new head; the old head keeps its own binding.
func (b *Book) ReadsAt(pr int, head string) []Read {
	if b == nil {
		return nil
	}
	src := b.reads[readKey(pr, head)]
	if len(src) == 0 {
		return nil
	}
	out := make([]Read, len(src))
	copy(out, src)
	return out
}

// PushedHead is the head a clean rebase pushed for this PR.
func (b *Book) PushedHead(pr int) (string, bool) {
	if b == nil {
		return "", false
	}
	h, ok := b.heads[pr]
	return h, ok
}

func (b *Book) addFix(task FixTask) {
	if _, ok := b.fix[task.Key]; ok {
		return
	}
	b.fix[task.Key] = task
	b.fixes = append(b.fixes, task.Key)
}

func (b *Book) carry(pr int, oldHead, newHead string) {
	src := b.reads[readKey(pr, oldHead)]
	b.reads[readKey(pr, newHead)] = bindReads(src, newHead)
}

func readKey(pr int, head string) string {
	return strconv.Itoa(pr) + "\x00" + head
}

func bindReads(reads []Read, head string) []Read {
	if len(reads) == 0 {
		return nil
	}
	out := make([]Read, len(reads))
	copy(out, reads)
	for i := range out {
		out[i].Head = head
	}
	return out
}

func validate(rec Record) error {
	if rec.Number < 1 || rec.Number > 9_999_999 {
		return fmt.Errorf("rebase: pr %d is not a pull request number", rec.Number)
	}
	if !headRx.MatchString(rec.Head) {
		return fmt.Errorf("rebase: head %q is not a lowercase git sha", rec.Head)
	}
	if !validRef(rec.Base) || !validRef(rec.Branch) || rec.Base == rec.Branch {
		return fmt.Errorf("rebase: base %q or branch %q is not a ref this card can rebase", rec.Base, rec.Branch)
	}
	if !authorRx.MatchString(rec.Author) {
		return fmt.Errorf("rebase: author %q is not a login this card can name", rec.Author)
	}
	if !repoRx.MatchString(rec.Repo) {
		return fmt.Errorf("rebase: repo %q is not owner/name", rec.Repo)
	}
	return nil
}

// validRef is a branch name that can be quoted into the card script as one
// word: no metacharacters, no empty segment, no ".." traversal.
func validRef(s string) bool {
	if s == "" || strings.Contains(s, "..") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") {
		return false
	}
	parts := strings.Split(s, "/")
	if len(parts) > 8 {
		return false
	}
	for _, p := range parts {
		if !refPart.MatchString(p) || strings.HasSuffix(p, ".lock") {
			return false
		}
	}
	return true
}
