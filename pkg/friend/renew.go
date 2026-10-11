package friend

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// A lane's harness session full or missing renews itself (fix
// a-friend-session-full-or-missing-starts-fresh; the owner, 2026-10-10: what
// else is manual that should be automatic from the machine): the lane starts a
// fresh session of its own, seeded from the friend's own files (LaneSeed, the
// standing brief), its card continues in it, and the seat is told once per
// renewal. No hand session plus a restart.

// contextFull holds the wordings of a provider's refusal that say the
// session's context window is full: not the message at fault, and not a
// usage limit a reset lifts — only a fresh session takes a turn again.
// Anthropic's invalid_request_error "prompt is too long: ... tokens > ...
// maximum", OpenAI's "maximum context length ..." (its code
// context_length_exceeded travels beside that message) and the same sentence
// in the other providers' wordings.
var contextFull = []string{
	"prompt is too long",
	"maximum context length",
	"context length exceeded",
	"context_length_exceeded",
	"input token limit exceeded",
	"context window is full",
	"exceeds the context window",
}

// ContextFull says a provider refusal's reason is one of those wordings.
func ContextFull(reason string) bool {
	text := strings.ToLower(reason)
	for _, w := range contextFull {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// sessionMissing holds the wordings a harness answers with when the session a
// delivery names does not exist (opencode: "Error: no such session", the
// wording the tree's adapter tests hold; opencode is the one LaneHarness
// whose sessions a lane keeps).
var sessionMissing = []string{"no such session", "session not found"}

// SessionMissing says a turn's first error line says the session is gone.
func SessionMissing(firstError string) bool {
	text := strings.ToLower(firstError)
	for _, w := range sessionMissing {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// laneSessionEnd says why a lane's turn ended in its session and not in its
// card, with the one line that said so: "full" when the provider refused the
// turn because the session's context window is full (ProviderRefused,
// ContextFull), "missing" when the harness answered that the session it
// delivered into does not exist. "" when the turn's end is the card's
// business: a stopped or capped turn, a lane without a session (a per-card
// runner runs every card in a fresh one), and a lane that already renewed for
// this card are none of these.
func laneSessionEnd(r laneResult, t *turn, ln *lane) (why, detail string) {
	if ln.card == nil || ln.session == "" || ln.renewed > 0 || t.stopped || t.capped {
		return "", ""
	}
	var refused ProviderRefused
	if errors.As(r.err, &refused) && ContextFull(refused.Reason) {
		return "full", refused.Reason
	}
	if r.err == nil && r.turn.Exit != 0 && SessionMissing(r.turn.FirstError) {
		return "missing", r.turn.FirstError
	}
	return "", ""
}

// renewLaneSession is a lane's turn that its session could not take, though
// the card may: the lane drops the session (the next laneStep opens a fresh
// one, seeded from the friend's own files), the card stays in its hand with
// its attempt not counted, the messages it carried go back pending, counted
// toward nothing and acked never, and the seat is told once. why is "full" or
// "missing"; detail the one line that said so.
func (l *loop) renewLaneSession(ln *lane, t *turn, why, detail string, now time.Time) {
	d := l.d
	ln.renewed++
	old := ln.session
	delete(l.lanes.state.Sessions, ln.n)
	ln.session = "" // the next step opens the fresh session (laneStep)
	l.saveLanes(now)
	for _, e := range t.entries {
		delete(l.inHand, e) // pending: the claim hands them in again, into the fresh session
	}
	if t.notice != nil {
		l.owedAgain(t.notice, now)
	}
	d.Record(fmt.Sprintf("%s lane=%d session=%s subject=%s messages=%d took=%s card=kept session_renewed=%s reason=%q: a fresh session is opened, seeded from the friend's own files, and card %s continues in it",
		now.UTC().Format(time.RFC3339), ln.n, old, t.subjects, len(t.entries), now.Sub(t.started).Round(time.Millisecond), why, oneLine(detail, 200), ln.card.ID))
	l.tellKind(bus.KindStatus,
		fmt.Sprintf("friend %s: session %s %s, renewed", d.Friend, old, why),
		fmt.Sprintf("Lane %d's session %s is %s: %s. The lane started a fresh session in its place, seeded from the friend's own files, and card %s continues in it; every message the turn carried stays pending and rides with it. Nothing to do: no hand session, no restart.\n",
			ln.n, old, map[string]string{"full": "full — the provider refused the turn because the session's context window is full", "missing": "missing — the harness answered it is gone"}[why], oneLine(detail, 300), ln.card.ID),
		now)
}
