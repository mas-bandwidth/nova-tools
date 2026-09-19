package bus

import "strings"

// THE TWO STREAMS, AND WHY THE SPLIT IS A CONTRACT.
//
// nova-bus writes on two streams, and which stream a line goes to is part of
// the interface rather than a habit of whoever wrote the line:
//
//	stdout is the PROTOCOL stream. Every line a listing verb writes on it
//	begins with one of the documented two-word prefixes below, and the
//	consumers -- nova-wake watch, nova-wake serve, anything that shells out to
//	`nova-bus inbox` or `nova-bus wait` -- parse it line by line.
//
//	stderr carries refusals, failures and PROGRESS: what the program is doing
//	while it is doing it. A consumer reads it so that an INBOX REFUSED is never
//	lost (the grep of 2026-09-10), but nothing on it is protocol.
//
// Glenn's rule has two halves and until 2026-09-18 only the first was written
// down: a program that takes longer than 0.1 s says what it is doing on stderr,
// AND a progress line never enters a protocol stream a consumer parses.
//
// The second half was paid for the same day. The since-walk's
// `INBOX WALK commits=1/1 notes=0 elapsed=3ms` went to stderr, exactly as the
// first half asks -- and nova-wake reads nova-bus's stdout and stderr TOGETHER,
// so the progress line arrived at a classifier whose default case is PRINT. It
// was relayed as `WAKE BUS LINE INBOX WALK ...`, counted as a change in the
// world, and the poll that found "news" returned before the mail the watcher
// was waiting for came down: `relayed=2` where the test wanted one note. The
// same line had broken nova-bus's own continuation tests hours earlier.
//
// The fix is a class fix and it lives HERE, in one place both sides read:
// nova-bus's own test asserts that no progress prefix ever reaches stdout, and
// every consumer that reads the two streams together drops progress before it
// classifies anything. A NEW progress line is then one entry in
// ProgressPrefixes and is safe in every consumer at once -- which is the
// property the instance fix (teach nova-wake about INBOX WALK) would not have
// had.

// ProgressPrefixes are the line prefixes nova-bus uses for progress: what it is
// doing while it does it. They are written on stderr ONLY, they are not
// protocol, and a consumer that parses nova-bus's output drops them.
//
// Each entry is matched as a whole-token prefix, so "INBOX WALK" covers
// `INBOX WALK commits=...` and does not cover a hypothetical `INBOX WALKER`.
// The bounded walk is no longer progress: it is the event line `INBOX BOUNDED`
// below, printed on stdout, and it is not matched here.
var ProgressPrefixes = []string{
	"INBOX WALK",
}

// ProtocolPrefixes are the documented prefixes of nova-bus's stdout: the whole
// vocabulary of the protocol stream, across every verb. A line a listing verb
// writes on stdout begins with one of these.
//
// It is an ALLOW-LIST FOR STDOUT AND FOR NOTHING ELSE. It never decides what a
// consumer shows: a line a consumer cannot classify is a line it prints, which
// is why an INBOX FAIL, an INBOX REFUSED or a token from a future nova-bus
// still reaches the window. This list is what nova-bus's own test holds ITSELF
// to, so that stdout stays parseable.
var ProtocolPrefixes = []string{
	"BUS INDEX", "BUS OK", "BUS SCOPE", "BUS WARN",
	"CLOSE OK",
	"DRAFT OK",
	"INBOX BODIES", "INBOX BODY", "INBOX BOUNDED", "INBOX CURSOR", "INBOX HEARD", "INBOX LEGACY",
	"INBOX NOTE", "INBOX OK", "INBOX OPEN", "INBOX RECEIPT", "INBOX SCOPE",
	"INBOX SWITCH", "INBOX UNADDRESSED", "INBOX UNREADABLE",
	"NAMES GROUP", "NAMES NAME", "NAMES OK",
	"RECEIPT ALREADY", "RECEIPT OK",
	"REPLY OK",
	"SEND DRAFT", "SEND NOTE", "SEND OK",
	"WAIT DONE", "WAIT NOTE", "WAIT OK", "WAIT TIMEOUT",
}

// IsProgress answers whether a line nova-bus wrote is progress rather than
// protocol. It is the one place the question is answered, for nova-bus and for
// every consumer of it.
func IsProgress(line string) bool { return hasTokenPrefix(line, ProgressPrefixes) }

// protocolOpeners are the documented stdout lines whose second token is a FIELD
// rather than a word, so a two-word prefix cannot name them: `wait`'s opening
// line is `WAIT as=<name> timeout=<d> interval=<d> cursor=<sha|->`. They are
// matched as plain prefixes, and kept apart from ProtocolPrefixes so that
// "matched on token boundaries" stays true of everything in that list.
var protocolOpeners = []string{"WAIT as="}

// IsProtocol answers whether a line begins with a documented stdout prefix.
func IsProtocol(line string) bool {
	if hasTokenPrefix(line, ProtocolPrefixes) {
		return true
	}
	for _, p := range protocolOpeners {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// hasTokenPrefix matches a prefix on TOKEN boundaries: the line is the prefix,
// or the prefix followed by a space. A substring match would make "INBOX OK" a
// prefix of a line nobody wrote and "INBOX WALK" a prefix of "INBOX WALKER".
func hasTokenPrefix(line string, prefixes []string) bool {
	line = strings.TrimRight(line, "\r")
	for _, p := range prefixes {
		if line == p || strings.HasPrefix(line, p+" ") {
			return true
		}
	}
	return false
}
