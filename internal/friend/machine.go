// Package friend is what a friend's machinery runs to be part of the team
// (docs/SPEC-FRIEND.md; the model is tla/Friend.tla). One daemon per friend
// parks on the friend's nova-bus stream and pushes each message into the
// running session as a turn, beats to the sprint server while it does, and
// keeps the connection with the coordinator symmetric: a ping every window
// from the coordinator, answered at once by the daemon and as a turn by the
// session; no ping for a window and the session is told the coordinator is
// silent. The rules live here, apart from the transport: Machine is the
// state the daemon owns, stepped by the events and the clock the daemon
// hands it, so every rule is tested with no socket and no real time.
package friend

import (
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// Window is how long either side waits before it decides the other is gone:
// the coordinator for a session pong, the daemon for a ping. One number on
// both sides (the contract of 2026-10-04: "3 minutes to start").
const Window = 3 * time.Minute

// The connection: what the daemon knows of the coordinator.
const (
	Connected = "connected" // a ping arrived within the window
	Silent    = "silent"    // no ping for a window; the session was told once
)

// The challenge: what the daemon knows of its own session.
const (
	Quiet      = "quiet"      // nothing asked, or the last ping was answered
	Challenged = "challenged" // a ping was pushed in; no pong yet
	Deaf       = "deaf"       // challenged for a window with no pong
)

// The idle watch: what the daemon knows of a session holding cards
// (docs/SPEC-FRIEND.md, idle wake).
const (
	Awake = "awake" // a write within IdleAfter, or no card held
	Woken = "woken" // idle for IdleAfter holding cards; one wake turn given
	Noted = "noted" // idle IdleAfter more after the wake; the coordinator told once
)

// DefaultIdleAfter is how long a session holding cards may write nothing before
// its daemon wakes it, and again before the coordinator is told (the friend row's
// idle setting, ten minutes when the row says none).
const DefaultIdleAfter = 10 * time.Minute

// Push is text the daemon owes the session as a turn: the ping (with the
// pong line to run), or a word about the coordinator.
type Push struct {
	Subject string
	Text    string
}

// RestLine is the envelope's line for the messages that did not fit in it:
// how many, and the command that prints every pending message whole
// (docs/SPEC-FRIEND.md, the loop).
const RestLine = "and %d more: nova-bus recv --as %s --all"

// ageMinutes is how long before now at was, in whole minutes, never below
// zero (at is the store's clock, now the daemon's). A function of its
// arguments.
func ageMinutes(now, at time.Time) int {
	if d := now.Sub(at); d > 0 {
		return int(d / time.Minute)
	}
	return 0
}

// Envelope is the one turn that carries every pending message when the
// session is free (docs/SPEC-FRIEND.md, the loop): the pong line first while
// a challenge is open, the daemon's word about the coordinator, a count,
// then each message oldest first as `[i/n] <id> from=<f> at=<RFC3339>
// age=<m>m subject=<s>` and its text, the age taken at now. Each message's
// text is authored: plain from the seat holder, quoted from anyone else
// (bus-authority-labels.w3). With limit above zero the text stops before the
// message that would pass it (the first always goes in), and the rest are
// named by their lines under RestLine for me. It answers the text and how
// many messages it carries, a prefix of msgs: exactly those are acked when
// the turn is accepted. A single message with nothing else is its authored
// text alone. A function of its arguments: the pending list, the clock and
// the limit.
func Envelope(seat string, msgs []bus.Message, now time.Time, me string, limit int, notice, pongCommand string) (text string, shown int) {
	if len(msgs) == 1 && notice == "" && pongCommand == "" {
		return authored(seat, msgs[0]), 1
	}
	var b strings.Builder
	if pongCommand != "" {
		b.WriteString("Run this now, first, exactly as written: " + pongCommand + "\nThen read on.\n\n")
	}
	if notice != "" {
		b.WriteString("nova-friend: " + notice + "\n\n")
	}
	fmt.Fprintf(&b, "nova-friend: %d message(s) for you, oldest first, in one turn; take each in order.\n", len(msgs))
	line := func(i int) string {
		m := msgs[i]
		return fmt.Sprintf("[%d/%d] %s from=%s at=%s age=%dm subject=%s\n", i+1, len(msgs), m.ID, m.From, m.At.Format(time.RFC3339), ageMinutes(now, m.At), oneLine(m.Subject, len(m.Subject)))
	}
	for i, m := range msgs {
		part := "\n" + line(i) + authored(seat, m)
		if limit > 0 && shown > 0 && b.Len()+len(part) > limit {
			break
		}
		b.WriteString(part)
		shown++
	}
	if shown < len(msgs) {
		fmt.Fprintf(&b, "\n"+RestLine+"\n", len(msgs)-shown, me)
		for i := shown; i < len(msgs); i++ {
			b.WriteString(line(i))
		}
	}
	return b.String(), shown
}

// Notice is one of the daemon's own words about the coordinator (a Push of
// Machine's), with the id the daemon gives it when it is said: the record
// names a dropped notice and its successor by these ids.
type Notice struct {
	Push
	ID  string
	seq int // the order the daemon said it in
}

// SupersededNotices is the supersede rule (docs/SPEC-FRIEND.md, the loop): of
// the daemon's own notices not yet in a turn, oldest first, each of which a
// newer one exists maps to the newest's id. Those are dropped, never
// delivered, and each is recorded with superseded=<that id>. It reads only
// the notices the daemon itself raised, never a message on the bus. A
// function of its argument.
func SupersededNotices(owed []Notice) map[string]string {
	out := map[string]string{}
	if len(owed) < 2 {
		return out
	}
	newest := owed[len(owed)-1].ID
	for _, n := range owed[:len(owed)-1] {
		out[n.ID] = newest
	}
	return out
}

// Machine is the daemon's state. The daemon steps it with the clock it
// reads (Tick), the pings that arrive (Ping) and the pongs the session
// records (Pong); each step answers the pushes the session is owed, in
// order. The daemon owns the transport around it.
type Machine struct {
	Window time.Duration // Window unless a test shortens it

	Connection string    // Connected or Silent
	LastPing   time.Time // when the last ping arrived; the start, before any
	Seat       string    // the coordinator the last ping named
	SeatSince  time.Time // since when, as the ping said
	SilentFrom time.Time // when the coordinator went silent, while Silent

	Challenge string    // Quiet, Challenged or Deaf
	Nonce     string    // the nonce of the current challenge
	Asked     time.Time // when it was pushed in
	LastPong  time.Time // when the session last answered a current nonce
	Pongs     int       // session pongs seen, in all

	Idle      string    // Awake, Woken or Noted
	IdleSince time.Time // when the idle measure starts, if no write is newer: the start, or when a card was first held
	WokenAt   time.Time // when the wake turn was given, while Woken or Noted
	WokenSeen time.Time // the newest write the wake was given on; a newer one answers it
}

// Start is the daemon's state as it comes up at now: connected, with the
// start standing in for the last ping (a coordinator that never pings is
// silent one window after the start), and nothing asked of the session.
func Start(now time.Time) *Machine {
	return &Machine{Window: Window, Connection: Connected, LastPing: now, Challenge: Quiet, Idle: Awake, IdleSince: now}
}

// Ping is a ping arriving at now from seat (held since since) with nonce:
// the connection is back if it was silent (the session is told once), and
// the session is challenged with this nonce, whatever it was before, so
// only the newest nonce counts (tla/Friend.tla: Ping). The ping itself
// goes into the session as the message it arrived in; the daemon delivers
// that, so the pushes here are only the daemon's own words.
func (m *Machine) Ping(now time.Time, seat string, since time.Time, nonce string) []Push {
	var out []Push
	if m.Connection == Silent {
		out = append(out, Push{"coordinator back", fmt.Sprintf("coordinator back: %s has the seat (since %s); silent from %s to %s", seat, since.UTC().Format(time.RFC3339), m.SilentFrom.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))})
	}
	m.Connection, m.LastPing, m.Seat, m.SeatSince = Connected, now, seat, since
	if m.Challenge != Deaf {
		m.Challenge = Challenged
	}
	m.Nonce, m.Asked = nonce, now
	return out
}

// Pong is the session answering nonce at now: the current nonce ends the
// challenge; a stale one changes nothing and says so (tla/Friend.tla: Pong).
func (m *Machine) Pong(now time.Time, nonce string) (current bool) {
	if nonce == "" || nonce != m.Nonce || m.Challenge == Quiet {
		return false
	}
	m.Challenge, m.LastPong, m.Pongs = Quiet, now, m.Pongs+1
	return true
}

// Tick is the clock at now: a window without a ping makes the coordinator
// silent, said to the session exactly once per outage; a window challenged
// with no pong makes the session deaf (tla/Friend.tla: Tick).
func (m *Machine) Tick(now time.Time) []Push {
	var out []Push
	if m.Connection == Connected && now.Sub(m.LastPing) >= m.Window {
		m.Connection, m.SilentFrom = Silent, m.LastPing
		out = append(out, Push{"coordinator silent", fmt.Sprintf("coordinator silent since %s: no ping for %s; keep working and keep your queue file; the next ping says who has the seat", m.LastPing.UTC().Format(time.RFC3339), m.Window)})
	}
	if m.Challenge == Challenged && now.Sub(m.Asked) >= m.Window {
		m.Challenge = Deaf
	}
	return out
}

// IdleStep is the idle watch at now, with active the session's newest write
// (zero: none known), cards the cards she holds and after the idle setting
// (docs/SPEC-FRIEND.md, idle wake): holding no card is awake, and the measure
// starts again when one is held; a write newer than the one the wake was given
// on is awake again; awake with no write for after is one wake turn; woken for
// after with no write is one note to the coordinator; noted says nothing more
// until a write or no card ends it.
func (m *Machine) IdleStep(now, active time.Time, cards int, after time.Duration) (wake, note bool) {
	if cards == 0 {
		m.Idle, m.IdleSince = Awake, now
		return false, false
	}
	if m.Idle != Awake && active.After(m.WokenSeen) {
		m.Idle = Awake
	}
	since := m.IdleSince
	if active.After(since) {
		since = active
	}
	switch {
	case m.Idle == Awake && now.Sub(since) >= after:
		m.Idle, m.WokenAt, m.WokenSeen = Woken, now, active
		return true, false
	case m.Idle == Woken && now.Sub(m.WokenAt) >= after:
		m.Idle = Noted
		return false, true
	}
	return false, false
}

// pingText is the ping as the session reads it: the nonce, the seat, and
// the one line to run, so a small model gets it right.
func PingText(seat string, since time.Time, nonce string) string {
	return fmt.Sprintf("PING %s\nseat=%s since=%s\nAnswer first, before anything else, with one command: nova-friend pong --as <you> --nonce %s --queue <tasks queued> --working <tasks working> --width <your width>\nThen go on with what you were doing.", nonce, seat, since.UTC().Format(time.RFC3339), nonce)
}

// WakeMark marks a ping as a wake check (nova-friend ping --wake): a line of
// its own in the ping's body.
const WakeMark = "wake=1"

// WakePingText is a wake check as the coordinator sends it: the ping, and
// WakeMark. The daemon answers it at once as any ping and, the session being
// free, pushes the pong line in as its own turn, so the session is asked
// even with no message waiting (docs/SPEC-FRIEND.md, session-pong.w1;
// tla/Friend.tla, WakeTurn).
func WakePingText(seat string, since time.Time, nonce string) string {
	return PingText(seat, since, nonce) + "\n" + WakeMark
}

// IsWake says whether a ping's text asks for a wake check: one of its lines
// is WakeMark (docs/SPEC-FRIEND.md, session-pong.w1).
func IsWake(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == WakeMark {
			return true
		}
	}
	return false
}

// PongLine is the session pong as it travels on the bus: pong <nonce>
// queue=<n> working=<n> width=<n>.
func PongLine(nonce string, queue, working, width int) string {
	return fmt.Sprintf("pong %s queue=%d working=%d width=%d", nonce, queue, working, width)
}

// ParsePong reads a pong line; ok is false when the text is no pong.
func ParsePong(text string) (nonce string, queue, working, width int, ok bool) {
	n, err := fmt.Sscanf(text, "pong %s queue=%d working=%d width=%d", &nonce, &queue, &working, &width)
	if err != nil || n < 1 {
		var only string
		if k, _ := fmt.Sscanf(text, "pong %s", &only); k == 1 {
			return only, 0, 0, 0, true // the numbers are optional
		}
		return "", 0, 0, 0, false
	}
	return nonce, queue, working, width, true
}

// ParsePing reads the nonce, seat and since off a ping's text; ok is false
// when the text is no ping. A ping with no seat line is from an unnamed
// coordinator: the nonce still counts.
func ParsePing(text string) (nonce, seat string, since time.Time, ok bool) {
	if _, err := fmt.Sscanf(text, "PING %s", &nonce); err != nil {
		return "", "", time.Time{}, false
	}
	var s string
	if _, err := fmt.Sscanf(afterLine(text), "seat=%s since=%s", &seat, &s); err == nil {
		since, _ = time.Parse(time.RFC3339, s) // ignored: a since that is no instant reads as the zero time, said in the result
	}
	return nonce, seat, since, true
}

func afterLine(text string) string {
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			return text[i+1:]
		}
	}
	return ""
}
