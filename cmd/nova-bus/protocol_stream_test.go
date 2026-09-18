package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// TestProgressNeverEntersTheProtocolStream is the CLASS this lane closes, held
// as a rule rather than as the one line that broke.
//
// Glenn's rule has two halves. The first has been written down since the
// four-minute silent walk: a program that takes longer than 0.1 s says what it
// is doing, on stderr. The second was paid for on 2026-09-18, when the
// since-walk's `INBOX WALK commits=1/1 notes=0 elapsed=3ms` -- correctly on
// stderr -- reached nova-wake, which reads this program's two streams together
// so that an INBOX REFUSED is never lost. Its classifier's default case PRINTS,
// so the progress line was relayed as `WAKE BUS LINE INBOX WALK ...`, counted
// as a change in the world, and the poll returned "news" before the mail the
// watcher was waiting for came down. The same line had broken this package's
// own continuation tests hours earlier. A progress line never enters a protocol
// stream a consumer parses.
//
// Two assertions, and they are the halves of the rule:
//
//  1. Every line a LISTING VERB writes on stdout begins with a documented
//     protocol prefix (internal/bus.ProtocolPrefixes). stdout is parsed line by
//     line by every consumer of this program, so a line on it that is not
//     protocol is a line somebody's classifier will guess at.
//  2. No progress prefix (internal/bus.ProgressPrefixes) ever appears on
//     stdout, on any verb -- and the walk's progress does appear on stderr, so
//     this test cannot pass by the program having gone quiet.
//
// The listing verbs are the ones under test because they are the ones
// consumers parse. `send --draft`, `draft --skeleton` and `inbox --bodies`
// write a FRAMED PAYLOAD on stdout -- bytes between `SEND DRAFT id=<id>` and
// `SEND DRAFT END`, between `INBOX BODY id=<id> bytes=<n>` and `INBOX BODY
// END` -- and those bytes are a person's prose, not lines of this protocol.
// The frame is protocol; what it carries is not, and part 2 covers it anyway.
func TestProgressNeverEntersTheProtocolStream(t *testing.T) {
	t.Parallel()
	hermetic(t)

	// A bus whose cursor stands 1000 commits behind HEAD, which is what makes
	// the since-walk long enough to narrate at all: with --max-commits 2000 it
	// finishes and prints progress, and at the default bound it stops and
	// prints the bounded line. Both are progress, and the point of the test is
	// that a consumer sees neither on stdout.
	long := longBus(t, 1000)
	plain, _ := busDir(t)

	runs := []struct {
		what string
		args []string
	}{
		{"a walk that finishes", []string{"inbox", "--bus", long, "--as", "Ada", "--receipt-max-words", "40", "--max-commits", "2000"}},
		{"a walk the bound stops", []string{"inbox", "--bus", long, "--as", "Ada", "--receipt-max-words", "40"}},
		{"a plain inbox", []string{"inbox", "--bus", plain, "--as", "Ada", "--receipt-max-words", "40"}},
		{"a full inbox", []string{"inbox", "--bus", plain, "--as", "Ada", "--receipt-max-words", "40", "--full"}},
		{"the open list", []string{"inbox", "--bus", plain, "--as", "Ada", "--receipt-max-words", "40", "--full", "--open"}},
		{"the bus listing", []string{"bus", "--bus", plain}},
		{"the roster", []string{"names", "--bus", plain}},
		// `wait` is `inbox` on a clock and they share one listing, and the
		// listing a poll made is printed on wait's OWN stdout after the poll
		// returns. So the stream split has to survive being carried: a wait
		// that captured the nested listing's two streams into one buffer would
		// put the walk's progress on stdout by the other road.
		{"a wait that times out", []string{"wait", "--bus", plain, "--as", "Ada", "--receipt-max-words", "40",
			"--timeout", "1s", "--interval", "1s", "--remote", "origin", "--branch", "main"}},
	}

	sawProgress := false
	for _, run := range runs {
		r := invoke(t, "", run.args...)
		for _, line := range strings.Split(r.stdout, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if bus.IsProgress(line) {
				t.Errorf("%s put a PROGRESS line on stdout, which is the stream consumers parse:\n%s", run.what, line)
				continue
			}
			if !bus.IsProtocol(line) {
				t.Errorf("%s wrote a line on stdout with no documented protocol prefix; add the prefix to internal/bus.ProtocolPrefixes or move the line to stderr:\n%s", run.what, line)
			}
		}
		for _, line := range strings.Split(r.stderr, "\n") {
			if bus.IsProgress(line) {
				sawProgress = true
			}
		}
	}
	if !sawProgress {
		t.Error("no progress line was written on stderr by any of these runs; a program over 0.1 s says what it is doing, and this test must not pass by the program having gone quiet")
	}
}

// TestTheProgressRegistryMatchesTheLineThisProgramWrites keeps the registry in
// internal/bus honest about THIS program: the walk's two shapes are both
// progress by the shared answer, so a consumer that asks internal/bus drops
// both without having heard of either.
func TestTheProgressRegistryMatchesTheLineThisProgramWrites(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"INBOX WALK commits=1/1 notes=0 elapsed=3ms",
		`INBOX WALK bounded commits=500 cursor=0edc81b7 behind=more-than-500 notes=0 remedy="raise --max-commits or close --before <instant>"`,
	} {
		if !bus.IsProgress(line) {
			t.Errorf("internal/bus does not know this is progress, so no consumer does:\n%s", line)
		}
		if bus.IsProtocol(line) {
			t.Errorf("a progress line is also claimed as protocol, which is the two halves of the rule contradicting each other:\n%s", line)
		}
	}
	// The complement: the lines consumers DO parse are protocol and are never
	// dropped as progress.
	for _, line := range []string{
		"INBOX NOTE id=bo-abcdef012345 from=Bo addr=to at=2026-09-07T00:01:00Z path=from-bo/x.md: A question",
		"INBOX OPEN carrying=2 heard=1 large=false remedy=-",
		"INBOX OK as=Ada carrying=2 open=0 notes=1 receipts=0 heard=1 unaddressed=0 unreadable=0",
		"INBOX HEARD id=bo-111111111111 from=Bo addr=to at=2026-09-07T00:02:00Z path=from-bo/y.md: Heard",
	} {
		if bus.IsProgress(line) {
			t.Errorf("a protocol line is dropped as progress, which is the false quiet arriving by the other road:\n%s", line)
		}
		if !bus.IsProtocol(line) {
			t.Errorf("a documented protocol line is not in internal/bus.ProtocolPrefixes:\n%s", line)
		}
	}
	// A prefix match on token boundaries and not on substrings: a future
	// `INBOX WALKER` is not this progress line.
	if bus.IsProgress("INBOX WALKER id=x") {
		t.Error("the progress match is a substring match, so a future token that merely starts with one would be dropped unread")
	}
}
