// wait: inbox on a clock, polling the bus inside one tool call until a note arrives or
// the deadline passes.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdWait is `inbox`, on a clock, INSIDE one tool call.
//
// THE FAILURE THIS CLOSES, and it is not a failure of the bus. A line reading this bus
// through a harness that does not wake it has a poller running beside its session,
// mechanically, on time. What the poller cannot do is get the session's attention: the
// notes land in the checkout and the session, being non-deterministic about housekeeping,
// does not always come back and look. So a note can sit answered by nobody for an hour
// beside a poller that has been doing its job the whole time.
//
// A SESSION INSIDE A TOOL CALL CANNOT FORGET. That is the whole idea here: the harness
// itself wakes the session when the call returns, on every harness there is, because that
// is what a tool call is. So the polling moves INSIDE the tool -- one blocking verb, which
// returns the moment there is something to read and says so when there is not.
//
// It is `inbox` and not a second reader: the same rules about what is addressed to you,
// the same open list, the same switch-day line, the same lines on stdout, so the caller's
// next action is the one an inbox listing always implies. The only things it adds are a
// clock and a fetch. inboxListing is the shared body; there is no second inbox to keep in
// step with this one.
//
// EVERY WAIT HAS A DEADLINE, which is why --timeout is required and has no default: an
// asynchronous wait with no deadline is a line that is stuck rather than waiting, and
// nobody outside can tell the two apart. A timeout is not an error -- it is the answer
// "nothing yet", exit 0, and the caller issues the next one.
//
// rearmCommand rebuilds `wait`'s re-arm line from the caller's own argv, shell-quoting each
// argument so that what is printed is what the caller pasted. The failure it closes: the
// command used to join the raw argv with blanks, so a --bus path holding a blank came back
// split in two -- --bus received the prefix through the blank, and the remainder landed as a
// separate argument -- and pasting it named a bus that does not exist instead of the one it
// was waiting on.
func rearmCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return "nova-bus wait " + strings.Join(quoted, " ")
}

// shellQuote quotes one argument for a POSIX shell: unchanged when it holds nothing the
// shell would act on, otherwise single quotes with the one character that cannot live
// inside them written the shell's own way. A value carrying a blank, a dollar sign or a
// semicolon unquoted would split the pasted command or run something the caller did not
// write.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '/' || c == '.' || c == '_' || c == '-' || c == ':') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func cmdWait(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("wait")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	maxWords := f.fs.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1)")
	timeout := f.fs.Duration("timeout", 0, "how long to wait before returning WAIT TIMEOUT (required; a duration like 25m, at most "+maxWaitTimeout.String()+")")
	until := f.fs.String("until", "", "an absolute deadline as an RFC 3339 UTC instant (e.g. 2026-09-18T18:00:00Z); the wait ends at that moment or at --timeout, whichever comes first")
	idleExit := f.fs.Int("idle-exit", 0, "exit with this code instead of 0 when the wait times out, so a harness that cannot loop can branch on the code without parsing anything; 1 and 2 are refused, they are this tool's own")
	interval := f.fs.Duration("interval", defaultWaitInterval, "how long between polls")
	beat := f.fs.Duration("beat", defaultBeatInterval, "retired and ignored with one WAIT NOTE: the bus carries notes, never beats; presence is friend:<name> in Redis, written by the friend's own runtime")
	beatLease := f.fs.Duration("beat-lease", defaultBeatLease, "retired and ignored with one WAIT NOTE, as --beat")
	noBeat := f.fs.Bool("no-beat", false, "this wait does not own the lane's BEAT, so it never discards one an older wait left: a read-only poll for a process that runs beside a line rather than as it; no wait writes a BEAT; cannot be given with --beat or --beat-lease")
	openList := f.fs.Bool("open", false, "list every open note when this wait returns, not only what is new")
	openMax := f.fs.Int("open-max", defaultOpenMax, "with --open, how many carried entries to print before saying how many more there are")
	openWarn := f.fs.Int("open-warn", defaultOpenWarn, "how many carried entries before every return adds one line saying the list is large and how to empty it")
	bodies := f.fs.Bool("bodies", false, "print bodies for NEW notes, bounded by --max-notes and --max-bytes")
	maxNotes := f.fs.Int("max-notes", defaultBodiesNotes, "with --bodies, maximum NEW items to print")
	maxBytes := f.fs.Int64("max-bytes", defaultBodiesBytes, "with --bodies, maximum body bytes to print")
	after := f.fs.String("after", "", "continue a bounded --bodies snapshot")
	advance := f.fs.Bool("advance", false, "move your cursor to HEAD and push it when this wait returns, the way inbox --advance does")
	remote := f.fs.String("remote", "", "the git remote to fetch the bus from (required: a wait that cannot fetch cannot notice anything)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push the cursor before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "with --advance, commit the cursor but do not push it")
	legacyBefore := f.fs.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339, e.g. 2026-09-09T18:07:00Z) are not carried on your open list, and are counted rather than listed")
	carryHistory := f.fs.Bool("carry-history", false, "on your FIRST --advance, carry every old note on your open list instead of drawing a switch-day line; does nothing otherwise")
	diagnostics := f.fs.Bool("diagnostics", false, "name every unreadable file with its reason, even ones already shown; the default collapses unchanged ones to one count line")
	quietBeats := f.fs.Bool("quiet-beats", false, "retired: accepted and changes nothing, with one WAIT NOTE; a change that is only beats and cursors never wakes a wait")
	onNote := f.fs.Bool("on-note", false, "wait only for a note addressed to the caller: on arrival exit 0 with the note, and on an empty tick exit 0 with WAIT TIMEOUT and the rearm line; requires --timeout, --bus, --as, --remote and --branch; incompatible with --open and --full")
	// --max-commits IS HERE BECAUSE THE REMEDY HAS TO BE TYPEABLE AT THE VERB THAT NEEDS IT
	// The since-walk is bounded in inboxListing, which `wait` polls through, so a
	// wait has always been bounded -- it simply had no way to say a bigger number. One reader's
	// loop ran for days behind a cursor the bus had left far behind, printing the bounded
	// line's `remedy="raise --max-commits"` at a verb that refused the flag.
	maxCommits := f.fs.Int("max-commits", defaultMaxCommits, "how many commits a since-walk may cross before it stops and names the remedy; raise it to read a staler cursor")
	// --remote and --branch are required here and conditional on inbox, because a wait
	// FETCHES: that is the difference between waiting and sleeping. A wait that read only
	// what its checkout already held would wait out its whole timeout beside a bus full of
	// notes, and this tool does not guess a remote.
	f.alsoRefuse = func() string {
		if _, ok := resolveReceiptMaxWords(*maxWords, f.set("receipt-max-words"), *busDir); !ok {
			return receiptMaxWordsRefusal(f.verb, *maxWords)
		}
		return ""
	}
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as, "remote": remote, "branch": branch}) {
		return 2
	}
	// --on-note REFUSES WHEN ITS REQUIRED FLAGS ARE MISSING. Each missing flag
	// gets its own remedy line, because a unit restarted after a harness cap needs to know
	// exactly which flag to add. The spec says: `--on-note needs <flag>; give it, refusing
	// to guess`.
	if *onNote {
		var missing []string
		if *timeout <= 0 {
			missing = append(missing, "--timeout")
		}
		if strings.TrimSpace(*busDir) == "" {
			missing = append(missing, "--bus")
		}
		if strings.TrimSpace(*as) == "" {
			missing = append(missing, "--as")
		}
		if strings.TrimSpace(*remote) == "" {
			missing = append(missing, "--remote")
		}
		if strings.TrimSpace(*branch) == "" {
			missing = append(missing, "--branch")
		}
		if len(missing) > 0 {
			fmt.Fprintf(stderr, "WAIT REFUSED: --on-note needs %s; give it, refusing to guess; run: nova-bus wait -h\n", oneline.Field(missing[0]))
			return 2
		}
		// --on-note WITH --open IS REFUSED. --on-note prints no open frame.
		if *openList {
			fmt.Fprint(stderr, "WAIT REFUSED: --on-note prints no open frame; drop --open; run: nova-bus wait -h\n")
			return 2
		}
	}
	// --no-beat AND --beat ARE ONE DECISION EACH AND THEY DISAGREE. --no-beat
	// says this wait writes no BEAT and --beat/--beat-lease say when and how far to write
	// one; a caller that gave both meant one of them, and guessing which would silently
	// break either presence or the read-only promise. It is refused by name, and the door
	// is the same one every invocation refusal names.
	if *noBeat && (f.set("beat") || f.set("beat-lease")) {
		fmt.Fprint(stderr, "WAIT REFUSED: --no-beat writes no BEAT and --beat/--beat-lease say when to write one; give one or the other; run: nova-bus wait -h\n")
		return 2
	}
	flagLegacy, ok := legacyLine("wait", *legacyBefore, stderr)
	if !ok {
		return 2
	}
	if *carryHistory && !flagLegacy.Before.IsZero() {
		fmt.Fprint(stderr, "WAIT REFUSED: --legacy-before draws a switch-day line and --carry-history says there is none to draw; give one or the other; run: nova-bus wait -h\n")
		return 2
	}
	maxWordsValue, ok := f.receiptMaxWords(*maxWords, f.set("receipt-max-words"), *busDir, stderr)
	if !ok {
		return 2
	}
	if !f.count("open-max", *openMax, stderr) {
		return 2
	}
	if !f.count("max-commits", *maxCommits, stderr) {
		return 2
	}
	if !f.atLeastZero("open-warn", *openWarn, stderr) {
		return 2
	}
	if *after != "" && !*bodies {
		fmt.Fprintln(stderr, "WAIT REFUSED: --after requires --bodies; run: nova-bus wait -h")
		return 2
	}
	if *bodies && !bodyLimit(stderr, "--max-notes", int64(*maxNotes), maxBodiesNotesCeiling) {
		return 2
	}
	if *bodies && !bodyLimit(stderr, "--max-bytes", *maxBytes, maxBodiesBytesCeiling) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.attempts(*attempts, stderr) {
		return 2
	}
	if !f.gitArgs(*remote, *branch, stderr) {
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprint(stderr, "WAIT REFUSED: --timeout is required and is a duration like 25m; every wait has a deadline, and one with no deadline is a line that is stuck rather than waiting; refusing to guess; run: nova-bus wait -h\n")
		return 2
	}
	// THE CEILING IS ABOUT THE HARNESS AND NOT ABOUT THE BUS. This verb is meant to be
	// called from inside a tool call, and every harness kills a tool call that runs too
	// long -- so a wait longer than the harness's limit does not wait longer, it is killed,
	// and the caller is told nothing at all. An hour is above every limit we know of and
	// below anything anybody would call a hang.
	if *timeout > maxWaitTimeout {
		fmt.Fprintf(stderr, "WAIT REFUSED: --timeout %s is longer than %s, which is as long as this verb will block; a wait runs inside your harness's tool call and every harness kills one that runs too long, so a longer timeout is not a longer wait, it is a call that is killed with nothing said; ask your harness for its limit, sit under it, and issue the next wait when this one returns; run: nova-bus wait -h\n",
			oneline.Field(timeout.String()), oneline.Field(maxWaitTimeout.String()))
		return 2
	}
	// --until IS THE DEADLINE A HARNESS ALREADY HAS. A duration is the wrong shape for a
	// caller whose own limit is a MOMENT -- the end of a session, the hour a shift hands over
	// -- because turning one into the other means knowing how long the call took to start,
	// and a caller that guesses that is a caller whose last wait runs past the thing it was
	// waiting for. So the two live side by side and the EARLIER ONE WINS: --timeout is how
	// long this call may block, --until is the moment past which blocking is pointless, and a
	// wait ends at whichever comes first. The ceiling needs no second check: --timeout is
	// required, it is capped at maxWaitTimeout, and the effective window is never longer.
	waitFor := *timeout
	if *until != "" {
		when, err := time.Parse(time.RFC3339, *until)
		if err != nil {
			fmt.Fprintf(stderr, "WAIT REFUSED: --until %s is not an RFC 3339 instant like 2026-09-18T18:00:00Z; %s\n", oneline.Field(*until), oneline.WithRemedy(oneline.Err(err), "nova-bus wait -h"))
			return 2
		}
		left := when.Sub(now)
		if left <= 0 {
			fmt.Fprintf(stderr, "WAIT REFUSED: --until %s is now or in the past (it is %s), so this wait would end before it began; give an instant in the future, or leave --until off and let --timeout bound the call; run: nova-bus wait -h\n",
				oneline.Field(*until), oneline.Field(now.UTC().Format(time.RFC3339)))
			return 2
		}
		waitFor = min(waitFor, left)
	}
	// --idle-exit IS FOR A HARNESS THAT CANNOT LOOP (OpenCode's, and every harness like it): it
	// runs one tool call per turn and branches on the exit code, and it has no way to tell
	// "nothing arrived" from "a note arrived" when both are exit 0. So a timeout may carry a
	// code of the caller's choosing. 1 and 2 are refused rather than allowed: they are this
	// tool's own -- a refusal and an invocation that could not run -- and a harness that saw
	// either would have to parse the output to know which it was, which is the thing this
	// flag exists to make unnecessary. 126 and up are the shell's own.
	if *idleExit < 0 || *idleExit > maxIdleExit {
		fmt.Fprintf(stderr, "WAIT REFUSED: --idle-exit %d is not an exit code this verb will use; give one between 0 and %d, and note that %d and above belong to the shell; run: nova-bus wait -h\n", *idleExit, maxIdleExit, maxIdleExit+1)
		return 2
	}
	if *idleExit == 1 || *idleExit == 2 {
		fmt.Fprintf(stderr, "WAIT REFUSED: --idle-exit %d is this tool's own code -- 1 is a refusal and 2 is an invocation that could not run -- so a harness that got it back could not tell a quiet bus from a broken one; pick another, 3 is free; run: nova-bus wait -h\n", *idleExit)
		return 2
	}
	if *interval < minWaitInterval {
		fmt.Fprintf(stderr, "WAIT REFUSED: --interval %s is shorter than %s, and every poll is a git fetch against somebody's server; refusing to fetch faster than that; run: nova-bus wait -h\n",
			oneline.Field(interval.String()), oneline.Field(minWaitInterval.String()))
		return 2
	}
	if *beat <= 0 {
		fmt.Fprint(stderr, "WAIT REFUSED: --beat must be a positive duration like 60s; run: nova-bus wait -h\n")
		return 2
	}
	if *beatLease <= 0 {
		fmt.Fprint(stderr, "WAIT REFUSED: --beat-lease must be a positive duration like 10m; run: nova-bus wait -h\n")
		return 2
	}
	// THE BUS CARRIES NOTES, NEVER BEATS. Beat commits were most of a busy bus's history,
	// and every clone pulled them and every bus monitor woke on them. Presence is
	// friend:<name> in Redis, written by each participant's own runtime. --beat and
	// --beat-lease stay parseable so a caller's argv does not break, and say so once.
	if f.set("beat") || f.set("beat-lease") || f.set("quiet-beats") {
		fmt.Fprint(stderr, "WAIT NOTE --beat, --beat-lease and --quiet-beats are retired and ignored: the bus carries notes, never beats, and a change that is only beats never wakes a wait; drop them\n")
	}
	// A wait always runs git, so the root check is unconditional -- see the same check, and
	// the same reason for the order it is in, in cmdInbox.
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "WAIT REFUSED: a wait fetches the bus and reads what changed since your cursor, which need git; %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus wait -h"))
		return 2
	}
	c, err := bus.LoadConfig(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "WAIT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus wait -h"))
		return 2
	}
	me, found := c.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "WAIT REFUSED: --as %q names no one on this bus (known: %s); run: nova-bus wait -h\n", *as, oneline.Escape(strings.Join(c.KnownNames(), "; ")))
		return 2
	}
	if me.Lane == "" {
		fmt.Fprintf(stderr, "WAIT REFUSED: %q has no lane on this bus, so nothing can answer for them; run: nova-bus wait -h\n", me.Name)
		return 2
	}
	o := inboxOpts{
		busDir: *busDir, as: *as, maxWords: maxWordsValue,
		openList: *openList, openMax: *openMax, openWarn: *openWarn, advance: *advance,
		remote: *remote, branch: *branch, attempts: *attempts, noPush: *noPush,
		legacy: flagLegacy, carryHistory: *carryHistory,
		bodies: *bodies, maxNotes: *maxNotes, maxBytes: *maxBytes, after: *after,
		me: me, noBeat: *noBeat,
		diagnostics: *diagnostics,
		quietBeats:  *quietBeats,
		maxCommits:  *maxCommits,
		onNote:      *onNote,
	}
	// The cursor as it stands, for the line that says this call BEGAN. A cursor that will
	// not read is not refused here: the first poll's listing refuses it, in the sentence
	// inbox already refuses it in.
	held, _ := bus.ReadCursor(*busDir, me.Lane)
	// A WAIT THAT CANNOT SEE THE BUS IS NOT A WAIT, AND IT SAYS SO BEFORE IT BLOCKS.
	//
	// The distance from a cursor to HEAD only GROWS while a wait runs -- the cursor moves
	// on --advance, at the end, and never during -- so a walk that is over the bound now is
	// over it on every poll this call will make. Every one of those polls reads nothing,
	// and the call then prints the same WAIT TIMEOUT as a wait over a quiet bus. One reader's
	// loop did exactly that, once a minute, for hours, at exit 0:
	//
	//	INBOX WALK bounded commits=500 remedy="raise --max-commits or close --before <instant>"
	//	WAIT TIMEOUT after=1m2.926s polls=2 cursor=8cd06f5a...
	//
	// So it is asked ONCE, here, before anything blocks, and it is a REFUSAL rather than a
	// note: a loop that is green and deaf is worse than one that stops, because nobody goes
	// to look at the one that is green. The bounded walk's own remedy is carried word for
	// word, and --max-commits above is now one of the two things it names.
	if over, err := waitWalkOverBound(*busDir, held.Commit, *maxCommits); err == nil && over {
		fmt.Fprintf(stderr, "WAIT BLIND commits=%d %s\n", *maxCommits, boundedWalkRemedy)
		fmt.Fprintf(stderr, "WAIT REFUSED: as=%s cursor=%s is further behind than this walk may cross, so every poll of this wait would read nothing and it would end saying `nothing yet`; raise the bound or close the backlog, then wait again; run: nova-bus wait -h\n",
			oneline.Field(me.Name), oneline.Field(dash(held.Commit)))
		return 2
	}
	// One line at the start, before anything is waited on, so that a transcript shows the
	// call began and what it was told to do. A tool call that prints nothing for twenty
	// minutes and then prints everything is, while it runs, indistinguishable from one
	// that has hung.
	//
	// THE TWO SPELLINGS ARE ONE DECISION AND NOT A DUPLICATE. A caller that passes neither
	// --until nor --idle-exit sees exactly the line it saw before they existed, byte for byte
	// (testdata/today/wait.txt holds those bytes and readhalf_test.go compares them): a flag
	// nobody used must not change what everybody reads. A caller that passes EITHER gets both
	// fields, filled in, because a deadline and an exit code are what that caller is asking
	// about and half a pair says less than none.
	if *until == "" && *idleExit == 0 {
		fmt.Fprintf(stdout, "WAIT as=%s timeout=%s interval=%s cursor=%s\n",
			oneline.Field(me.Name), oneline.Field(timeout.String()), oneline.Field(interval.String()), oneline.Field(dash(held.Commit)))
	} else {
		fmt.Fprintf(stdout, "WAIT as=%s timeout=%s interval=%s cursor=%s until=%s idle-exit=%d\n",
			oneline.Field(me.Name), oneline.Field(timeout.String()), oneline.Field(interval.String()), oneline.Field(dash(held.Commit)),
			oneline.Field(dash(*until)), *idleExit)
	}
	// A KILLED TICK LEAVES THE NEXT ONE UNABLE TO START. An index.lock older than a
	// minute, with no git still owning the checkout, is the killed git's leftover, and a
	// dirty BEAT is the generated file that same kill left half-written. Both are this
	// tool's. A CURSOR, a hand edit, anything else is not, and is not touched here. The
	// clock on the lock is the machine's clock: `now` above is the note clock, which a
	// test freezes, and a frozen clock would call a lock written today either ancient or
	// not yet born. The repair is recorded only after it has happened, and printed once,
	// from the poll, together with whatever the fast-forward itself had to discard.
	repairs := &repairLog{}
	rep, err := bus.ClearStaleIndexLock(*busDir, time.Now())
	if rep.Scanned {
		printLockScan(stdout, rep.Scan)
	}
	if errors.Is(err, bus.ErrIndexLockChanged) {
		fmt.Fprintf(stderr, "WAIT: %s\n", oneline.Err(err))
	} else if err != nil {
		fmt.Fprintf(stderr, "WAIT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus wait -h"))
		return 1
	} else if rep.Cleared {
		repairs.add("index.lock")
	}
	// A dirty BEAT is what an older wait, killed mid-tick, left behind. No wait
	// writes one now, so the repair is the discard alone: the file goes back to what the
	// bus holds, and nothing is regenerated.
	if !o.noBeat {
		discarded, err := discardDirtyOwnedBeat(o)
		if err != nil {
			repairs.flush(stdout)
			fmt.Fprintf(stderr, "WAIT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus wait -h"))
			return 1
		}
		if discarded {
			repairs.add(bus.BeatPath(me.Lane))
		}
	}
	// The command the caller issues again to re-arm this wait: the next one, with the same
	// flags, echoed back so a harness that does not wake on its own can paste it. A wait is
	// ONE read, with one terminal line saying it ended and must be re-armed. Each argument
	// is shell-quoted, not joined raw: a --bus path carrying a blank must come back as the
	// same one argument after a paste, not split in two.
	next := rearmCommand(args)
	return waitLoop(o, waitFor, *interval, *idleExit, next, stdout, stderr, now, repairs)
}

// discardDirtyOwnedBeat throws away a BEAT this wait owns, when one was already dirty.
// --no-beat does not own the line's BEAT — a process beside the line must not discard the
// harness's — and returns false without looking. discarded is false when there was nothing
// to throw away, so a clean beat is not a repair.
func discardDirtyOwnedBeat(o inboxOpts) (bool, error) {
	if o.noBeat {
		return false, nil
	}
	beat := bus.BeatPath(o.me.Lane)
	dirty, err := bus.PathDirty(o.busDir, beat)
	if err != nil {
		return false, err
	}
	if !dirty {
		return false, nil
	}
	return bus.DiscardPath(o.busDir, beat)
}

// printLockScan is the receipt of one process scan over a stale index.lock of this
// account: how many live gits were placed at this checkout (owner), how many of another
// account were passed over unplaced (foreign), and how many could not be read (unknown).
// It is printed whenever such a scan classified every process, before the WAIT REPAIR
// that clears the lock or the WAIT REFUSED that keeps it, so the evidence for the unlink
// (owner=0 unknown=0) or for the refusal is on the record. A scan that failed as a whole
// has no counts and prints only its refusal.
func printLockScan(w io.Writer, s bus.LockScan) {
	fmt.Fprintf(w, "WAIT SCAN index.lock owner=%d foreign=%d unknown=%d\n", s.Owner, s.Foreign, s.Unknown)
}

// repairLog is the repairs this wait has done and not yet said. add is idempotent: a beat
// discarded on the way in and discarded again so the fast-forward can move is one repair.
// flush prints one WAIT REPAIR line naming exactly what was added, then forgets it, so a
// later tick that repairs something new can say so and a tick that repairs nothing says
// nothing.
type repairLog struct {
	pending []string
}

func (r *repairLog) add(item string) {
	if r == nil || item == "" {
		return
	}
	for _, e := range r.pending {
		if e == item {
			return
		}
	}
	r.pending = append(r.pending, item)
}

func (r *repairLog) flush(w io.Writer) {
	if r == nil || len(r.pending) == 0 {
		return
	}
	fmt.Fprintf(w, "WAIT REPAIR repaired=%s\n", oneline.Field(strings.Join(r.pending, ",")))
	r.pending = nil
}

// waitOwnedPaths is the lane file wait may discard: its own BEAT, and only when this
// call is the one writing it. CURSOR is the reader's place, not a generated beat, and is
// never in this list. --no-beat owns nothing.
func waitOwnedPaths(o inboxOpts) []string {
	if o.noBeat {
		return nil
	}
	return []string{bus.BeatPath(o.me.Lane)}
}

// waitWalkOverBound answers whether this lane's cursor is further behind HEAD than the
// walk's bound, which is the one condition under which a wait can see nothing whatever
// happens.
//
// IT FAILS OPEN, in both directions that matter. A lane with no cursor at all is not over
// any bound -- it reads from the beginning of the switch-day line, which is what a first
// wait does -- and an error asking git is NOT a refusal: a wait that cannot measure the
// distance is a wait whose first poll's listing will refuse in inbox's own sentence, and
// this check exists to name a silence, never to invent a new way to fail.
func waitWalkOverBound(busDir, cursor string, limit int) (bool, error) {
	if cursor == "" || limit <= 0 {
		return false, nil
	}
	_, over, err := bus.CommitsSinceBounded(busDir, cursor, limit)
	if err != nil {
		return false, err
	}
	return over, nil
}

// maxIdleExit is the highest code --idle-exit will take. 126 and 127 are the shell's own --
// "found and not executable", "not found" -- and 128 up is a signal, so a wait that returned
// one of those would be read as something the shell did to it rather than something it said.
const maxIdleExit = 125

// defaultWaitInterval is how long a wait leaves between polls when the caller names no
// interval. It is a default, unlike --timeout, on the same test the tool's other two
// defaults pass: it is not a fact about a bus that only its owner can supply. Ten seconds
// is under the time it takes to read a note and well over the cost of a fetch, and it is
// the number chosen after watching lines wait on each other. A shorter interval is a shorter round trip between two lines
// that are answering each other, and a git fetch of a bus this size is cheap enough that
// the round trip is what the number should be chosen for.
const defaultWaitInterval = 10 * time.Second

// defaultBeatInterval and defaultBeatLease are the defaults of the retired --beat and
// --beat-lease: a wait no longer writes or pushes a BEAT, and the flags are parsed
// only so a caller's argv keeps working.
const (
	defaultBeatInterval = 60 * time.Second
	defaultBeatLease    = 10 * time.Minute
)

// maxWaitTimeout is as long as `wait` will block, and it is a fact about HARNESSES rather
// than about buses; see the refusal above.
const maxWaitTimeout = 60 * time.Minute

// minWaitInterval is as fast as a wait will poll, because a poll is a git fetch.
const minWaitInterval = 100 * time.Millisecond

// waitBlockedHook is a test-only sync point, set behind this unexported name. It
// is called with the bus directory at the exact boundary a wait becomes blocked:
// it has polled, found nothing new, and is about to sleep until its next poll.
// Tests point it at a channel close so a note can be pushed at that boundary
// instead of after a sleep that races the poll. It is an atomic because it
// is read from whichever goroutine runs waitLoop and written once by a test, and
// parallel tests may run waitLoop while a sibling's hook is installed.
type waitBlockedHook func(busDir string)

var testWaitBlockedHook atomic.Pointer[waitBlockedHook]

// waitClock is the clock waitLoop reads and sleeps on: the wall clock in production.
type waitClock interface {
	Now() time.Time
	Sleep(time.Duration)
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func (wallClock) Sleep(d time.Duration) { time.Sleep(d) }

// testWaitClock is the test-only clock seam beside testWaitBlockedHook, and it is shaped
// the same way for the same reason: one process-wide atomic, nil in production, which a
// test points at a lookup from bus directory to clock, so a parallel test drives ITS wait
// on a fake clock without touching a sibling's. A lookup that answers nil for a bus
// directory leaves that wait on the wall clock.
var testWaitClock atomic.Pointer[func(busDir string) waitClock]

// waitClockFor is the clock one wait over busDir runs on.
func waitClockFor(busDir string) waitClock {
	if f := testWaitClock.Load(); f != nil {
		if c := (*f)(busDir); c != nil {
			return c
		}
	}
	return wallClock{}
}

// waitLoop is the clock: poll, and either return what arrived or sleep and poll again
// until the deadline. It is apart from the flags so that what it does is readable without
// them.
//
// THE FIRST POLL HAPPENS IMMEDIATELY, before any sleep, because the commonest case is a
// note that is already there -- the caller answered the last one and came straight back --
// and making them wait an interval for news the bus already had would be a tool inventing
// latency.
func waitLoop(o inboxOpts, timeout, interval time.Duration, idleExit int, next string, stdout, stderr io.Writer, now time.Time, repairs *repairLog) int {
	clock := waitClockFor(o.busDir)
	start := clock.Now()
	deadline := start.Add(timeout)
	// The moment this call cannot see past: a switch-day line drawn after it hides
	// everything that could possibly arrive during this wait.
	horizon := now.Add(timeout)
	polls := 0
	cursor := ""
	// The cursor this call starts from, for the WAIT TIMEOUT line when nothing moves it.
	if held, err := bus.ReadCursor(o.busDir, o.me.Lane); err == nil {
		cursor = held.Commit
	}
	// NO BEAT IS WRITTEN OR PUSHED HERE. A tick used to rewrite from-<lane>/BEAT and every
	// --beat pushed it as its own `beat <name>` commit: most of a bus's commits, pulled by
	// every clone. Presence is friend:<name> in Redis, written
	// by each participant's own runtime; a wait leaves the checkout exactly as its polls left it.
	for {
		polls++
		elapsed := clock.Now().Sub(start).Round(time.Millisecond)
		pollNow := now.Add(elapsed)
		// A wait returns the moment it sees news, an unadvanced
		// cursor's backlog included, printing exactly what inbox prints for that state;
		// it blocks only while there is nothing new at all, until a note arrives or the
		// deadline. Blocking over the backlog instead breaks byte-identity with inbox.
		//
		// A beat or a cursor is never news. A wait that woke on every
		// line's presence beat (one a minute, six lines) was a poll with extra steps and cost the
		// window a turn per beat; --quiet-beats is accepted and changes nothing.
		keep := func(r inboxReading) bool { return r.New > 0 || hiddenWholeWait(r.Legacy, horizon) }
		// --on-note WAKES ON A NOTE ADDRESSED TO: THE CALLER AND ON NOTHING ELSE. A Cc: note, a receipt or a heard note is data, not a
		// wake: the tick that brings only those is empty, prints nothing and keeps waiting.
		if o.onNote {
			keep = func(r inboxReading) bool { return len(onNoteWakes(r)) > 0 }
		}
		code, r, lines, skipped := waitPoll(o, polls == 1, pollNow, keep, stdout, stderr, repairs)
		if r.Cursor != "" {
			cursor = r.Cursor
		}
		if code != 0 {
			// The listing this poll had already printed, if it printed one, under the
			// refusal that is on stderr: a reader who was shown their inbox has been shown
			// it, whatever happened after.
			fmt.Fprint(stdout, lines)
			fmt.Fprintf(stdout, "WAIT DONE reason=signal rearm=required next=%s\n", next)
			return code
		}
		if skipped {
			// The cursor moved over heard notes without returning, so the timeout line at
			// the end of the wait names where the reader now stands, and the clock goes on.
			fmt.Fprint(stdout, lines)
			if head, err := bus.HeadCommit(o.busDir); err == nil {
				cursor = head
			}
			continue
		}
		if keep(r) {
			if o.onNote {
				// The poll already built the --on-note frame: one WAIT OK id= line and the
				// notes. No listing, no INBOX OPEN frame, no carrying count.
				fmt.Fprint(stdout, lines)
				fmt.Fprintf(stdout, "WAIT DONE reason=new rearm=required next=%s\n", next)
				return 0
			}
			// Why this wait is not waiting, when the answer is not "a note arrived": the
			// reader's own switch-day line is drawn after everything this call could see,
			// so no note written during it would be listed. Telling them costs one line;
			// not telling them costs an hour of waiting for something that cannot happen.
			if hiddenWholeWait(r.Legacy, horizon) && !r.SwitchDay {
				fmt.Fprintf(stdout, "WAIT NOTE %s\n", oneline.Escape(hiddenReason(r.Legacy, pollNow)))
			}
			fmt.Fprintf(stdout, "WAIT OK new=%d after=%s polls=%d\n", r.New, oneline.Field(elapsed.String()), polls)
			fmt.Fprint(stdout, lines)
			fmt.Fprintf(stdout, "WAIT DONE reason=new rearm=required next=%s\n", next)
			return 0
		}
		// A line drawn in the future that does NOT cover the whole wait is no reason to
		// stop, and is still worth a sentence: the notes written before it will not be
		// listed, and a reader who did not mean to draw it there would otherwise find that
		// out by being told about none of them.
		if polls == 1 && !r.Legacy.Before.IsZero() && r.Legacy.Before.After(pollNow) && !r.SwitchDay {
			fmt.Fprintf(stdout, "WAIT NOTE %s\n", oneline.Escape(hiddenReason(r.Legacy, pollNow)))
		}
		if polls == 1 {
			if h := testWaitBlockedHook.Load(); h != nil {
				(*h)(o.busDir)
			}
		}
		left := deadline.Sub(clock.Now())
		if left <= 0 {
			break
		}
		// The last sleep is the short one, so the LAST poll lands ON the deadline rather
		// than before it: a note that arrives in the final interval is a note this call
		// saw, and stopping early would hand it to the next call for no reason.
		if left < interval {
			clock.Sleep(left)
			continue
		}
		clock.Sleep(interval)
	}
	// A TIMEOUT IS NOT AN ERROR. Nothing arrived, and nothing was written -- no cursor
	// moves on a wait that found nothing, because there is nothing to record having read --
	// and the caller's move is to issue the next wait. Exit 0, with the counts that say the
	// tool was awake the whole time.
	// ONE LINE A HARNESS CAN GREP, and -- with --idle-exit -- one code it does not have to
	// grep for at all. The code is named ON the line as well, because an exit code that
	// appears nowhere in the transcript is a number somebody reads a bug into: a harness
	// branching on 3 and a person reading the log see the same fact.
	if idleExit == 0 {
		fmt.Fprintf(stdout, "WAIT TIMEOUT after=%s polls=%d cursor=%s\n",
			oneline.Field(clock.Now().Sub(start).Round(time.Millisecond).String()), polls, oneline.Field(dash(cursor)))
	} else {
		fmt.Fprintf(stdout, "WAIT TIMEOUT after=%s polls=%d cursor=%s idle-exit=%d\n",
			oneline.Field(clock.Now().Sub(start).Round(time.Millisecond).String()), polls, oneline.Field(dash(cursor)), idleExit)
	}
	fmt.Fprintf(stdout, "WAIT DONE reason=timeout rearm=required next=%s\n", next)
	return idleExit
}

// waitPoll is ONE poll, under the checkout lock: the fetch, the listing, and -- only on the
// poll that returns -- the cursor. It hands its lines back rather than printing them,
// because a poll that found nothing prints nothing: twenty polls of a quiet bus are not
// twenty listings.
//
// The lock is taken and released here rather than around the loop; see lockCheckout.
func waitPoll(o inboxOpts, first bool, now time.Time, keep func(inboxReading) bool, stdout, stderr io.Writer, repairs *repairLog) (int, inboxReading, string, bool) {
	release, code := lockCheckout("WAIT", o.busDir, stderr)
	if code != 0 {
		repairs.flush(stdout)
		return code, inboxReading{}, "", false
	}
	defer release()
	// THE FETCH IS THE POLL. Every read in this tool reads the working tree, so a poll that
	// fetched and left the checkout where it was would never see anything. The recovery
	// around that fetch removes a stale index.lock and, when the checkout is behind,
	// discards only this wait's own BEAT if that file is what blocks the fast-forward. A
	// dirty file it does not own refuses the poll and is not touched. See
	// bus.RecoverWaitFastForward.
	rec, err := bus.RecoverWaitFastForward(o.busDir, o.remote, o.branch, waitOwnedPaths(o), time.Now())
	if rec.Scanned {
		printLockScan(stdout, rec.Scan)
	}
	if rec.LockChanged {
		fmt.Fprintf(stderr, "WAIT: %s\n", oneline.Err(bus.ErrIndexLockChanged))
	}
	if rec.LockCleared {
		repairs.add("index.lock")
	}
	// A BEAT the fast-forward had to discard is an older wait's leftover: the
	// discard is the whole repair, and nothing is written back.
	for _, p := range rec.Discarded {
		repairs.add(p)
	}
	repairs.flush(stdout)
	if err != nil {
		if first {
			// The first poll's fetch failing is the invocation being wrong -- a remote that
			// is not there, a branch nobody has, a checkout that has diverged, a dirty file
			// this wait does not own -- and the caller should hear that now rather than in
			// an hour.
			fmt.Fprintf(stderr, "WAIT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus wait -h"))
			return 1, inboxReading{}, "", false
		}
		// A later one is the network, or somebody's server, and it is not this reader's to
		// fix. It is said out loud and the wait goes on: the deadline still bounds the
		// whole thing, and a wait that gave up on one failed fetch is a wait nobody can
		// rely on.
		fmt.Fprintf(stderr, "WAIT POLL fetch: %s\n", oneline.Err(err))
	}
	var buf bytes.Buffer
	code, r := inboxListing(o, &buf, stderr, now)
	if code != 0 {
		return code, r, buf.String(), false
	}
	// A poll whose since-walk hit the bound read nothing, so it has neither news to return
	// on nor a read to advance over. `wait` refuses a cursor already past the bound before
	// it blocks (WAIT BLIND); this is the same state arriving mid-wait, when the bus
	// moves past the bound while the wait is standing there.
	if r.Bounded {
		return 0, r, "", false
	}
	if !keep(r) {
		return 0, r, "", false
	}
	// `wait --advance` skips notes already heard before it blocks. A reader who
	// receipted a note and then waits has already taken that note -- heard is not answered,
	// so the note is still news to the open list -- and a wait that returns on it pays a
	// turn for nothing. When every new note is already heard, the cursor is moved to the
	// head instead, one WAIT ADVANCED line names the move, and the wait keeps blocking for a
	// genuinely new note. Addressed-elsewhere notes never reach this listing at all, so
	// "every new note is heard" is exactly "no note between the cursor and the head owes
	// this reader an answer".
	if o.advance && !o.bodies && r.New > 0 && r.HeardNew == r.New {
		head, err := bus.HeadCommit(o.busDir)
		if err != nil {
			fmt.Fprintf(stderr, "WAIT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus wait -h"))
			return 1, r, "", false
		}
		var quiet bytes.Buffer
		if code := advanceCursor(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), o.remote, o.branch, o.attempts, o.noPush, o.noBeat, o.dryRun, now, &quiet, stderr); code != 0 {
			return code, r, "", false
		}
		skip := fmt.Sprintf("WAIT ADVANCED from=%s to=%s heard=%d\n", oneline.Field(sha8(r.Cursor)), oneline.Field(sha8(head)), r.HeardNew)
		return 0, r, skip, true
	}
	if o.advance && o.bodies && r.AdvanceTo != "" {
		code = advanceCursorTo(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), r.AdvanceTo, o.remote, o.branch, o.attempts, o.noPush, o.noBeat, o.dryRun, now, &buf, stderr)
	} else if o.advance && !o.bodies {
		code = advanceCursor(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), o.remote, o.branch, o.attempts, o.noPush, o.noBeat, o.dryRun, now, &buf, stderr)
	}
	if o.onNote && code == 0 {
		frame, err := onNoteFrame(o, onNoteWakes(r))
		if err != nil {
			fmt.Fprintf(stderr, "WAIT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus wait -h"))
			return 1, r, "", false
		}
		return 0, r, frame, false
	}
	return code, r, buf.String(), false
}

// onNoteWakes is what `wait --on-note` wakes on: the new, unheard notes addressed To: the
// reader. Cc: notes, receipts, heard notes and unreadable files are on the listing and
// are not a wake (docs/SPEC-BUS.md: "the wake being To: only").
func onNoteWakes(r inboxReading) []bus.OpenEntry {
	var wakes []bus.OpenEntry
	for _, e := range r.Fresh {
		if e.Kind == bus.OpenNote && !e.Heard && e.Addr == "to" {
			wakes = append(wakes, e)
		}
	}
	return wakes
}

// onNoteFrame is what `wait --on-note` prints when a note arrives, per docs/SPEC-BUS.md:
// exactly one status line naming the first note -- `WAIT OK id= from= path= bytes=` --
// and then each note's INBOX NOTE line and body frame, at most --max-notes of them and
// --max-bytes of body across the return; a body past the budget is left whole for the
// next wake. Nothing else: no INBOX SCOPE, OPEN or OK frame and no carrying count.
func onNoteFrame(o inboxOpts, wakes []bus.OpenEntry) (string, error) {
	var b bytes.Buffer
	budget := o.maxBytes
	for i, e := range wakes {
		if i >= o.maxNotes {
			break
		}
		text, err := os.ReadFile(filepath.Join(o.busDir, filepath.FromSlash(e.Path)))
		if err != nil {
			return "", err
		}
		n, err := bus.ParseNote(e.Path, string(text))
		if err != nil {
			return "", err
		}
		body := n.Body
		if i == 0 {
			fmt.Fprintf(&b, "WAIT OK id=%s from=%s path=%s bytes=%d\n",
				oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), oneline.Field(e.Path), len(body))
		}
		if int64(len(body)) > budget && i > 0 {
			break
		}
		if int64(len(body)) > budget {
			if _, err := printOpenEntries(&b, []bus.OpenEntry{e}, 1); err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "INBOX BODY OVERSIZE id=%s bytes=%d max-bytes=%d path=%s\n",
				oneline.Field(dash(e.ID)), len(body), o.maxBytes, oneline.Field(e.Path))
			break
		}
		budget -= int64(len(body))
		// The INBOX NOTE line and the body frame through the one audited printer the
		// --bodies page uses, so the verbatim body has one writer in this file.
		if err := printBodyItem(&b, bus.BodyItem{Path: e.Path, Entry: e, Body: []byte(body)}); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}
