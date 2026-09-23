// Package sprintci is the dealer's slot accounting for a CI pass
// (nova-tools#2842).
//
// A CI pass is one card per PR head, id ci-<pr>-<sha8>. It takes one slot
// inside the machine width. Half the slots stay for model cards: the CI share
// is width/2 (the odd slot, if there is one, stays with the model cards), and
// a pass that would hold more does not run. That is the whole of "a CI pass
// runs inside the dealer's shares". A pass that is not a card does not run
// either. A second sha is a second head, never this one. A rerun of the same
// head is the same card and does not take a second slot.
package sprintci

import (
	"fmt"
	"strings"
	"unicode"
)

// OutsideShares is why a CI pass did not run: the dealer's CI share is full,
// and the slots that remain stay for model cards.
const OutsideShares = "outside the dealer's shares"

// Dealer is one machine's slot accounting. Width is the machine's slot count.
// CI cards and model cards draw from that one width; CI may not draw past its share.
type Dealer struct {
	width int
	ci    map[string]string // head key -> card id
	model map[string]struct{}
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

// ID is ci-<pr>-<sha8>. The sha is the full head; the id carries its first
// eight hex digits. A card that is not one head has no id.
func (c Card) ID() (string, error) {
	sha, err := c.norm()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ci-%d-%s", c.PR, sha[:8]), nil
}

// RunCI runs one CI pass if and only if it is a card and the dealer has a
// slot for it inside the CI share and inside the machine width. Otherwise the
// pass does not run. The same head a second time is the same card: it is
// already running, and it does not take another slot.
func (d *Dealer) RunCI(c Card) (ran bool, reason string) {
	if d == nil {
		return false, "no dealer"
	}
	id, err := c.ID()
	if err != nil {
		return false, "not a ci card: " + err.Error()
	}
	d.init()
	key := c.key()
	if _, held := d.ci[key]; held {
		return true, ""
	}
	if len(d.ci)+1 > d.CIShare() {
		return false, OutsideShares
	}
	if len(d.ci)+len(d.model)+1 > d.width {
		return false, "the machine has no free slot"
	}
	d.ci[key] = id
	return true, ""
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
