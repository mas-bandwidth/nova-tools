package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// DefaultStuckAfter is how long a turn may run with output but no completion
// before the daemon replaces the session (--stuck-after). It is longer than
// DefaultSilentStop: a turn that prints is working until this.
const DefaultStuckAfter = 45 * time.Minute

// DefaultRecoverMax is how many recoveries one RecoverWindow allows
// (--recover-max): past it the session stays broken and a person is needed.
const DefaultRecoverMax = 3

// RecoverWindow is the window a recovery budget is counted over.
const RecoverWindow = time.Hour

// HandoffBytes bounds the cairn or status file the handoff carries.
const HandoffBytes = 32 << 10

// SessionRecovered is the status's word once a fresh session has taken over:
// session=recovered from=<old> to=<new> reason=<...> at=<...>. It stands until
// the new session's first successful turn, which reads ok.
const SessionRecovered = "recovered"

// Recoverer is a harness that can open a fresh session of the friend in the
// same harness and directory and hand it seed as its first turn, answering
// the new session's id: the one-shot lanes' OpenSession, reached through the
// adapter. A harness that cannot open one answers Deferred with the remedy;
// a nil Recoverer is such a harness, and the daemon leaves the session broken
// as it does today.
type Recoverer interface {
	RecoverSession(ctx context.Context, seed string) (session string, err error)
}

// ContextLimit is a Deliverer's answer when a turn's output is the harness's
// context-length or prompt-too-long error: the session cannot continue in
// place, whatever the exit. The daemon replaces the session on the first one.
type ContextLimit struct{ Session, Reason string }

func (c ContextLimit) Error() string {
	return "the session is at its context limit: " + c.Reason
}

// Compaction is a Deliverer's answer when a turn was the harness compacting
// the conversation and nothing else. CompactionAfter of them in a row is a
// loop, and the daemon replaces the session out of it.
type Compaction struct{ Session string }

func (c Compaction) Error() string {
	return "the session compacted its conversation with no other work"
}

// CompactionAfter is how many turns in a row may be a compaction alone before
// the session is replaced.
const CompactionAfter = 3

// contextLimitText is the harness's context-length or prompt-too-long error,
// as the provider prints it: OpenAI's context_length_exceeded, Anthropic's
// "prompt is too long", and the harnesses' own wording.
var contextLimitText = regexp.MustCompile(`(?i)(context[_ ]length|maximum context|prompt is too long|prompt too long|too many tokens|context window)`)

// ContextLimitReason reports whether a provider refusal's reason is the
// context-length or prompt-too-long error.
func ContextLimitReason(reason string) bool { return contextLimitText.MatchString(reason) }

// HandoffText is a fresh session's first turn: the friend's queue file, her
// newest cairn or status file, the pong line and every pending message, so
// the new session picks up where the old one stopped. old is the session
// that was replaced and reason why (docs/SPEC-FRIEND.md,
// session-recovery-r-b.w2).
func HandoffText(friend, dir, old, reason, pong, seat string, msgs []bus.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nova-friend: your session %s was replaced (%s); this is a fresh session of %s. Read the handoff below and continue.\n", dash(old), reason, friend)
	if q, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(QueueFile))); err == nil {
		fmt.Fprintf(&b, "\n== %s ==\n%s\n", QueueFile, strings.TrimRight(string(q), "\n"))
	} else {
		fmt.Fprintf(&b, "\n== %s ==\nnone\n", QueueFile)
	}
	if name, text, ok := newestCairn(dir); ok {
		fmt.Fprintf(&b, "\n== %s ==\n%s\n", name, strings.TrimRight(text, "\n"))
	}
	if pong != "" {
		b.WriteString("\nRun this now, first, exactly as written: " + pong + "\n")
	}
	if len(msgs) > 0 {
		b.WriteString("\n" + BatchFor(seat, msgs, "", ""))
	}
	return b.String()
}

// newestCairn is the newest of the friend's cairn*.md and STATUS.md in dir,
// at most HandoffBytes of it; ok is false when there is none.
func newestCairn(dir string) (name, text string, ok bool) {
	paths, _ := filepath.Glob(filepath.Join(dir, "cairn*.md"))
	if p := filepath.Join(dir, "STATUS.md"); exists(p) {
		paths = append(paths, p)
	}
	var newest string
	var newestAt time.Time
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		if newest == "" || fi.ModTime().After(newestAt) {
			newest, newestAt = p, fi.ModTime()
		}
	}
	if newest == "" {
		return "", "", false
	}
	raw, err := os.ReadFile(newest)
	if err != nil {
		return "", "", false
	}
	if len(raw) > HandoffBytes {
		raw = raw[:HandoffBytes]
	}
	return filepath.Base(newest), string(raw), true
}

// recoverResult is one fresh session's open ending: the old id, the new id,
// the reason and the error, queued for the loop.
type recoverResult struct {
	from, to, reason string
	err              error
}

// breakSession marks the session broken with reason and starts a recovery.
// The messages the failing turn carried stay pending; the recovery's handoff
// carries them, and none is given up.
func (l *loop) breakSession(session, reason string, now time.Time) {
	if l.broken {
		return
	}
	l.broken, l.told = true, false
	l.d.status.Session, l.d.status.SessionID, l.d.status.SessionReason, l.d.status.BrokenAt =
		SessionBroken, session, oneLine(reason, 200), now
	l.startRecover(now, reason)
}

// startRecover opens a fresh session for the broken one: the handoff is its
// first turn. At most recoverMax recoveries in RecoverWindow are allowed;
// past that the session stays broken and the coordinator is told a person is
// needed. A harness with no Recoverer leaves the session broken as today, so
// the loop's own "broken" word goes out. Nothing here gives up a message:
// every pending message rides in the handoff and stays pending.
func (l *loop) startRecover(now time.Time, reason string) {
	d := l.d
	if l.recovering {
		return
	}
	if d.Recover == nil {
		return // no fresh session can be opened: the loop's broken word is the one told
	}
	l.told = true // the recovery, not the old broken word, does the telling
	l.recoveries = pruneRecoveries(l.recoveries, now)
	if len(l.recoveries) >= l.recoverMax {
		if !l.recoverTold {
			l.recoverTold = true
			l.tellKind(bus.KindBlocker,
				fmt.Sprintf("friend %s: session %s broken and needs a person: %s", d.Friend, dash(d.status.SessionID), oneLine(reason, 120)),
				fmt.Sprintf("The session is broken (%s) and %d recoveries in the last %s are spent; the daemon leaves it broken until a person renews it. Every message stays pending, none given up.\n", oneLine(reason, 200), len(l.recoveries), RecoverWindow),
				now)
		}
		return
	}
	pong := ""
	if d.m.Challenge != Quiet && d.PongCommand != nil {
		pong = d.PongCommand(d.m.Nonce)
	}
	pending, fresh, _ := l.b.Peek(l.ctx, d.Friend)
	var msgs []bus.Message
	for _, e := range pending {
		msgs = append(msgs, e.Message())
	}
	for _, e := range fresh {
		msgs = append(msgs, e.Message())
	}
	seed := HandoffText(d.Friend, d.Dir, d.status.SessionID, reason, pong, l.seat(now), msgs)
	l.recovering, l.recoverFrom, l.recoverReason = true, d.status.SessionID, reason
	d.Record(fmt.Sprintf("%s session=recovering from=%s reason=%q: opening a fresh session with the handoff", now.UTC().Format(time.RFC3339), dash(l.recoverFrom), oneLine(reason, 200)))
	go func() {
		id, err := d.Recover.RecoverSession(l.ctx, seed)
		l.recoverCh <- recoverResult{from: l.recoverFrom, to: id, reason: reason, err: err}
	}()
}

// recovered is a fresh session's open ending: it is the session now, the
// status says so, and the coordinator is told once. A Deferred error is a
// harness that cannot open one, with its remedy; any other error leaves the
// session broken and the messages pending.
func (l *loop) recovered(r recoverResult, now time.Time) {
	d := l.d
	l.recovering = false
	if r.err != nil {
		var deferred Deferred
		if errors.As(r.err, &deferred) {
			d.Record(fmt.Sprintf("%s session=broken: no fresh session: %s; %s; the session stays broken and every message stays pending",
				now.UTC().Format(time.RFC3339), oneLine(deferred.Reason, 300), oneLine(deferred.Remedy, 300)))
			return
		}
		d.Record(fmt.Sprintf("%s session=broken: no fresh session from %s: %s; every message stays pending",
			now.UTC().Format(time.RFC3339), dash(r.from), oneLine(r.err.Error(), 300)))
		return
	}
	l.recoveries = append(pruneRecoveries(l.recoveries, now), now)
	l.broken, l.streak, l.refusal, l.unable, l.unableTries, l.told, l.compacting = false, 0, "", "", 0, false, 0
	d.status.Session, d.status.SessionID, d.status.SessionReason, d.status.BrokenAt = SessionRecovered, "", oneLine(r.reason, 200), time.Time{}
	d.status.SessionFrom, d.status.SessionTo, d.status.RecoveredAt = r.from, r.to, now
	d.Record(fmt.Sprintf("%s session=recovered from=%s to=%s reason=%q", now.UTC().Format(time.RFC3339), dash(r.from), dash(r.to), oneLine(r.reason, 200)))
	l.tell(fmt.Sprintf("friend %s: session %s replaced by %s: %s", d.Friend, dash(r.from), dash(r.to), oneLine(r.reason, 120)),
		fmt.Sprintf("The session was replaced by a fresh one (%s) after: %s. The handoff (the queue, her newest cairn or status file, the pong line and every pending message) was its first turn; every message stays pending until the new session takes it.\n", dash(r.to), oneLine(r.reason, 200)), now)
}

// pruneRecoveries drops the recovery times older than RecoverWindow.
func pruneRecoveries(times []time.Time, now time.Time) []time.Time {
	cut := now.Add(-RecoverWindow)
	var kept []time.Time
	for _, t := range times {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	return kept
}
