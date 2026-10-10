package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
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
// wrote it says whether this start is a reinstall that changed it. push sends one message into
// her session through the harness's deliver command, the path a card's deal takes.
func (w world) newSessionContract(ctx context.Context, c *tool.Call, name, dir, state string, push func(ctx context.Context, subject, body string) error, record func(string), pong func(nonce string) string) *sessionContract {
	s := &sessionContract{
		teller: &friend.ContractTeller{Push: push, Write: func(text string) error { return friend.WriteContract(state, text) }, Record: record, Now: w.now},
		watch:  &friend.MonitorWatch{Post: push, Pong: pong, Record: record, Now: w.now},
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

// deliverPush is a harness's deliver command as the contract's push path: one message, its
// subject and body, into the session as one turn — the same path a card's deal takes. The
// subject opens the turn only when the body does not already (the contract's body does). A
// deliver that defers (no monitor, no window) is returned as its reason, so the record says the
// push did not go in; the checks still carry the contract.
func deliverPush(d friend.Deliverer) func(ctx context.Context, subject, body string) error {
	if d == nil {
		return nil
	}
	return func(ctx context.Context, subject, body string) error {
		text := body
		if subject != "" && !strings.HasPrefix(body, subject) {
			text = subject + "\n\n" + body
		}
		exit, err := d.Deliver(ctx, text)
		if err != nil {
			return err
		}
		if exit != 0 {
			return fmt.Errorf("the harness deliver command exited %d", exit)
		}
		return nil
	}
}

// contractChannel is the deliver command the contract and a deferred check go in by. Every
// harness has one; for grok it is the file a monitor actually tails (Wake ""). A wake path that
// changed leaves the session's monitor on the old file until the contract tells it the new one,
// and a push to the configured path alone would defer — the exact session that was never told.
// A harness that opens a session per lane (opencode), with no session named, has no one session
// to push into: the adapter would pick the newest, a lane's, so the check finds it and carries
// the contract instead (nil).
func contractChannel(harness, session, dir string, deliver friend.Deliverer, run friend.Exec, out io.Writer) friend.Deliverer {
	if harness == "grok" {
		return &friend.Grok{Dir: dir, Wake: "", Run: run, Out: out}
	}
	if session == "" {
		if _, lanes := deliver.(friend.LaneHarness); lanes {
			return nil
		}
	}
	return deliver
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
