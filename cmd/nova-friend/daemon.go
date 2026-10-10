package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// sessionContract is the run verb's side of the session contract (docs/SPEC-FRIEND.md, the
// session contract; internal/friend/session_contract.go): the teller that owes the contract to
// the session's checks and pushes it on a change, the watch that pushes a check deferred for
// no monitor as a message and says her down after three, and the inbox pass's start stamps.
type sessionContract struct {
	teller *friend.ContractTeller
	watch  *friend.MonitorWatch
	starts *friend.StartWatch
}

// contractOf is the contract the run verb's flags say: the wake file and the monitor line for
// a harness the session wakes through a monitor (grok: --session is the wake file), the pong
// line, the sprint server, and her outbox.
func contractOf(c *tool.Call, name, dir string, pong func(nonce string) string) friend.Contract {
	k := friend.Contract{Friend: name, Harness: c.Str("harness"), Pong: pong("<nonce>"), Server: c.Str("server"), Reports: filepath.Join(dir, "outbox")}
	if k.Harness == "grok" {
		k.Wake, k.Monitor = c.Str("session"), friend.GrokMonitorLine(c.Str("session"))
	}
	return k
}

// newSessionContract starts the contract for a session friend: CONTRACT.md as the last run
// wrote it says whether this start is a reinstall that changed it. post sends one message from
// the daemon to her own stream, the path a card's deal takes into her session.
func (w world) newSessionContract(ctx context.Context, c *tool.Call, name, dir, state string, post func(ctx context.Context, subject, body string) error, record func(string), pong func(nonce string) string) *sessionContract {
	s := &sessionContract{
		teller: &friend.ContractTeller{Push: post, Write: func(text string) error { return friend.WriteContract(state, text) }, Record: record, Now: w.now},
		watch:  &friend.MonitorWatch{Post: post, Pong: pong, Record: record, Now: w.now},
		starts: &friend.StartWatch{Friend: name, Look: jobSigns(dir)},
	}
	// the deferred check is pushed in place of a deliver that wrote nothing, so that message
	// carries the contract the session was not handed
	s.watch.Contract = func() string { return s.teller.Contract().Text() }
	prior, _, err := friend.ReadContract(state)
	if err != nil {
		record(w.now().UTC().Format(time.RFC3339) + " contract: the last run's " + friend.ContractFile + " cannot be read: " + err.Error())
	}
	s.teller.Start(ctx, contractOf(c, name, dir, pong), prior)
	return s
}

// line reads one of the session check's record lines: an answer is the contract read and the
// no-monitor count cleared; a check deferred for no monitor is pushed as a message.
func (s *sessionContract) line(ctx context.Context, line string) {
	if s == nil {
		return
	}
	if friend.IsSessionAnswer(line) {
		s.teller.Answered()
	}
	s.watch.Line(ctx, line)
}

// downReason is the reason her beat and her presence file say while three checks in a row were
// deferred for no monitor; ok is false otherwise.
func (s *sessionContract) downReason() (string, bool) {
	if s == nil {
		return "", false
	}
	down, why := s.watch.Down()
	return why, down
}

// checkText is a session check's text, with the contract after it while it is owed.
func (s *sessionContract) checkText(check string) string {
	if s == nil {
		return check
	}
	return s.teller.CheckText(check)
}

// postTo is the daemon's message to her own stream through its own store (so the session check
// never reads it as hers): the path every message, a card's deal among them, takes into her
// session.
func postTo(st bus.Store, name string) func(ctx context.Context, subject, body string) error {
	return func(ctx context.Context, subject, body string) error {
		_, err := (&bus.Bus{Store: st}).Send(ctx, bus.Message{From: name, To: []string{name}, Subject: subject, Body: body})
		return err
	}
}

// contract is the verb: the contract the daemon last told her session, as CONTRACT.md holds it.
func (w world) contract(c *tool.Call) *tool.Out {
	state := w.stateDir(c, c.Str("dir"))
	text, found, err := friend.ReadContract(state)
	if err != nil {
		return tool.Refuse("the contract cannot be read: " + err.Error())
	}
	if !found {
		return tool.Refuse("no contract in " + state + ": no daemon of " + c.Str("as") + " has started there; start it (nova-friend run, or install), or name --state-dir")
	}
	return tool.Payload(text)
}
