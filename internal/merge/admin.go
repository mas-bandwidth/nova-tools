package merge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Rule S: an --admin merge is the coordinator's hands reaching a protected base. It is
// allowed for exactly one thing -- landing a revert -- and never over an open HOLD,
// whether the hold stands on the pull request itself or on one its body carries
// ('carries #n'). #858 landed by --admin carrying #843/#848/#849 while Johnny's and
// Stella's HOLDs were open and its full Windows leg had never run; dev went red and #871
// reverted it. The leak was the coordinator's hands, so the refusal lives in the tool the
// hands use, and it is a REFUSAL here rather than a grant: there is no code path that
// lets an --admin merge past an open hold or onto a non-revert.

// The 'carries #n' convention, read as DATA. adminCarries takes the prose run after the
// word carries; adminHash reads the pull-request numbers out of it.
var (
	adminCarries = regexp.MustCompile(`(?i)\bcarries\b[^\n]*`)
	adminHash    = regexp.MustCompile(`#(\d+)`)
)

// CarriedPRs reads the pull requests a body says it carries: every '#n' on a line whose
// prose says "carries". It authorizes nothing; it only ever narrows what --admin may do.
func CarriedPRs(body string) []int {
	var out []int
	seen := map[int]bool{}
	for _, run := range adminCarries.FindAllString(body, -1) {
		for _, m := range adminHash.FindAllStringSubmatch(run, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// isRevert says whether a title or body names a revert. A revert pull request says so in
// its title ("Revert \"...\"") or in the body's "This reverts commit <sha>".
func isRevert(title, body string) bool {
	return strings.Contains(strings.ToLower(title), "revert") ||
		strings.Contains(strings.ToLower(body), "revert")
}

// adminRefusal is the ONE statement of rule S: the sentence an --admin merge is refused
// with, or "" when the admin merge is allowed.
//
// e is the entry being merged, pr is what the host said about it. A branch entry has no
// pull request at all and is refused; an entry that is held is already STATE HOLD and
// never reaches here, but its own hold is named anyway so the refusal says why.
func (p *Pass) adminRefusal(e *Entry, pr PR) string {
	if !e.IsPR() {
		return fmt.Sprintf("--admin names a pull request and entry=%s is a branch; there is no pull request to admin-merge", e.ID())
	}
	if !isRevert(pr.Subject, pr.Body) {
		return fmt.Sprintf("--admin is refused: pull request %d is not a revert (its title and body name no revert); an admin merge onto %s is allowed only for a revert", e.PR, p.State.Base)
	}
	if EvaluateReads(e, pr.Author).Held {
		return fmt.Sprintf("--admin is refused: an open HOLD stands on pull request %d's own head %s; the line that recorded the hold removes it by recording an approve for the same head", e.PR, Short(e.OID))
	}
	for _, n := range CarriedPRs(pr.Body) {
		c := p.State.Find(strconv.Itoa(n))
		if c == nil || c.OID == "" {
			continue
		}
		if EvaluateReads(c, "").Held {
			return fmt.Sprintf("--admin is refused: pull request %d carries #%d, which has an open HOLD on its head %s; an admin merge never lands over an open hold", e.PR, n, Short(c.OID))
		}
	}
	return ""
}
