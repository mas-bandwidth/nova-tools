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
	"time"
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

// Push is text the daemon owes the session as a turn: the ping (with the
// pong line to run), or a word about the coordinator.
type Push struct {
	Subject string
	Text    string
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
}

// Start is the daemon's state as it comes up at now: connected, with the
// start standing in for the last ping (a coordinator that never pings is
// silent one window after the start), and nothing asked of the session.
func Start(now time.Time) *Machine {
	return &Machine{Window: Window, Connection: Connected, LastPing: now, Challenge: Quiet}
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

// pingText is the ping as the session reads it: the nonce, the seat, and
// the one line to run, so a small model gets it right.
func PingText(seat string, since time.Time, nonce string) string {
	return fmt.Sprintf("PING %s\nseat=%s since=%s\nAnswer first, before anything else, with one command: nova-friend pong --as <you> --nonce %s --queue <tasks queued> --working <tasks working> --width <your width>\nThen go on with what you were doing.", nonce, seat, since.UTC().Format(time.RFC3339), nonce)
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
