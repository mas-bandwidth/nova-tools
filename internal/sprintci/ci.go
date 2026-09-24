// Package sprintci is a CI pass as a card (nova-tools#2842).
//
// A CI pass is one card per PR head. Its id is cicard:<repo>:<sha>, the Redis
// key for that head, and the sha is the full head rather than eight digits.
// It takes one slot inside the machine width. Half the slots stay for model
// cards: the CI share is width/2 (the odd slot, if there is one, stays with
// the model cards), and a pass that would hold more does not run. A pass
// that is not a card does not run either. A second sha is a second head,
// never this one. A rerun of the same head is the same card and does not
// take a second slot.
//
// The dealer deals that one slot to one bench. A bench that has no dealt slot
// for the card does not start the run. Cut writes ci-<pr>-<sha8>.md into the
// front tier: KIND script, zero model calls. The bench runs that script from
// its mirror at the sha, never a clone from GitHub. The script runs gofmt,
// go vet, and go test on the PATHS packages. The card's end writes Redis
// ci:<repo>:<head>:<gid> as OK, or FAIL plus the package and the test.
package sprintci

import (
	"fmt"
	"strings"
	"unicode"
)

// OutsideShares is why a CI pass did not run: the dealer's CI share is full,
// and the slots that remain stay for model cards.
const OutsideShares = "outside the dealer's shares"

// NoDealtSlot is why a bench did not start a CI run: the dealer has not dealt
// this card's slot to that bench. A free share is not a dealt slot.
const NoDealtSlot = "no dealt slot"

// Dealer is one machine's slot accounting. Width is the machine's slot count.
// CI cards and model cards draw from that one width; CI may not draw past its share.
// dealt is the bench that holds each head's one slot.
type Dealer struct {
	width int
	ci    map[string]string // head key -> card id
	model map[string]struct{}
	dealt map[string]string // head key -> bench
}

// Card is one CI pass: one repository, one pull request, one full head.
type Card struct {
	Repo string
	PR   int
	SHA  string
}

// New returns a dealer for a machine of the given width. A negative width is
// refused: a share nobody measured is not zero, and it is not a guess.
func New(machineWidth int) (*Dealer, error) {
	if machineWidth < 0 {
		return nil, fmt.Errorf("machine width is a count of slots, got %d", machineWidth)
	}
	return &Dealer{
		width: machineWidth,
		ci:    map[string]string{},
		model: map[string]struct{}{},
		dealt: map[string]string{},
	}, nil
}

// CIShare is how many CI cards this machine may hold at once. Half the slots
// stay for model cards, so the share is the floor of half the width.
func (d *Dealer) CIShare() int {
	if d == nil || d.width <= 0 {
		return 0
	}
	return d.width / 2
}

// ModelReserve is the slots CI may not take. It is at least half the width.
func (d *Dealer) ModelReserve() int {
	if d == nil || d.width <= 0 {
		return 0
	}
	return d.width - d.width/2
}

// CIHeld is how many distinct CI heads currently hold a slot.
func (d *Dealer) CIHeld() int {
	if d == nil {
		return 0
	}
	return len(d.ci)
}

// ModelHeld is how many model cards currently hold a slot.
func (d *Dealer) ModelHeld() int {
	if d == nil {
		return 0
	}
	return len(d.model)
}

// ID is cicard:<repo>:<sha>, the card identity for this head. The sha is the full
// 40-digit head. Eight hex digits are not a head, and the repository is part
// of the card: two repos, or two heads that share only those eight digits,
// are not one card. The pull request is not in the id. A card that is not
// one head has no id.
func (c Card) ID() (string, error) {
	sha, err := c.norm()
	if err != nil {
		return "", err
	}
	return "cicard:" + c.Repo + ":" + sha, nil
}

// fileName is the front-tier file ci-<pr>-<sha8>.md. It is not the card id.
// Two cards can share this short name and must not share an id.
func (c Card) fileName() (string, error) {
	sha, err := c.norm()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ci-%d-%s.md", c.PR, sha[:8]), nil
}

// RunCI runs one CI pass if and only if it is a card and the dealer has a
// slot for it inside the CI share and inside the machine width. Otherwise the
// pass does not run. The same head a second time is the same card: it is
// already running, and it does not take another slot.
//
// RunCI admits the card to the share. It does not deal a bench, and it does
// not start a bench: Start is what a bench may do, and only after Deal.
func (d *Dealer) RunCI(c Card) (ran bool, reason string) {
	_, _, reason = d.hold(c)
	if reason != "" {
		return false, reason
	}
	return true, ""
}

// Deal gives this card's one slot to one bench. The share rules are the same
// as RunCI. The same head dealt to the same bench again does not take a second
// slot. A second bench does not get the slot: one head, one bench.
func (d *Dealer) Deal(bench string, c Card) (bool, string) {
	if strings.TrimSpace(bench) == "" || strings.ContainsAny(bench, " \t\r\n") {
		return false, "a bench needs a name of one word"
	}
	_, key, reason := d.hold(c)
	if reason != "" {
		return false, reason
	}
	if prev, ok := d.dealt[key]; ok && prev != bench {
		return false, "the slot is dealt to " + prev
	}
	d.dealt[key] = bench
	return true, ""
}

// Start reports whether bench may start this card's run. The only yes is a
// dealt slot for this head on this bench. A share with room, a slot dealt to
// another bench, and a pass that is not a card are all nos, and none of them
// starts a run.
func (d *Dealer) Start(bench string, c Card) (bool, string) {
	if d == nil {
		return false, "no dealer"
	}
	if _, err := c.ID(); err != nil {
		return false, "not a ci card: " + err.Error()
	}
	d.init()
	got, ok := d.dealt[c.key()]
	if !ok || got != bench {
		return false, NoDealtSlot
	}
	return true, ""
}

// hold admits c to the CI share. An empty reason means the head holds a slot
// (it already did, or it does now). The caller deals a bench separately.
func (d *Dealer) hold(c Card) (id, key, reason string) {
	if d == nil {
		return "", "", "no dealer"
	}
	id, err := c.ID()
	if err != nil {
		return "", "", "not a ci card: " + err.Error()
	}
	d.init()
	key = c.key()
	if _, held := d.ci[key]; held {
		return id, key, ""
	}
	if len(d.ci)+1 > d.CIShare() {
		return "", "", OutsideShares
	}
	if len(d.ci)+len(d.model)+1 > d.width {
		return "", "", "the machine has no free slot"
	}
	d.ci[key] = id
	return id, key, ""
}

// TakeModel takes one slot for a model card. Model cards may use a CI slot
// the CI share is not holding; they may not use a slot the machine does not
// have. The same id twice does not take a second slot.
func (d *Dealer) TakeModel(id string) (bool, string) {
	if d == nil {
		return false, "no dealer"
	}
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, " \t\r\n") {
		return false, "a model card needs an id of one word"
	}
	d.init()
	if _, held := d.model[id]; held {
		return true, ""
	}
	if len(d.ci)+len(d.model)+1 > d.width {
		return false, "the machine has no free slot"
	}
	d.model[id] = struct{}{}
	return true, ""
}

func (d *Dealer) init() {
	if d.ci == nil {
		d.ci = map[string]string{}
	}
	if d.model == nil {
		d.model = map[string]struct{}{}
	}
	if d.dealt == nil {
		d.dealt = map[string]string{}
	}
}

func (c Card) key() string {
	sha, _ := c.norm()
	return c.Repo + "\n" + fmt.Sprintf("%d", c.PR) + "\n" + sha
}

func (c Card) norm() (string, error) {
	if c.Repo == "" || strings.TrimSpace(c.Repo) != c.Repo || strings.ContainsAny(c.Repo, " \t\r\n") {
		return "", fmt.Errorf("repo wants one word, like example/nova")
	}
	if c.PR < 1 {
		return "", fmt.Errorf("pr wants a pull request number, got %d", c.PR)
	}
	sha := strings.ToLower(strings.TrimSpace(c.SHA))
	if len(sha) != 40 || strings.IndexFunc(sha, func(r rune) bool {
		return !unicode.Is(unicode.ASCII_Hex_Digit, r)
	}) >= 0 {
		return "", fmt.Errorf("sha wants the 40-digit head, got %d characters", len(strings.TrimSpace(c.SHA)))
	}
	return sha, nil
}
