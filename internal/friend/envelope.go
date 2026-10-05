package friend

import (
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// The daemon's own notices on the bus: of any two from the same friend, only
// the newest is worth a turn.
var noticeSubjects = []string{"coordinator silent", "coordinator back"}

// Envelope is every message in one turn's text, oldest first, each as
//
//	[i/n] <id> from=<f> at=<RFC3339> age=<m>m subject=<s>
//
// then its body. It stops at the first message that would take the text past
// limit bytes (at least one always goes in) and names the rest by id, with
// the command that reads them. carried is how many messages are in the text;
// they are acked together when the turn is accepted (docs/SPEC-FRIEND.md,
// "The loop"). It is a pure function of the list, the
// clock and the limit.
func Envelope(msgs []bus.Message, now time.Time, me string, limit int) (text string, carried int) {
	section := func(i, n int, m bus.Message) string {
		body := m.Body
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		age := max(now.Sub(m.At), 0)
		return fmt.Sprintf("\n[%d/%d] %s from=%s at=%s age=%dm subject=%s\n%s", i, n, m.ID, m.From, m.At.Format(time.RFC3339), int(age/time.Minute), m.Subject, body)
	}
	tail := func(rest []bus.Message) string {
		if len(rest) == 0 {
			return ""
		}
		ids := make([]string, len(rest))
		for i, m := range rest {
			ids[i] = m.ID
		}
		return fmt.Sprintf("\nand %d more (%s): nova-bus recv --as %s --all\n", len(rest), strings.Join(ids, ", "), me)
	}
	head := func(n int) string {
		return fmt.Sprintf("nova-friend: %d message(s) for you, oldest first, in one turn; take each in order.\n", n)
	}
	carried = len(msgs)
	for ; carried > 1; carried-- { // the longest prefix that fits; one at least
		size := len(head(carried))
		for i, m := range msgs[:carried] {
			size += len(section(i+1, carried, m))
		}
		if size+len(tail(msgs[carried:])) <= limit {
			break
		}
	}
	var b strings.Builder
	b.WriteString(head(carried))
	for i, m := range msgs[:carried] {
		b.WriteString(section(i+1, carried, m))
	}
	b.WriteString(tail(msgs[carried:]))
	return b.String(), carried
}

// Superseded is which of msgs are dropped, each with the id of the newer
// notice that replaces it: a message from self whose subject is one of the
// daemon's notices, when a newer one from self exists (docs/SPEC-FRIEND.md,
// "The loop"). msgs is oldest first.
func Superseded(msgs []bus.Message, self string) map[string]string {
	out := map[string]string{}
	newer := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.From != self || !isNotice(m.Subject) {
			continue
		}
		if newer != "" {
			out[m.ID] = newer
		} else {
			newer = m.ID
		}
	}
	return out
}

func isNotice(subject string) bool {
	for _, s := range noticeSubjects {
		if subject == s {
			return true
		}
	}
	return false
}
