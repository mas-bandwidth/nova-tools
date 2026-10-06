package friend

import (
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// How a brief's contract reaches its lane (docs/SPEC-CARD-CONTRACT.md section 7), the
// contract= word of the daemon's record of the card it hands.
const (
	ContractNone      = "none"      // the brief names no contract: it carries its own frame
	ContractCheckout  = "checkout"  // the staged checkout holds it: the lane reads it there, once
	ContractPrepended = "prepended" // the daemon put the text in the brief in place of its line
	ContractUnread    = "unread"    // neither the checkout nor the daemon holds that version
)

// HandBrief makes c's brief ready for its lane and says how its contract reaches it,
// with the brief's token count as handed (card.Tokens): a brief by reference whose
// staged checkout (jobs/<job>/repo) holds the contract at the version it names is left
// as it is, for the lane reads the repository first; otherwise the daemon puts the text
// contract answers for that version in place of the Contract: line, rewriting BRIEF.md
// whole, and with no text in hand it hands the brief as it is, unread (section 7).
func HandBrief(dir string, c Card, contract func(version string) (string, bool)) (how string, tokens int, err error) {
	raw, err := os.ReadFile(c.Brief)
	if err != nil {
		return "", 0, err
	}
	brief := string(raw)
	_, version, ok := card.ContractRef(brief)
	if !ok {
		return ContractNone, card.Tokens(brief), nil
	}
	checkout := filepath.Join(JobDir(dir, filepath.Base(filepath.Dir(c.Brief))), "repo")
	if _, err := card.ReadContract(checkout, version); err == nil {
		return ContractCheckout, card.Tokens(brief), nil
	} // else no checkout, no contract file in it, or no block of that version
	text, held := "", false
	if contract != nil {
		text, held = contract(version)
	}
	if !held {
		return ContractUnread, card.Tokens(brief), nil
	}
	handed := card.WithContract(brief, text)
	if err := atomicfile.WriteFile(c.Brief, []byte(handed), 0o644); err != nil {
		return "", 0, err
	}
	return ContractPrepended, card.Tokens(handed), nil
}
