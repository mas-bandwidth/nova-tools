package sprint

import (
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Packet is what a worker or a reader is handed with a card, so that no
// actor needs the coordinator's view to learn what it was asked: the card,
// its epoch and generation, the brief and this attempt's fix, the notes on
// it, and the branch to work on and the one to start from. A read's packet
// carries the work it reads: who did it, its head and branches, and the
// worker's report.
type Packet struct {
	Card    string `json:"card"`
	Kind    string `json:"kind"` // work or read
	As      string `json:"as"`   // the member or the reader it is handed to
	Primary string `json:"primary"`
	Stream  string `json:"stream"`
	Attempt int    `json:"attempt"`
	Gen     int    `json:"gen,omitempty"`
	Epoch   uint64 `json:"epoch"`
	Brief   string `json:"brief,omitempty"`
	Fix     string `json:"fix,omitempty"`
	// A rework's: the words of the readers that found the attempt before broken, and how
	// that attempt ended (steps_review.go reworkGiven); the member's frame writes both
	// into JOB.md, so the child learns why its attempt exists.
	Finding string   `json:"finding,omitempty"`
	Why     string   `json:"why,omitempty"`
	Notes   []string `json:"notes"`
	Branch  string   `json:"branch,omitempty"`
	Base    string   `json:"base,omitempty"`
	// BaseHead is the commit a later attempt starts from: the last head any earlier attempt
	// finished ok at (pushed by its member), never a branch name that may not have reached
	// origin, and BaseAttempt the attempt it is the head of (BaseOf; docs/SPEC-CARD-CONTRACT.md
	// layer 1); both empty when no earlier attempt pushed: the base is staged.
	BaseHead    string `json:"base_head,omitempty"`
	BaseAttempt int    `json:"base_attempt,omitempty"`
	// A work card's route (route.go): the route drawn ("pin" for a pinned card),
	// the model id the member launches with (provider/model), its token budget
	// (a count or unmetered) and its deadline in seconds; empty when the store
	// has no route, and the member runs its own.
	Route    string `json:"route,omitempty"`
	Model    string `json:"model,omitempty"`
	Tokens   string `json:"tokens,omitempty"`
	USD      string `json:"usd,omitempty"` // the dollar budget, a decimal; "" for none (#5094)
	Deadline int    `json:"deadline,omitempty"`
	// A read's: the work it reads.
	Worker     string `json:"worker,omitempty"`
	Head       string `json:"head,omitempty"`
	WorkBranch string `json:"work_branch,omitempty"`
	WorkBase   string `json:"work_base,omitempty"`
	Report     string `json:"report,omitempty"`
}

// BranchOf is the branch one launch of a work card's attempt is worked on: one per launch,
// named by the sprint (its prefix, the card), the launch's generation and its epoch, as the
// slot and job names are: sprint/<prefix><card>.g<gen>.e<epoch>. A card id comes back after a
// clear, and a branch named by the card alone holds the last epoch's push, so every push of the
// next would be refused non-fast-forward; and a card dealt again within one epoch (withdrawn
// from a member, or redealt after a staging or provider failure) is another generation, whose
// push would collide with the first launch's on a branch named by the epoch alone.
func BranchOf(prefix string, epoch uint64, workCard string, gen int) string {
	return "sprint/" + prefix + workCard + ".g" + strconv.Itoa(gen) + ".e" + strconv.FormatUint(epoch, 10)
}

// Base is where a later attempt of a card starts: the attempt whose pushed head it is, and
// the head; the zero Base is the card's own base.
type Base struct {
	Attempt int    `json:"attempt"`
	Head    string `json:"head"`
}

// BaseOf is the one place that decides where the attempt after starts (tla/CardContract.tla,
// RestagedAtLastPushedHead; docs/SPEC-CARD-CONTRACT.md layer 1): the head of the latest attempt
// of attempts that finished ok at a full sha, whatever happened to the attempts after it. The
// packet and `card` both read it, so the display is what the packet hands out.
func BaseOf(attempts []*Card) Base {
	var b Base
	for _, w := range attempts {
		if n := w.Int("attempt"); n > b.Attempt && PushedHead(w) != "" {
			b = Base{Attempt: n, Head: PushedHead(w)}
		}
	}
	return b
}

// PushedHead is the head an attempt's work card finished ok at, "" when it finished failed, in
// flight, or at no commit.
func PushedHead(w *Card) string {
	if w.F("ok") == "yes" && typedrec.IsFullSha(w.F("head")) {
		return w.F("head")
	}
	return ""
}

// NextLine is the line `card <id>` prints after its attempts: where the attempt after starts
// (BaseOf), the attempt whose pushed head it continues or the card's base.
func NextLine(attempts []*Card) string {
	if b := BaseOf(attempts); b.Attempt > 0 {
		return fmt.Sprintf("NEXT starts from attempt %d head=%s", b.Attempt, b.Head)
	}
	return "NEXT starts from the card's base: no attempt pushed a head"
}

// PacketOf is a work or read card's packet: the primary gives the brief (and a read's
// fix), a work card its own fix, finding and why; a work card of a later attempt starts from the
// attempt before's branch (as reported, else as named) and at BaseOf its earlier attempts; a read
// card reads its attempt's work card (work, when it is known).
func PacketOf(prefix string, epoch uint64, c, primary *Card, earlier []*Card, work *Card) Packet {
	p := Packet{Card: c.ID, Kind: c.F("kind"), As: c.Row, Primary: c.F("primary"), Stream: c.F("stream"), Attempt: c.Int("attempt"),
		Gen: c.Int("gen"), Epoch: epoch, Notes: []string{}}
	if primary != nil {
		p.Brief = primary.F("brief")
		if primary.Int("attempt") == p.Attempt {
			p.Fix = primary.F("fix")
		}
	}
	// a work card's route, or a read card's: the ask draws a read's as the deal
	// draws a work card's (route.go), so a reader needs no --model
	p.Route, p.Model, p.Tokens, p.USD, p.Deadline = c.F(FieldRoute), c.F(FieldModel), c.F(FieldTokens), c.F(FieldUSD), c.Int(FieldDeadline)
	if p.Kind == "work" {
		p.Branch = BranchOf(prefix, epoch, c.ID, c.Int("gen"))
		p.Finding, p.Why = c.F("finding"), c.F("why")
		// the attempt's own words, written with its card: the primary's are queued for the next
		// tick's drain, so a take before it sees the primary at the attempt before
		if fix := c.F("fix"); fix != "" {
			p.Fix = fix
		}
		if prevWork := latestOf(earlier); prevWork != nil {
			p.Base = prevWork.F("branch")
			if p.Base == "" {
				p.Base = BranchOf(prefix, epoch, prevWork.ID, prevWork.Int("gen"))
			}
		}
		b := BaseOf(earlier)
		p.BaseHead, p.BaseAttempt = b.Head, b.Attempt
		return p
	}
	if work != nil {
		p.Worker = work.F("member")
		p.Head = work.F("head")
		p.WorkBranch = work.F("branch")
		if p.WorkBranch == "" {
			p.WorkBranch = BranchOf(prefix, epoch, work.ID, work.Int("gen"))
		}
		p.WorkBase = work.F("base")
		p.Report = work.F("report")
	}
	if p.Head == "" {
		p.Head = c.F("head")
	}
	return p
}

// latestOf is the work card of the latest attempt of attempts, nil when none.
func latestOf(attempts []*Card) *Card {
	var last *Card
	for _, w := range attempts {
		if last == nil || w.Int("attempt") > last.Int("attempt") {
			last = w
		}
	}
	return last
}

// PacketCards is the ids a packet of the card reads besides the card: its
// primary, the work cards of every attempt before it (a work card), or the
// work card it reads (a read card).
func PacketCards(c *Card) (primary string, earlier []string, work string) {
	primary = c.F("primary")
	if c.F("kind") == "work" {
		for k := 1; k < c.Int("attempt"); k++ {
			earlier = append(earlier, WorkCardID(primary, k))
		}
		return primary, earlier, ""
	}
	return primary, nil, WorkCardID(primary, c.Int("attempt"))
}
