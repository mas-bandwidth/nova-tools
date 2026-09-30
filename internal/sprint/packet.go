package sprint

import "strconv"

// Packet is what a worker or a reader is handed with a card, so that no
// actor needs the coordinator's view to learn what it was asked: the card,
// its epoch and generation, the brief and this attempt's fix, the notes on
// it, and the branch to work on and the one to start from. A read's packet
// carries the work it reads: who did it, its head and branches, and the
// worker's report.
type Packet struct {
	Card    string   `json:"card"`
	Kind    string   `json:"kind"` // work or read
	As      string   `json:"as"`   // the member or the reader it is handed to
	Primary string   `json:"primary"`
	Stream  string   `json:"stream"`
	Attempt int      `json:"attempt"`
	Gen     int      `json:"gen,omitempty"`
	Epoch   uint64   `json:"epoch"`
	Brief   string   `json:"brief,omitempty"`
	Fix     string   `json:"fix,omitempty"`
	Notes   []string `json:"notes"`
	Branch  string   `json:"branch,omitempty"`
	Base    string   `json:"base,omitempty"`
	// A read's: the work it reads.
	Worker     string `json:"worker,omitempty"`
	Head       string `json:"head,omitempty"`
	WorkBranch string `json:"work_branch,omitempty"`
	WorkBase   string `json:"work_base,omitempty"`
	Report     string `json:"report,omitempty"`
}

// BranchOf is the branch a work card's attempt is worked on: one per attempt
// of one sprint, named by the sprint (its prefix, the card): sprint/<prefix><card>.
func BranchOf(prefix, workCard string) string { return "sprint/" + prefix + workCard }

// PacketOf is a work or read card's packet: the primary gives the brief and
// the fix; a work card of a later attempt starts from the attempt before's
// branch (as reported, else as named); a read card reads its attempt's work
// card (work, when it is known).
func PacketOf(prefix string, epoch uint64, c, primary, prevWork, work *Card) Packet {
	p := Packet{Card: c.ID, Kind: c.F("kind"), As: c.Row, Primary: c.F("primary"), Stream: c.F("stream"), Attempt: c.Int("attempt"),
		Gen: c.Int("gen"), Epoch: epoch, Notes: []string{}}
	if primary != nil {
		p.Brief = primary.F("brief")
		if primary.Int("attempt") == p.Attempt {
			p.Fix = primary.F("fix")
		}
	}
	if p.Kind == "work" {
		p.Branch = BranchOf(prefix, c.ID)
		if prevWork != nil {
			p.Base = prevWork.F("branch")
			if p.Base == "" {
				p.Base = BranchOf(prefix, prevWork.ID)
			}
		}
		return p
	}
	if work != nil {
		p.Worker = work.F("member")
		p.Head = work.F("head")
		p.WorkBranch = work.F("branch")
		if p.WorkBranch == "" {
			p.WorkBranch = BranchOf(prefix, work.ID)
		}
		p.WorkBase = work.F("base")
		p.Report = work.F("report")
	}
	if p.Head == "" {
		p.Head = c.F("head")
	}
	return p
}

// prevAttempt is the id of the work card of the attempt before.
func prevAttempt(primary string, attempt int) string {
	if attempt <= 1 {
		return ""
	}
	return primary + ".w" + strconv.Itoa(attempt-1)
}

// PacketCards is the ids a packet of the card reads besides the card: its
// primary, the work card before it (a work card), or the work card it reads
// (a read card).
func PacketCards(c *Card) (primary, prevWork, work string) {
	primary = c.F("primary")
	if c.F("kind") == "work" {
		return primary, prevAttempt(primary, c.Int("attempt")), ""
	}
	return primary, "", WorkCardID(primary, c.Int("attempt"))
}
