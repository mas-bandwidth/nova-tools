package dealer

// The deal-time base pin (#2529): cut-to-deal lag was hours, so a card dealt
// after its branch moved still carried the base-sha cut stamped, and a card
// whose base is not on the target bench's mirror died at STEP 1's
// clone/checkout instead of being refused at deal.
//
// Deal is the production entry: it refuses a card whose base is missing or
// not on the bench mirror, and otherwise rewrites the card's `base-sha:` line
// to the landing tip at deal time. The line shape is the one the launchers
// read (docs/SPEC-CARD.md clause 8 reader 5): `base-sha: <40 hex>`, lowercase,
// at column 0, inside the first 40 lines. Re-pinning an already-pinned card
// writes nothing, so the pin is idempotent.
//
// A pin stamps `pinned-at-deal: <sha> <UTC instant>` on the line after
// base-sha (replacing an earlier stamp), so the card says when and to what it
// was pinned. A card carrying `pin: cut` at column 0 in the same window opts
// out: its cut base is kept (a bisect, a reproduction) and only checked
// against the mirror.

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// readerWindow is how many lines of the card the pin reads: the launchers
// `sed -n '1,40p'` the card, so a base-sha below line 40 is one no reader
// sees and is refused as missing rather than pinned where nobody looks.
const readerWindow = 40

var (
	baseSHALine = regexp.MustCompile(`^base-sha: (\S+)\s*$`)
	hex40       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	pinCutLine  = regexp.MustCompile(`^pin:\s*cut\s*$`)
	stampLine   = regexp.MustCompile(`^pinned-at-deal:`)

	// now is the deal clock the pinned-at-deal stamp reads; tests fix it.
	now = time.Now
)

// DealResult is what pinning one card at deal time did: the base the card
// carries now, and whether the card was rewritten to carry it.
type DealResult struct {
	Base   string
	Pinned bool
	// KeptCut is true when the card opted out with `pin: cut`: its cut
	// base was kept (and checked on the mirror), not re-pinned.
	KeptCut bool
}

// Deal pins one card at deal time: the card's base-sha becomes landingTip,
// the landing tip at deal time. A card with no base-sha line, and a card
// whose base is not on the target bench's mirror, is refused here, at deal,
// not at STEP 1.
func Deal(cardPath, mirrorDir, landingTip string) (DealResult, error) {
	if !hex40.MatchString(strings.TrimSpace(landingTip)) {
		return DealResult{}, fmt.Errorf("DEAL REFUSED card=%s: landing tip %s is not a 40-character hex sha (name the tip the branch lands on)",
			oneline.Field(cardPath), oneline.Field(landingTip))
	}
	base, lines, err := baseOf(cardPath)
	if err != nil {
		return DealResult{}, err
	}
	if err := BaseOnMirror(mirrorDir, base); err != nil {
		return DealResult{}, err
	}
	if optsOutWithPinCut(lines) {
		return DealResult{Base: base, KeptCut: true}, nil
	}
	if err := BaseOnMirror(mirrorDir, strings.TrimSpace(landingTip)); err != nil {
		return DealResult{}, err
	}
	pinned, err := pinBaseAtDeal(cardPath, strings.TrimSpace(landingTip))
	if err != nil {
		return DealResult{}, err
	}
	return DealResult{Base: strings.TrimSpace(landingTip), Pinned: pinned}, nil
}

// optsOutWithPinCut answers whether the card carries `pin: cut` at column 0
// inside the reader window.
func optsOutWithPinCut(lines []string) bool {
	for i, ln := range lines {
		if i >= readerWindow {
			break
		}
		if pinCutLine.MatchString(ln) {
			return true
		}
	}
	return false
}

// BaseOf reads the base-sha the card carries now, or an error refusing the
// card when no readable base-sha line is there to deal.
func BaseOf(cardPath string) (string, error) {
	base, _, err := baseOf(cardPath)
	return base, err
}

// BaseOnMirror refuses a base that is not on the target bench's mirror: the
// object must name a commit the mirror holds. A base the mirror never
// fetched is refused at deal, not at STEP 1's clone/checkout.
func BaseOnMirror(mirrorDir, sha string) error {
	sha = strings.TrimSpace(sha)
	if !hex40.MatchString(sha) {
		return fmt.Errorf("DEAL REFUSED base=%s: not a 40-character hex sha (a base the mirror could hold is one)",
			oneline.Field(sha))
	}
	if strings.TrimSpace(mirrorDir) == "" {
		return fmt.Errorf("DEAL REFUSED base=%s: no mirror named, refusing to guess (name the target bench's mirror directory)",
			oneline.Field(sha))
	}
	if err := exec.Command("git", "-C", mirrorDir, "cat-file", "-e", sha+"^{commit}").Run(); err != nil {
		return fmt.Errorf("DEAL REFUSED base=%s: not on the target bench's mirror %s (fetch the mirror before dealing onto it)",
			oneline.Field(sha), oneline.Field(mirrorDir))
	}
	return nil
}

// baseOf reads the card's base-sha line and the card's lines: the base, the
// lines, or a refusal when the line is missing, malformed or outside the
// window every launcher reads.
func baseOf(cardPath string) (string, []string, error) {
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		return "", nil, fmt.Errorf("DEAL REFUSED card=%s: %s (name a readable card file)",
			oneline.Field(cardPath), oneline.Err(err))
	}
	lines := strings.Split(string(raw), "\n")
	for i, ln := range lines {
		if i >= readerWindow {
			break
		}
		m := baseSHALine.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		if !hex40.MatchString(m[1]) {
			return "", nil, fmt.Errorf("DEAL REFUSED card=%s: base-sha %s on line %d is not a 40-character hex sha (cut wrote it wrong; recut the card)",
				oneline.Field(cardPath), oneline.Field(m[1]), i+1)
		}
		return m[1], lines, nil
	}
	return "", nil, fmt.Errorf("DEAL REFUSED card=%s: no `base-sha: <40 hex>` at column 0 in the first %d lines (cut writes it beside base-repo:; recut the card)",
		oneline.Field(cardPath), readerWindow)
}

// pinBaseAtDeal rewrites the card's base-sha line to the landing tip, stamps
// pinned-at-deal on the next line, and answers whether the card changed: an already-pinned card is left
// byte-identical, so dealing it twice pins it once.
func pinBaseAtDeal(cardPath, tip string) (bool, error) {
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		return false, fmt.Errorf("DEAL REFUSED card=%s: %s (name a readable card file)",
			oneline.Field(cardPath), oneline.Err(err))
	}
	lines := strings.Split(string(raw), "\n")
	for i, ln := range lines {
		if i >= readerWindow {
			break
		}
		m := baseSHALine.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		if m[1] == tip {
			return false, nil
		}
		lines[i] = "base-sha: " + tip
		stamp := "pinned-at-deal: " + tip + " " + now().UTC().Format(time.RFC3339)
		if i+1 < len(lines) && stampLine.MatchString(lines[i+1]) {
			lines[i+1] = stamp
		} else {
			lines = append(lines[:i+1], append([]string{stamp}, lines[i+1:]...)...)
		}
		info, err := os.Stat(cardPath)
		if err != nil {
			return false, fmt.Errorf("DEAL REFUSED card=%s: %s (name a readable card file)",
				oneline.Field(cardPath), oneline.Err(err))
		}
		if err := os.WriteFile(cardPath, []byte(strings.Join(lines, "\n")), info.Mode().Perm()); err != nil {
			return false, fmt.Errorf("DEAL REFUSED card=%s: %s (name a writable card file)",
				oneline.Field(cardPath), oneline.Err(err))
		}
		return true, nil
	}
	return false, fmt.Errorf("DEAL REFUSED card=%s: no `base-sha: <40 hex>` at column 0 in the first %d lines (cut writes it beside base-repo:; recut the card)",
		oneline.Field(cardPath), readerWindow)
}
