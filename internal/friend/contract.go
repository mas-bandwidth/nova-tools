package friend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/card"
	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
)

// How a brief's contract reaches its session (docs/SPEC-CARD-CONTRACT.md section 7), the
// contract= word of the daemon's record of each card it hands.
const (
	ContractNone      = "none"      // the brief names no contract: it carries its own frame
	ContractCheckout  = "checkout"  // the staged checkout holds it: the lane reads it there, once
	ContractPrepended = "prepended" // the daemon put the text in the brief in place of its line
	ContractUnread    = "unread"    // neither the checkout nor the daemon holds that version
)

// contract is the contract text of a version the daemon puts in a brief: d.Contract, and
// this build's copy of the versioned block (cardgen.HeldContract) when nothing set it.
func (d *Daemon) contract(version string) (string, bool) {
	if d.Contract != nil {
		return d.Contract(version)
	}
	return cardgen.HeldContract(version)
}

// HandBrief makes c's brief ready for its session and says how its contract reaches it,
// with the brief's token count as handed (card.Tokens). A brief by reference whose staged
// checkout (jobs/<job>/repo) holds the validated text of the version it names is left as
// it is: the lane reads the repository first. Otherwise the daemon puts the held text in
// place of the Contract: line, rewriting BRIEF.md whole; with no held text for that
// version it hands the brief as it is, unread.
func HandBrief(dir string, c Card, contract func(version string) (string, bool)) (how string, tokens int, err error) {
	raw, err := os.ReadFile(c.Brief)
	if err != nil {
		return "", 0, err
	}
	brief := string(raw)
	version, at := cardgen.ContractRef(brief)
	if at == 0 {
		return ContractNone, card.Tokens(brief), nil
	}
	text, held := "", false
	if contract != nil && version != "" {
		text, held = contract(version)
	}
	if !held {
		return ContractUnread, card.Tokens(brief), nil
	}
	doc, err := os.ReadFile(filepath.Join(JobDir(dir, filepath.Base(filepath.Dir(c.Brief))), "repo", filepath.FromSlash(cardgen.ContractPath)))
	if err == nil {
		if checkout, err := cardgen.ContractText(doc, version); err == nil && checkout == strings.TrimSpace(text) {
			return ContractCheckout, card.Tokens(brief), nil
		}
	} // else no checkout, no contract file in it, or no block of that version
	handed := cardgen.WithContract(brief, text)
	if err := atomicfile.WriteFile(c.Brief, []byte(handed), 0o644); err != nil {
		return "", 0, err
	}
	return ContractPrepended, card.Tokens(handed), nil
}

// hand is HandBrief on the card a lane or the batch session (by) is handed, said on the
// daemon's record with the brief's token count and how its contract reached the session.
func (d *Daemon) hand(c Card, by string, now time.Time) {
	at := now.UTC().Format(time.RFC3339)
	how, tokens, err := HandBrief(d.Dir, c, d.contract)
	if err != nil {
		d.Record(fmt.Sprintf("%s %s: card %s: its brief: %s", at, by, c.ID, err.Error()))
		return
	}
	d.Record(fmt.Sprintf("%s %s: card %s handed: brief_tokens=%d contract=%s", at, by, c.ID, tokens, how))
}

// dealtCard is the card a dealt line names, `inbox/<job>/BRIEF.md (card <id>, ...)` (the
// inbox's wrote line, startDealt), its brief and outbox under dir.
func dealtCard(dir, line string) (Card, bool) {
	p, rest, _ := strings.Cut(line, " ")
	job := jobOfWrote(line)
	id, _, _ := strings.Cut(strings.TrimPrefix(rest, "(card "), ",")
	if !validJob(job) || filepath.Base(p) != "BRIEF.md" || !strings.HasPrefix(rest, "(card ") || strings.TrimSpace(id) == "" {
		return Card{}, false
	}
	return Card{ID: id, Brief: filepath.Join(dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", job)}, true
}
