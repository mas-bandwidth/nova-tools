package friend

import (
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// RestCommand is the command a turn's text names for the messages that did not fit
// in it: every pending message, printed whole (docs/SPEC-FRIEND.md, the loop).
const RestCommand = "nova-bus recv --as %s --all"

// NoticeSubjects are the subjects of the daemon's own notices about the coordinator;
// a notice of which a newer one exists is stale (SupersededNotices).
var NoticeSubjects = []string{"coordinator silent", "coordinator back"}

// Envelope is the one turn that carries every pending message (docs/SPEC-FRIEND.md,
// the loop; tla/Friend.tla, Deliver): the pong line to run first while a challenge is
// open, the daemon's word about the coordinator, a header, then each message oldest
// first as `[i/n] <id> from=<f> at=<RFC3339> age=<m>m subject=<s>` and its body. The
// text is at most limit bytes when limit is above zero, at least one message long;
// the messages that do not fit are named by id after the last, with the command that
// prints them (RestCommand for me). It answers the text and how many messages it
// carries, a prefix of msgs: those, and only those, are acked when the turn is
// accepted. A single message with nothing else is its Text alone. A function of its
// arguments: the clock is now.
func Envelope(msgs []bus.Message, now time.Time, me string, limit int, notice, pongCommand string) (text string, shown int) {
	if len(msgs) == 1 && notice == "" && pongCommand == "" {
		return Text(msgs[0]), 1
	}
	var head strings.Builder
	if pongCommand != "" {
		head.WriteString("Run this now, first, exactly as written: " + pongCommand + "\nThen read on.\n\n")
	}
	if notice != "" {
		head.WriteString("nova-friend: " + notice + "\n\n")
	}
	fmt.Fprintf(&head, "nova-friend: %d message(s) for you, oldest first, in one turn; take each in order.\n", len(msgs))
	text = head.String()
	for i, m := range msgs {
		body := m.Body
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		part := fmt.Sprintf("\n[%d/%d] %s from=%s at=%s age=%dm subject=%s\n%s", i+1, len(msgs), m.ID, m.From, m.At.Format(time.RFC3339), ageMinutes(now, m.At), m.Subject, body)
		if limit > 0 && shown > 0 && len(text)+len(part) > limit {
			break
		}
		text += part
		shown++
	}
	if rest := msgs[shown:]; len(rest) > 0 {
		ids := make([]string, len(rest))
		for i, m := range rest {
			ids[i] = m.ID
		}
		text += fmt.Sprintf("\nand %d more (%s): %s\n", len(rest), strings.Join(ids, ", "), fmt.Sprintf(RestCommand, me))
	}
	return text, shown
}

// ageMinutes is how long before now at was, in whole minutes, never below zero.
func ageMinutes(now, at time.Time) int {
	if d := now.Sub(at); d > 0 {
		return int(d / time.Minute)
	}
	return 0
}

// SupersededNotices maps each of the daemon's own notices (from self, a subject of
// NoticeSubjects) of which a newer one exists to the id of the newest: the dropped
// ones are acked with superseded=<that id>, never delivered (docs/SPEC-FRIEND.md,
// the loop). msgs is oldest first; every other message is untouched.
func SupersededNotices(msgs []bus.Message, self string) map[string]string {
	newest := ""
	for _, m := range msgs {
		if isNotice(m, self) {
			newest = m.ID
		}
	}
	out := map[string]string{}
	for _, m := range msgs {
		if isNotice(m, self) && m.ID != newest {
			out[m.ID] = newest
		}
	}
	return out
}

func isNotice(m bus.Message, self string) bool {
	if m.From != self {
		return false
	}
	for _, s := range NoticeSubjects {
		if m.Subject == s {
			return true
		}
	}
	return false
}
