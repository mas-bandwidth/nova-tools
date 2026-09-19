package main

import (
	"time"
)

// transportAllowance is the bounded wall-clock time added to a declared wait --
// operation wait's --timeout or session-stop/session-handoff's --git-timeout --
// on top of the wait the session itself is already spending. The ruling
// (Stella, 2026-09-19T1428Z) names it: a declared wait "retains its declared
// wait plus a bounded 30-second transport allowance".
const transportAllowance = 30 * time.Second

// deadlineState is the three-valued answer a --deadline flag has: absent,
// well-formed, or malformed. A bool cannot carry it, because its single false
// is returned from two places for two inputs that need opposite answers:
// silence takes the ordinary 30-second default, while a broken value refuses
// before send, and no caller can recover which of the two it got.
type deadlineState int

const (
	deadlineAbsent deadlineState = iota
	deadlineWellFormed
	deadlineMalformed
)

// deadlineParse reads a --deadline stamp. A non-empty flag that does not parse
// as RFC3339 is MALFORMED, not absent: the caller typed something. That
// distinction is what lets the client refuse a value it cannot read rather than
// silently substitute the ordinary default for an explicit cap.
func deadlineParse(flag string) (time.Time, deadlineState) {
	if flag == "" {
		return time.Time{}, deadlineAbsent
	}
	at, err := time.Parse(time.RFC3339, flag)
	if err != nil {
		return time.Time{}, deadlineMalformed
	}
	return at, deadlineWellFormed
}

// deadlineStamp is the two-valued view for the callers that only ask whether
// there is a well-formed deadline to cap the local bound with: a false from an
// absent flag and a false from a malformed one both mean "no bound to derive
// here". The refusal for a malformed explicit deadline lives in sessionVerb,
// before either of those callers is reached.
func deadlineStamp(flag string) (time.Time, bool) {
	at, st := deadlineParse(flag)
	return at, st == deadlineWellFormed
}

// declaredWait parses a declared wait: --timeout is a Go duration, --git-timeout
// is a bare integer of seconds. ok is false when neither is present or neither
// parses, in which case the caller keeps the ordinary bound. A socket verb
// carries one spelling or the other and never both.
func declaredWait(timeout, gitTimeout string) (time.Duration, bool) {
	if timeout != "" {
		if d, err := time.ParseDuration(timeout); err == nil {
			return d, true
		}
	}
	if n, ok := bareSeconds(gitTimeout); ok {
		return time.Duration(n) * time.Second, true
	}
	return 0, false
}

// bareSeconds reads a --git-timeout: digits and nothing else, so `45` is a wait
// and `45s` is not. The thin client accepts only the spelling the session's
// field actually has and leaves every other spelling for the session to
// validate. Hand-rolled because strconv is not on this package's audit
// allowlist and the parse is three lines.
func bareSeconds(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

// derivedBound is the wall-clock bound one exchange may spend, read from the
// flags as the caller spelled them. The ordinary exchange is askTimeout. A
// declared wait raises the bound to the declared wait plus its transport
// allowance, because the session is legitimately holding the line that long and
// a client bound set to the wait alone would call a slow answer a silence. An
// explicit --deadline caps whatever bound came before it, so a far-future
// deadline can never turn the ordinary transport bound into an unbounded wait,
// and a deadline already past yields a non-positive result the caller refuses
// before it dials. The measuring instant is the real clock, never --now: --now
// is the engine's instant, for fencing and receipts, and sizing a LOCAL socket
// timeout from a caller-supplied one would let --now 2020-01-01 give a wildly
// wrong bound.
func derivedBound(deadline, timeout, gitTimeout string) time.Duration {
	bound := askTimeout
	if wait, ok := declaredWait(timeout, gitTimeout); ok {
		bound = wait + transportAllowance
	}
	if at, ok := deadlineStamp(deadline); ok {
		if until := time.Until(at); until < bound {
			bound = until
		}
	}
	return bound
}
