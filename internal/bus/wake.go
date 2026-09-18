package bus

import (
	"regexp"
	"strings"
)

// Wake is what one line needs to start a turn: the ONE note it is being woken for, and the
// two counts that say what it is carrying. Nothing else -- no listing, no bodies, no
// catalogue.
//
// WHY A SELECTION AND NOT A LISTING. The bus was being read by minds: a wake loaded
// INBOX OPEN, 1,937 notes of it, and a model worked out from the whole backlog what it was
// for. The backlog is a fact about the bus and the turn is a fact about one note, and the
// two costs had been fused. This is the turn's half: one note, chosen by a rule written
// here rather than by a reader's judgment, and two integers for the rest.
type Wake struct {
	// Note is the newest note addressed to me on the To line that this lane has neither
	// answered nor receipted. It is nil when there is none, which is the quiet wake.
	Note *Note
	// From is the sender's roster name, resolved.
	From string
	// Deadline is what the body names, if it names one in a shape a machine can read, and
	// empty otherwise. See DeadlineIn.
	Deadline string

	// OpenTo and OpenCc count the open notes addressed to me on each line. They are the
	// backlog as two integers: a line that wants the listing runs inbox.
	OpenTo int
	OpenCc int
}

// SelectWake picks the one note and counts the rest.
//
// The rule, whole: of the notes on my open list -- addressed to me, in somebody else's
// lane, unanswered by any reply of mine -- drop the ones that are BARE ACKNOWLEDGEMENTS,
// because "heard, thank you" is not a turn's work and a wake that handed one out would be
// the mailbox again. Of what is left, the counts are by address, and the note is the newest
// one addressed on the To line that my RECEIPTS does not already record. Cc is counted and
// never woken on: a line copied in is being told, not asked.
//
// A note already receipted is skipped so that a pulse which receipts what it did is
// IDEMPOTENT: run it twice and the second run is quiet, rather than doing the same work
// again because nothing it wrote changed what it reads.
func SelectWake(t *Bus, me Participant) Wake {
	var w Wake
	for _, item := range t.Inbox(me, maxReceiptGuessWords) {
		if item.Receipt {
			continue
		}
		switch item.Address {
		case "to":
			w.OpenTo++
		case "cc":
			w.OpenCc++
		}
		if w.Note != nil || item.Address != "to" || item.Heard {
			continue
		}
		w.Note = item.Note
		w.From = item.From
		w.Deadline = DeadlineIn(item.Note.Body)
	}
	return w
}

// deadlinePattern is the one shape of deadline this tool will read out of prose: the word,
// then an optional colon, then a stamp. The stamp is an RFC 3339 instant, a date with a UTC
// time after it, a bare date, or a bare UTC time -- the four shapes the record actually
// holds, counted over 380 notes carrying the word.
//
// It is deliberately NARROW. "deadline holds", "deadline attached", "deadline and token
// budget" are all in the record too, and a pattern loose enough to answer those would be a
// tool inventing a time somebody then works to. What it cannot read it says nothing about,
// and the reader opens the note -- which they are doing anyway, because the note is what
// the wake hands them.
var deadlinePattern = regexp.MustCompile(`(?i)deadline:?\s+(` +
	`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?Z` + `|` +
	`\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}Z` + `|` +
	`\d{4}-\d{2}-\d{2}` + `|` +
	`\d{2}:\d{2}Z` + `)`)

// DeadlineIn returns the first deadline a body names in a shape a machine can read, or "".
// A date and a time separated by a space are joined by a "T" so that every deadline this
// prints is one field.
func DeadlineIn(body string) string {
	m := deadlinePattern.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.Join(strings.Fields(m[1]), "T")
}
