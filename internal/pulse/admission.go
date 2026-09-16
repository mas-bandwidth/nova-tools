package pulse

// STOP IS A STATE WITH AN ADMISSION LIST, NOT A HALT. Pit stop 3, class C: an
// all-or-nothing STOP stopped the red's own fix card from launching, so it went out by hand
// three times -- wrong runner path, then a held slot, then it landed (issue #828, bug 6).
// While red, the loop launches only cards whose line 1 names the red (the gate writes that
// name into STOP's line 2), harvests everything, and enqueues nothing.

import (
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
