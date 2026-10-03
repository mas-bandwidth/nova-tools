// check: the bus validated -- headers, ids, threads, receipts, lanes -- the whole of it or
// what changed since a cursor, with the listing capped and the counting never.

package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// defaultCheckMax is how many findings `check` prints before one BUS MORE line names the
// rest. It is the Conventions' cap: a check over a bus adopted onto an old history failed
// 1,059 times, most of them one class of finding repeating, and a wall of red that
// large is a wall nobody reads -- the loud kind eats the quiet one and the one finding that
// matters is somewhere in it. Twenty findings is a screen; the BUS CHECK line that follows
// counts every finding by class, so the listing is capped and the counting never is.
const defaultCheckMax = 20

func cmdCheck(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("check")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	full := f.fs.Bool("full", false, "walk the whole bus: what CI on main and a first adoption run want")
	as := f.fs.String("as", "", "check what changed since this participant's cursor")
	since := f.fs.String("since", "", "check what changed since this commit: a revision, a UTC date (YYYY-MM-DD, so the last commit written before that day), or an RFC 3339 UTC instant")
	legacyBefore := f.fs.String("legacy-before", "", "a finding about the header of a note dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339, e.g. 2026-09-09T18:07:00Z) warns instead of failing")
	rebuildIndex := f.fs.Bool("rebuild-index", false, "with --full, rewrite each lane's INDEX from the notes on disk")
	dryRun := f.fs.Bool("dry-run", false, "with --rebuild-index, print each lane's count and write nothing")
	maxFindings := f.fs.Int("max", defaultCheckMax, "findings of each class to print before one BUS MORE line per class names the rest of it (default 20, 0 = all)")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir}) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.atLeastZero("max", *maxFindings, stderr) {
		return 2
	}
	// A check with no baseline is not a check of nothing, it is a caller who has not said
	// what they want checked. There is no default here for the same reason there is no
	// default bus.
	if !*full && strings.TrimSpace(*as) == "" && strings.TrimSpace(*since) == "" {
		fmt.Fprint(stderr, "CHECK REFUSED: give one of --full, --as <name> or --since <commit>; refusing to guess; run: nova-bus check -h\n")
		return 2
	}
	if *rebuildIndex && !*full {
		fmt.Fprint(stderr, "CHECK REFUSED: --rebuild-index writes each lane's INDEX from every note in it, so it needs --full; run: nova-bus check -h\n")
		return 2
	}
	line, ok := legacyLine("check", *legacyBefore, stderr)
	if !ok {
		return 2
	}
	opts := bus.CheckOptions{LegacyBefore: line.Before}
	// The root check comes BEFORE the roster: a --bus pointing at a subdirectory of a
	// bigger repository refused with "participants.json: no such file", which is true and
	// is not the caller's mistake. See the same reordering in cmdInbox.
	if !*full {
		if err := bus.IsRepoRoot(*busDir); err != nil {
			fmt.Fprintf(stderr, "CHECK REFUSED: checking only what changed needs git; %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus check -h"))
			return 2
		}
	}
	if *rebuildIndex && !*dryRun {
		release, lockErr := bus.LockCheckout(*busDir, checkoutLockWait)
		if lockErr != nil {
			fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(lockErr), "nova-bus check -h"))
			return 1
		}
		defer release()
	}
	c, err := bus.LoadConfig(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "CHECK REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus check -h"))
		return 2
	}
	scope := bus.Scope{Full: *full}
	from := ""
	// The lane whose CURSOR this run read, and the switch-day line it found there, kept for
	// the note printed below the scope line. They are "" for every other way of asking.
	noteName, noteLegacy := "", ""
	if !*full {
		if strings.TrimSpace(*since) != "" {
			from, err = bus.ResolveSinceCommit(*busDir, *since)
			if err != nil {
				fmt.Fprintf(stderr, "CHECK REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus check -h"))
				return 2
			}
		} else {
			me, ok := c.Lookup(*as)
			if !ok {
				fmt.Fprintf(stderr, "CHECK REFUSED: --as %q names no one on this bus (known: %s); run: nova-bus check -h\n", *as, oneline.Escape(strings.Join(c.KnownNames(), "; ")))
				return 2
			}
			if me.Lane == "" {
				fmt.Fprintf(stderr, "CHECK REFUSED: %q has no lane on this bus, so has no cursor; run: nova-bus check -h\n", me.Name)
				return 2
			}
			cursor, cerr := bus.ReadCursor(*busDir, me.Lane)
			if cerr != nil {
				fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(cerr), "nova-bus check -h"))
				return 1
			}
			// A reader with no cursor yet has no baseline, so this run is a full one. It
			// is the same adoption path inbox takes, and it costs one full check once.
			scope.Full, from = cursor.Commit == "", cursor.Commit
			noteName, noteLegacy = me.Name, cursor.Legacy
		}
	}
	if from != "" {
		ok, aerr := bus.IsAncestor(*busDir, from)
		if aerr != nil {
			fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(aerr), "nova-bus check -h"))
			return 1
		}
		if !ok {
			fmt.Fprintf(stderr, "BUS REFUSED: %s is not an ancestor of HEAD, so a diff from it would name changes that are not changes; run check --full; run: nova-bus check -h\n", oneline.Field(from))
			return 1
		}
	}

	var problems []bus.Problem
	var stats bus.CheckStats
	if scope.Full {
		t, terr := bus.ReadBus(*busDir, c)
		if terr != nil {
			fmt.Fprintf(stderr, "CHECK REFUSED: %s\n", oneline.WithRemedy(oneline.Err(terr), "nova-bus check -h"))
			return 2
		}
		if *rebuildIndex {
			for _, lane := range c.Lanes() {
				if *dryRun {
					n, _ := bus.PlanLaneIndex(c, t, lane)
					fmt.Fprintf(stdout, "BUS INDEX lane=%s notes=%d dry_run=true\n", oneline.Field(lane), n)
					continue
				}
				n, rerr := bus.RebuildLaneIndex(*busDir, c, t, lane)
				if rerr != nil {
					fmt.Fprintf(stderr, "BUS FAIL %s: %s\n", oneline.Escape(bus.IndexPath(lane)), oneline.Err(rerr))
					return 1
				}
				fmt.Fprintf(stdout, "BUS INDEX lane=%s notes=%d\n", oneline.Field(lane), n)
			}
		}
		idx, ierr := bus.ReadIndex(*busDir, c)
		if ierr != nil {
			fmt.Fprintf(stderr, "CHECK REFUSED: %s\n", oneline.WithRemedy(oneline.Err(ierr), "nova-bus check -h"))
			return 2
		}
		problems = append(t.CheckWith(opts), bus.CheckIndex(c, t, idx)...)
		stats = bus.CheckStats{Notes: len(t.Notes), Lanes: len(c.Lanes()), Receipts: len(t.Receipts)}
	} else {
		changed, derr := bus.ChangedSince(*busDir, from)
		if derr != nil {
			fmt.Fprintf(stderr, "BUS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(derr), "nova-bus check -h"))
			return 1
		}
		idx, ierr := bus.ReadIndex(*busDir, c)
		if ierr != nil {
			fmt.Fprintf(stderr, "CHECK REFUSED: %s\n", oneline.WithRemedy(oneline.Err(ierr), "nova-bus check -h"))
			return 2
		}
		scope.From, scope.Changed = from, len(changed)
		problems, stats = bus.CheckSince(*busDir, c, idx, changed, opts)
	}
	fmt.Fprintf(stdout, "BUS SCOPE mode=%s cursor=%s changed=%d\n",
		oneline.Field(scope.Mode()), oneline.Field(dash(from)), scope.Changed)
	// THE SAME SENTENCE, WHEREVER THE CURSOR IS READ. `check --as <you>` reads a lane's
	// CURSOR for its commit, so it sees the drawn-forward line as plainly as `inbox` does,
	// and a reader polling `check` and reading nothing is in exactly the trouble this note
	// exists for. It is printed under `INBOX SWITCH` here rather than under a `BUS` token of
	// its own -- the one place this verb speaks in another verb's grammar, and on purpose:
	// it is a fact about an INBOX cursor, it names an `inbox` command, and one grep finds
	// it wherever it was met. The values `check` was never given are the placeholders they
	// are; see printSwitchDayNote.
	printSwitchDayNote(stdout, noteLegacy, *busDir, noteName, "<n>", "", "", now)
	// THE CAP, AND THE COUNT THAT IS NEVER CAPPED. A check that fails 1,059 times
	// printed all 1,059 lines, most of them one class repeating, and a reader holding the
	// wall could not tell the loud kind from the one finding that mattered. So the report
	// is capped at --max findings PER CLASS (docs/SPEC.md, the common cap rule: where one
	// verb runs several checks into one stream, the loud kind must not eat the quiet one),
	// one BUS MORE line per class says what the cap held back of it and names the flag that
	// lifts it, and one BUS CHECK line counts every finding by class before
	// the exit -- the listing is capped, the counting never is, and the class the cap ate
	// is still on the line. The cap governs what is PRINTED and nothing else: the exit
	// code and the counts come from the whole walk, exactly as an uncapped run said them.
	counts := bus.CountCheckFindings(problems)
	printed := map[string]int{}
	for _, p := range problems {
		class := bus.ProblemClass(p)
		if *maxFindings > 0 && printed[class] >= *maxFindings {
			continue
		}
		printed[class]++
		// A WARN GOES TO STDOUT, and it went to stderr. The grammar says which stream a
		// line is on and the rule is one sentence: FAIL lines and refusals to stderr,
		// everything else to stdout. A WARN is neither -- it is a finding that was
		// TOLERATED, reported by a run that passed -- so putting it on stderr made every
		// clean-but-forgiving run look like a failing one to anything reading the streams
		// apart, which is what CI does. It is an informational line and it is now where the
		// informational lines are.
		if p.Warn {
			fmt.Fprintf(stdout, "BUS WARN %s: %s\n", oneline.Escape(p.Where), oneline.Escape(p.Reason))
			continue
		}
		fmt.Fprintf(stderr, "BUS FAIL %s: %s\n", oneline.Escape(p.Where), oneline.Escape(p.Reason))
	}
	for _, cc := range counts.Class {
		if printed[cc.Class] < cc.Count {
			fmt.Fprintf(stderr, "BUS MORE kind=%s shown=%d total=%d remedy=%s\n", oneline.Field(cc.Class), printed[cc.Class], cc.Count, oneline.Quote("--max 0"))
		}
	}
	// THE CAP'S OWN LINES GO WHERE THE FAIL LINES GO, and no further than they do. The
	// findings report's gate half is the BUS FAIL lines on stderr, and the two lines that
	// account for it -- what the cap held back, and the count by class -- end that same
	// report on that same stream: a count a caller could read as a pass never enters
	// stdout of a failing run, which is the law TestCheckFailsAndNamesEveryFinding keeps,
	// and a run with NO findings prints neither line, so a clean run's stderr stays empty
	// and the stream contract is where it was.
	if len(problems) > 0 {
		fmt.Fprintf(stderr, "BUS CHECK findings=%d fail=%d warn=%d", counts.Findings, counts.Fail, counts.Warn)
		for _, cc := range counts.Class {
			fmt.Fprintf(stderr, " %s=%d", oneline.Escape(cc.Class), cc.Count)
		}
		fmt.Fprint(stderr, "\n")
	}
	if counts.Fail > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "BUS OK notes=%d lanes=%d receipts=%d participants=%d warn=%d\n",
		stats.Notes, stats.Lanes, stats.Receipts, len(c.Participants), counts.Warn)
	return 0
}
