package pulse

// STOP IS A STATE WITH AN ADMISSION LIST, NOT A HALT. Pit stop 3, class C: an
// all-or-nothing STOP stopped the red's own fix card from launching, so it went out by hand
// three times -- wrong runner path, then a held slot, then it landed (issue #828, bug 6).
// While red, the loop launches only cards whose line 1 names the red (the gate writes that
// name into STOP's line 2), harvests everything, and enqueues nothing.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// AdmitShown is how many refusals a launch prints before it prints the count instead.
const AdmitShown = 5

// Admit says whether one card may launch under the STOP in hand, and why not when it may
// not. An empty STOP is no state to be in and admits everything. A STOP whose line 2 is an
// admission name admits the cards whose line 1 carries that name. A STOP with no line 2 --
// a person's hold, or a red the gate could not name -- admits nothing.
func Admit(stop []byte, cardLine1 string) (ok bool, reason string) {
	if len(strings.TrimSpace(string(stop))) == 0 {
		return true, ""
	}
	name := AdmissionName(stop)
	if name == "" {
		return false, "STOP names no admission name (the gate writes one on STOP line 2; lift the STOP, or name the red it holds for)"
	}
	if strings.Contains(cardLine1, name) {
		return true, ""
	}
	return false, "STOP admits only a card whose line 1 names " + name + " (cut the red's own fix card, or lift the STOP)"
}

// AdmissionName is STOP's line 2: the issue or the test the red belongs to, and the one
// name a card must carry to launch while it stands.
func AdmissionName(stop []byte) string {
	lines := strings.Split(string(stop), "\n")
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

// readStopFor finds the STOP a launch is under: <queue dir>/STOP when the caller named a
// queue directory, else the pulse root's, else the queue directory beneath it. No STOP is
// no bytes, and no bytes is every card admitted -- which is what every launch before this
// card did, so a launch with no STOP is unchanged.
func readStopFor(in LaunchInput) []byte {
	for _, dir := range []string{in.QueueDir, in.Root, filepath.Join(in.Root, "queue")} {
		if dir == "" {
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(dir, StopFile)); err == nil {
			return raw
		}
	}
	return nil
}

// admitCards filters the cards a launch may admit under the STOP in hand, printing one
// ADMIT REFUSED line per refusal, bounded at AdmitShown with one MORE line carrying the
// count. An admission refusal is not a failed run (rule 9): the caller fills the slots it
// can with what is left.
func admitCards(cards []CardRow, stop []byte, w io.Writer) ([]CardRow, int) {
	if len(strings.TrimSpace(string(stop))) == 0 {
		return cards, 0
	}
	list := bounded.Capped(w, AdmitShown, "ADMIT", "stop", "(see "+StopFile+" for the red this queue is under)")
	var admitted []CardRow
	refused := 0
	for _, c := range cards {
		ok, reason := Admit(stop, contractLineOf(c))
		if ok {
			admitted = append(admitted, c)
			continue
		}
		refused++
		list.Line("ADMIT REFUSED card=" + oneline.Field(c.Label) + " gate=stop " + reason)
	}
	list.More()
	return admitted, refused
}

// contractLineOf is the card's line 1 -- the RESULT contract line the admission name must
// appear in. A card whose file cannot be read is judged by its label, which is the only
// other thing about it that is written down.
func contractLineOf(c CardRow) string {
	raw, err := os.ReadFile(c.Card)
	if err != nil {
		return c.Label
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	if strings.TrimSpace(line) == "" {
		return c.Label
	}
	return line
}

// THE TWO GATES THE CARD ITSELF ANSWERS. Above this line is the STOP gate, which is about
// the bench. These two are about the CARD, and they run on every launch, red or green:
//
//	the cut stamp     a card nobody cut, or a cut card somebody edited, is refused (class P)
//	the recut index   a card text that has already been re-cut is refused (class I)
//
// The second is the one that ends the 307: a failed card is re-cut under a new number with
// its cause named, and the text it replaced can never be launched again -- not by a stale
// cards.tsv, not by a re-run of an old pulse, not by a hand.

// CardGate is one card's own admission: whether it may launch, and the one line saying why
// not. UnstampedOK is the migration week, and it admits an UNSIGNED card only -- never one
// whose stamp does not match its body, because that card was edited after it was cut.
func CardGate(card string, recuts []RecutRow, unstampedOK bool) (ok bool, refusal string, note string) {
	if r, yes := Superseded(recuts, card); yes {
		return false, "gate=recut this card's text was re-cut as " + oneline.Field(r.To) +
			" after a " + oneline.Field(r.Kind) + " failure (launch the re-cut card, not this one)", ""
	}
	chk := CheckStamp(card)
	if chk.Valid {
		return true, "", ""
	}
	if unstampedOK && !chk.Stamped {
		return true, "", "gate=stamp admitted unstamped under --unstamped-ok (migration week; cut it with nova-pulse cut --kind ...)"
	}
	return false, "gate=stamp " + chk.Reason, ""
}

// gateCards runs CardGate over a launch's cards, printing one ADMIT line per refusal and one
// per unstamped admission, bounded like every other list this tool prints.
func gateCards(cards []CardRow, recuts []RecutRow, unstampedOK bool, w io.Writer) ([]CardRow, int) {
	list := bounded.Capped(w, AdmitShown, "ADMIT", "card", "(cut the card again with nova-pulse cut --kind ...)")
	var admitted []CardRow
	refused := 0
	for _, c := range cards {
		raw, err := os.ReadFile(c.Card)
		if err != nil {
			refused++
			list.Line("ADMIT REFUSED card=" + oneline.Field(c.Label) + " gate=stamp the card file could not be read: " + oneline.Err(err) + " (name a readable card path in cards.tsv)")
			continue
		}
		ok, refusal, note := CardGate(string(raw), recuts, unstampedOK)
		if !ok {
			refused++
			list.Line("ADMIT REFUSED card=" + oneline.Field(c.Label) + " " + refusal)
			continue
		}
		if note != "" {
			fmt.Fprintf(w, "ADMIT UNSTAMPED card=%s %s\n", oneline.Field(c.Label), note)
		}
		admitted = append(admitted, c)
	}
	list.More()
	return admitted, refused
}

// queueDirOf is the queue directory a launch's own state lives in: the one the caller named,
// else the pulse root, else the queue directory beneath it -- the same three readStopFor
// walks, so STOP and RECUT.tsv are never read out of two different queues.
func queueDirOf(in LaunchInput) string {
	for _, dir := range []string{in.QueueDir, in.Root, filepath.Join(in.Root, "queue")} {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, recutFile)); err == nil {
			return dir
		}
	}
	if in.QueueDir != "" {
		return in.QueueDir
	}
	return in.Root
}
