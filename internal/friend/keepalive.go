package friend

import (
	"slices"
	"strings"
	"time"
)

// The coordinator's side of the connection (docs/SPEC-FRIEND.md, "The
// coordinator's ping"): the coordinator is the server, it pings every friend
// row each PingEvery, and a friend is down once DownAfter passes with no pong.
const (
	PingEvery = time.Second
	DownAfter = 10 * time.Second
	RowsEvery = time.Minute // how often the friend rows are read again
)

// What the coordinator knows of a friend's connection.
const (
	PeerUp   = "up"   // a pong to a ping of the last DownAfter
	PeerDown = "down" // none for DownAfter
)

// Change is one friend's connection changing state, the one line the
// keepalive says: never one per ping.
type Change struct {
	Friend   string
	State    string    // PeerUp or PeerDown
	LastPong time.Time // zero when the friend never answered
	Reason   string    // why down: no pong, or the ping could not be sent
}

// Keepalive is the coordinator's state: per friend, the nonces it sent and
// when, and the last pong to one of them. It is stepped with the clock the
// loop reads; the loop owns the transport (cmd/nova-friend serve), so every
// rule is tested with no socket and no real time.
type Keepalive struct {
	DownAfter time.Duration // DownAfter unless a test shortens it
	peers     map[string]*peer
}

type peer struct {
	state string               // "" until the first pong or DownAfter, then PeerUp or PeerDown
	since time.Time            // when the friend row was first read
	last  time.Time            // the last pong, zero before any
	sent  map[string]time.Time // the nonces of the last DownAfter, and when each went
	why   string               // the last ping's failure, "" once one is sent
}

// NewKeepalive is the coordinator's state with no friend yet.
func NewKeepalive() *Keepalive {
	return &Keepalive{DownAfter: DownAfter, peers: map[string]*peer{}}
}

// Friends sets who is pinged to the friend rows read at now: a new row is
// pinged from now, and one no longer read is forgotten.
func (k *Keepalive) Friends(now time.Time, names []string) {
	for n := range k.peers {
		if !slices.Contains(names, n) {
			delete(k.peers, n)
		}
	}
	for _, n := range names {
		if k.peers[n] == nil {
			k.peers[n] = &peer{since: now, sent: map[string]time.Time{}}
		}
	}
}

// Names is the friends pinged, sorted.
func (k *Keepalive) Names() []string {
	out := make([]string, 0, len(k.peers))
	for n := range k.peers {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Sent records a ping to friend with nonce at at.
func (k *Keepalive) Sent(friend, nonce string, at time.Time) {
	if p := k.peers[friend]; p != nil {
		p.sent[nonce], p.why = at, ""
	}
}

// Failed records that the ping to friend could not be sent, and why.
func (k *Keepalive) Failed(friend, why string) {
	if p := k.peers[friend]; p != nil {
		p.why = why
	}
}

// Pong records a pong from friend with nonce, read at now (the coordinator's
// clock, never the store's). Only a nonce sent to that friend in the last
// DownAfter answers, and only once: a stale, replayed or other friend's nonce
// changes nothing. It answers whether the pong counted.
func (k *Keepalive) Pong(friend, nonce string, now time.Time) bool {
	p := k.peers[friend]
	if p == nil {
		return false
	}
	at, ok := p.sent[nonce]
	if !ok || now.Sub(at) >= k.DownAfter {
		return false
	}
	delete(p.sent, nonce)
	p.last = now
	return true
}

// Step is the clock at now: every friend whose state changes, in name order.
// Up is a pong within DownAfter; down is DownAfter with none, counted from
// the last pong, else from when the row was first read.
func (k *Keepalive) Step(now time.Time) []Change {
	var out []Change
	for _, n := range k.Names() {
		p := k.peers[n]
		for nonce, at := range p.sent {
			if now.Sub(at) >= k.DownAfter {
				delete(p.sent, nonce)
			}
		}
		from := p.last
		if from.IsZero() {
			from = p.since
		}
		state := p.state
		switch {
		case !p.last.IsZero() && now.Sub(p.last) < k.DownAfter:
			state = PeerUp
		case now.Sub(from) >= k.DownAfter:
			state = PeerDown
		}
		if state == p.state {
			continue
		}
		p.state = state
		c := Change{Friend: n, State: state, LastPong: p.last}
		if state == PeerDown {
			c.Reason = "no pong for " + k.DownAfter.String()
			if p.why != "" {
				c.Reason += "; the ping could not be sent: " + p.why
			}
		}
		out = append(out, c)
	}
	return out
}

// PongNonce is the nonce a pong body answers: a daemon-pong's or a session
// pong's; ok is false when the body is neither.
func PongNonce(body string) (nonce string, ok bool) {
	body = strings.TrimSpace(body)
	if n, found := strings.CutPrefix(body, "daemon-pong "); found {
		return strings.TrimSpace(n), true
	}
	n, _, _, _, ok := ParsePong(body)
	return n, ok
}
