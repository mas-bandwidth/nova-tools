package main

import (
	"context"
	"path/filepath"
	"time"

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
// wrote it says whether this start is a reinstall that changed it. post delivers one message
// into her session through the harness's deliver command, the path a card's deal takes. proven
// is the session check's push proof (SessionCheck.Proof): the daemon's own rule that nothing
// goes into a session that has not answered its check holds the contract's push too, and the
// check itself carries the contract until the session answers one, so the session is told
// either way.
func (w world) newSessionContract(ctx context.Context, c *tool.Call, name, dir, state string, post func(ctx context.Context, subject, body string) error, proven func() (bool, string), record func(string), pong func(nonce string) string) *sessionContract {
	s := &sessionContract{
		teller: &friend.ContractTeller{Push: heldUntilProven(post, proven), Write: func(text string) error { return friend.WriteContract(state, text) }, Record: record, Now: w.now},
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

// pushInto is the daemon's push of one message into the session through its harness's deliver
// command (d, the same path a card's deal takes): the subject as the title line, a blank line,
// then the body. A deliver the harness defers (a grok session running no monitor over its wake
// file) is an error the teller and the watch record as a deferred push: nothing was delivered
// into the session and nothing is claimed delivered.
func pushInto(d friend.Deliverer) func(ctx context.Context, subject, body string) error {
	return func(ctx context.Context, subject, body string) error {
		text := subject
		if body != "" {
			text += "\n\n" + body
		}
		_, err := d.Deliver(ctx, text)
		return err
	}
}

// heldUntilProven holds the contract's push until the session has answered its check (the push
// proof): the daemon delivers nothing into a session that has not answered (docs/SPEC-FRIEND.md,
// The push proof), and the check itself carries the contract until it answers one, so the session
// is told either way. The held push is a Deferred the teller records, never a claimed delivery;
// once the session has answered, a change of the wake path, the server or the epoch goes in at
// once, the same path a card's deal takes.
func heldUntilProven(post func(ctx context.Context, subject, body string) error, proven func() (bool, string)) func(ctx context.Context, subject, body string) error {
	if proven == nil {
		return post
	}
	return func(ctx context.Context, subject, body string) error {
		if ok, nonce := proven(); !ok {
			check := "the first session check"
			if nonce != "" {
				check = "session check " + nonce
			}
			return friend.Deferred{Reason: "the push is unproven: " + check + " has not been answered; the check carries the contract"}
		}
		return post(ctx, subject, body)
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
