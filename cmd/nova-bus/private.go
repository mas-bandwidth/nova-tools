package main

// private.go is the whole of `inbox --decide`'s PRIVATE route, and it is a file of its own
// so that the boundary can be read rather than argued about.
//
// The contract (Stella, 2026-09-19T23:13Z, settling #1644): "A bus without `.public` is
// private. Explicit `inbox --decide` may use rules or a mechanically admitted local/private
// decider with no network or provider-key access; it should not blanket-refuse when that
// safe path exists. If the requested route cannot be satisfied privately, refuse before
// client, key or network, and never fall back to a public route. A `local` label or
// redaction alone does not prove the boundary."
//
// MECHANICALLY ADMITTED means the compiler keeps the promise, not a label and not a flag a
// caller sets. privateDecider is a different TYPE from the public route's noteDecider: it
// has no base URL, no key-env name and no client field, so there is nothing in it to build
// a client from or read a key with. The private route constructs this type and no other.
// cmd/nova-bus/private_boundary_test.go holds the rest of the promise against this file's
// own source: no `internal/decide` import, no `net/*`, no environment read, no `decide.New`.
//
// What is DELIBERATELY NOT HERE: any override. There is no `--allow-private`, no
// `ALLOW-PRIVATE=true` marker, no environment variable and no second marker file. A note
// the rule table has no row for is refused by name, before a client, before a key and
// before a socket, and the run never retries it on the public route.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// busIsPrivate reports whether this clone is a private bus: one that does not carry the
// .public marker. The marker is the clone's own statement that its text may leave; its
// ABSENCE is the default, so a bus nobody has said anything about is private.
func busIsPrivate(busDir string) bool {
	_, err := os.Stat(filepath.Join(busDir, publicMarker))
	return err != nil
}

// ruleRow is the mechanical table, consulted first on EVERY route and the only thing
// consulted on the private one. A subject beginning STOP: or HOLD: is a structured signal
// and is never filtered semantically (Stella's standing rule, docs/CLI.md); everything
// else has no row, and the second return says so.
//
// It is one table because two tables are two answers: the public route reads this same
// function, so a private bus and a public bus agree about every note the table can judge.
func ruleRow(subject string) (noteJudgment, bool) {
	if strings.HasPrefix(subject, "STOP:") || strings.HasPrefix(subject, "HOLD:") {
		return noteJudgment{kind: "edge", needsReply: 1, wake: wakeNeedsAction, conf: 1}, true
	}
	return noteJudgment{}, false
}

// privateDecider answers --decide on a bus with no .public marker, from the rule table and
// from nothing else.
//
// Read its fields: a bus directory to open note files in, a confidence floor to tally
// against, a cache and a tally. There is no base URL, no key-env name and no client. That
// absence is the admission -- the private route cannot call out because the value it holds
// has nothing to call out with, which is a fact about the type rather than a promise about
// the flags.
type privateDecider struct {
	busDir string
	floor  float64
	cache  map[string]noteJudgment
	counts decideCounts
}

func newPrivateDecider(busDir string, floor float64) *privateDecider {
	return &privateDecider{busDir: busDir, floor: floor, cache: map[string]noteJudgment{}}
}

// judge answers one note from the table, or refuses it. The refusal is returned as an error
// so that it takes the listing's existing INBOX REFUSED path: one typed line, exit 2, and
// no second attempt anywhere. Nothing below this line opens a socket or reads an
// environment variable, and there is no branch in it that could.
func (d *privateDecider) judge(e bus.OpenEntry) (noteJudgment, error) {
	if j, ok := d.cache[e.Path]; ok {
		return j, nil
	}
	subject, body, owner := e.Subject, "", ""
	if raw, err := os.ReadFile(filepath.Join(d.busDir, filepath.FromSlash(e.Path))); err == nil {
		if n, perr := bus.ParseNote(e.Path, string(raw)); perr == nil {
			if subject == "" {
				subject = n.Header.Subject
			}
			body = n.Body
			owner = n.Header.To
		}
	}
	j, ok := ruleRow(subject)
	if !ok {
		return noteJudgment{}, &privateRouteError{ID: e.ID, Path: e.Path}
	}
	// owner and ref are read mechanically off the local file, the same as the public
	// route's judge -- never asked of a provider, so the private route carries them too.
	if j.wake == "" {
		j.wake = wakeUnknown
	}
	j.owner = owner
	j.refs = noteRefs(subject, body)
	d.cache[e.Path] = j
	d.counts.n++
	if j.needsReply >= 0.5 {
		d.counts.needsReply++
	}
	if j.conf < d.floor {
		d.counts.belowFloor++
	}
	if wakesReader(j.wake) {
		d.counts.wake++
	}
	return j, nil
}

func (d *privateDecider) decided() decideCounts { return d.counts }

// privateRoute is true for this type and this type only, which is how the run's existing
// INBOX DECIDED receipt comes to carry `privacy=private decider=rules` -- the actual
// privacy of the bus and the decider that actually answered (Stella: "Report the actual
// privacy/source/refusal through existing typed receipts"). It is a fact about the type,
// so no caller can claim it and no caller can suppress it.
func (d *privateDecider) privateRoute() bool { return true }

// privateRouteError is the typed refusal for a note whose only remaining route is a public
// provider on a private bus. It is raised BEFORE any client is built, before the key-env is
// read and before any socket exists -- there is no code path from here to any of the three.
//
// `why=private-evidence` is SPEC-DECIDE S7's own token for this refusal, so the line a
// reader greps for on a bus is the line the spec already names.
type privateRouteError struct {
	ID   string
	Path string
}

func (e *privateRouteError) Error() string {
	return fmt.Sprintf("privacy=private decider=rules why=private-evidence id=%s path=%s: this clone carries no %s marker, so --decide answers from the rule table alone; this note has no rule row, and the only route left reads a provider key and calls out, which private evidence may not take -- there is no flag that changes it",
		oneline.Field(dash(e.ID)), oneline.Field(e.Path), publicMarker)
}
