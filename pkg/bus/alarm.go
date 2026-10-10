package bus

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
)

// The classes of a send alarm: the store refused the login, or could not be
// reached (SPEC-BUS.md, fr-delivery-receipts.w1).
const (
	AlarmAuth       = "auth"
	AlarmConnection = "connection"
)

// Alarm is one outage of sends on a bus store: who logged in where, why it
// failed, since when and how many sends failed in it. It never holds a
// password: the reason is the store's refusal word, or the transport's
// error, which carries the address and never the secret.
type Alarm struct {
	Store, User string
	Class       string // AlarmAuth or AlarmConnection
	Reason      string
	Since       time.Time
	Failures    int
	Cleared     time.Time // set on the alarm Clear is handed
}

// Text is the alarm as the coordinator reads it, one line.
func (a Alarm) Text() string {
	user := a.User
	if user == "" {
		user = "(default)"
	}
	if !a.Cleared.IsZero() {
		return fmt.Sprintf("bus store %s answers user %s again: %d sends failed (%s) from %s to %s",
			a.Store, user, a.Failures, a.Class, a.Since.UTC().Format(time.RFC3339), a.Cleared.UTC().Format(time.RFC3339))
	}
	if a.Class == AlarmAuth {
		return fmt.Sprintf("bus store %s refuses the login of user %s: %s; every send on it fails until the user and its password agree with the store's ACL (since %s)",
			a.Store, user, a.Reason, a.Since.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("bus store %s does not answer user %s: %s; every send on it fails until it is reached (since %s)",
		a.Store, user, a.Reason, a.Since.UTC().Format(time.RFC3339))
}

// Watch turns the results of sends on one store into alarms: the first send
// that fails on the login or the connection raises one alarm, every later
// failure only counts in it, and the next send that succeeds clears it. A
// refusal of the message (Refusal) or any other answer of the store is the
// sender's to read, neither a failure nor a success here. Raise and Clear
// are called with the Watch's lock held and must not call back into it.
type Watch struct {
	Store, User  string
	Raise, Clear func(Alarm)

	mu   sync.Mutex
	open *Alarm
}

// Open says whether an alarm is raised and not yet cleared.
func (w *Watch) Open() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.open != nil
}

// Observe is one send's result at now: nil is a success.
func (w *Watch) Observe(now time.Time, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err == nil {
		if w.open != nil {
			a := *w.open
			a.Cleared, w.open = now, nil
			if w.Clear != nil {
				w.Clear(a)
			}
		}
		return
	}
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return
	}
	class := ""
	switch redisconn.Classify(err) {
	case redisconn.AuthRefused:
		class = AlarmAuth
	case redisconn.Unreachable:
		class = AlarmConnection
	default:
		return
	}
	if w.open != nil {
		w.open.Failures++
		return
	}
	w.open = &Alarm{Store: w.Store, User: w.User, Class: class, Reason: reason(class, err), Since: now, Failures: 1}
	if w.Raise != nil {
		w.Raise(*w.open)
	}
}

// reason is why a send failed, fit for an alarm: for a refused login the
// store's own refusal word (WRONGPASS, NOAUTH), never the rest of the text;
// for a store not reached the error on one line, at most 200 bytes.
func reason(class string, err error) string {
	text := err.Error()
	if class == AlarmAuth {
		for _, w := range []string{"WRONGPASS", "NOAUTH", "NOPERM"} {
			if strings.Contains(text, w) {
				return "login refused (" + w + ")"
			}
		}
		return "login refused"
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}
