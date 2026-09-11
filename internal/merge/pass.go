package merge

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Work list 8: ONE PASS.
//
// The base's evidence first and a red base stops (rule 6); walk the order; classify each
// entry into the closed state set; re-merge only what conflicts (rule 3); build the
// integration commit for an admitted entry (rule 18); evaluate MERGE(entry) as one
// function that is the only caller of the publication helper (rule 21); merge at most
// one, then stop, because the base has moved and every entry behind it must be read
// again.

// Pass is everything one pass needs. It is a struct rather than nine arguments because
// dry-run runs the same reads with Survey set and no path from here to a mutation.
type Pass struct {
	Lane       string
	State      *State
	Host       Host
	Clone      *Git // the lane's own clone, <lane>/repo
	Records    *Records
	Remote     string
	Max        int
	PlannedRed string
	Build      string
	Now        time.Time
	Stdout     io.Writer
	Stderr     io.Writer
	// Survey is dry-run: every read this pass performs, the whole plan printed rather
	// than stopping at the first merge, and no path to the mutating helper at all.
	Survey bool
	// Problems is the fold's refusals, which block their entries and are never skipped.
	Problems []FoldProblem
	Pulled   int
	LaneTip  string

	// basePending is set by baseLine: the base has no evidence for its head, which is
	// every base below main right after a merge. A pending base WAITS -- nothing merges
	// onto it -- and RUN NOTE names the gate run that proves it.
	basePending bool
}

// Result is what a pass did, for the exit code and the last line.
type Result struct {
	Merged  int
	Dropped int
	Blocked int
	Waiting int
	Stopped bool // something was refused, stopped or blocked: exit 1
	// Refused is THE TOOL COULD NOT LOOK -- the host or the remote did not answer -- and
	// it is exit 2, not 1. Exit 1 is "the tool ran and said no", which is a different
	// piece of news from "the tool could not find out", and a caller that cannot tell
	// them apart cannot decide whether to retry.
	Refused bool
	Note    string
	Plan    []string
}

// Exit is this repo's grammar over a pass: 0 the pass completed with nothing refused, 1
// something was refused, stopped or blocked.
//
// AN ENTRY THAT IS MERELY WAITING IS NOT A FAILURE. Zero pending checks is not the same
// news as a red check, and a lane full of entries waiting on CI is a lane working exactly
// as intended -- the prototype exited 1 on every pass while CI ran, and its caller
// stopped reading the exit code.
func (r *Result) Exit() int {
	switch {
	case r.Refused:
		return 2
	case r.Stopped:
		return 1
	}
	return 0
}

// Run is the pass. It prints the grammar and returns what happened.
func (p *Pass) Run(n int) *Result {
	res := &Result{}
	if p.Survey {
		return p.survey(res)
	}
	fmt.Fprintf(p.Stdout, "RUN PASS n=%d at=%s build=%s pulled=%d planned_red=%s\n",
		n, oneline.Field(p.Now.UTC().Format(Stamp)), oneline.Field(p.Build), p.Pulled,
		oneline.Field(dashIfEmpty(p.PlannedRed)))

	// THE FOLD REFUSES, IT NEVER SKIPS. An unreadable record file is preserved untouched
	// and said out loud, because the file nobody could read may be the hold or the newer
	// red -- a malformed HOLD beside valid approvals once vanished from the decision, and
	// a printed NOTE does not make that safe.
	for _, pr := range p.Problems {
		fmt.Fprintf(p.Stderr, "FOLD REFUSED file=%s: %s\n",
			oneline.Field(pr.File), oneline.Escape(oneline.Cap(pr.Reason, oneline.TailBytes)))
	}
	// A record file whose path names no entry stops the pass before any entry is read:
	// the scope is indeterminate, so there is no entry to block instead.
	for _, pr := range p.Problems {
		if pr.Entry == "" {
			fmt.Fprintf(p.Stderr, "RUN STOPPED reason=malformed_record file=%s: %s; re-record it with the verb that wrote it\n",
				oneline.Field(pr.File), oneline.Escape(oneline.Cap(pr.Reason, oneline.TailBytes)))
			res.Stopped = true
			res.Note = "nova-merge status --lane " + p.Lane
			p.note(res)
			return res
		}
	}

	base, ok := p.baseLine(res)
	if !ok {
		p.note(res)
		return res
	}
	p.walk(base, res)
	p.note(res)
	return res
}

// baseLine is RUN BASE, the second line of every pass: the base's own evidence, read the
// same way an entry's is. A RED base stops the pass before any entry is read -- an
// entry's checks are evidence about the entry merged with the base the host computed at
// check time, so if the base is red that merge is red too, whatever the entry's own
// checks say (#922, 2026-09-11).
func (p *Pass) baseLine(res *Result) (baseSHA string, ok bool) {
	baseSHA, err := p.BaseSHA()
	if err != nil {
		fmt.Fprintf(p.Stderr, "RUN REFUSED: the lane's base %s could not be read from %s: %s\n",
			oneline.Field(p.State.Base), oneline.Field(p.Remote), oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		res.Stopped, res.Refused = true, true
		res.Note = fmt.Sprintf("nova-merge status --lane %s  # the remote did not answer for %s; this pass looked at nothing", p.Lane, p.State.Base)
		return "", false
	}
	checks, err := p.Host.Checks(baseSHA)
	if err != nil {
		fmt.Fprintf(p.Stderr, "RUN REFUSED: the base's checks could not be read: %s\n",
			oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		res.Stopped, res.Refused = true, true
		res.Note = fmt.Sprintf("nova-merge status --lane %s  # the host did not answer for the base; this pass looked at nothing", p.Lane)
		return "", false
	}
	gate := "-"
	if p.baseGateGreen(baseSHA) {
		gate = "green"
	}
	state := checks.Verdict()
	if gate == "green" && state != "RED" {
		state = "GREEN"
	}
	if p.PlannedRed != "" && state == "RED" {
		state = "PLANNED-RED"
	}
	fmt.Fprintf(p.Stdout, "RUN BASE base=%s head=%s checks=%s gate=%s state=%s\n",
		oneline.Field(p.State.Base), oneline.Field(Short(baseSHA)), checks.Field(), gate, state)
	if state == "RED" {
		fmt.Fprintf(p.Stderr, "RUN STOPPED base=%s: the base is red (%d failing); nothing merges onto a red base\n",
			oneline.Field(p.State.Base), checks.Red)
		res.Stopped = true
		res.Note = "nova-merge status --lane " + p.Lane + "  # the base is red: read its failing job before anything merges onto it"
		return baseSHA, false
	}
	if state == "PENDING" {
		// A REMEDY IS A COMMAND. This one had no verb and named a --from flag that
		// exists on nothing.
		res.Note = fmt.Sprintf("nova-merge gate --lane %s --branch %s --head %s --base-sha %s --merge %s --verdict green --summary <path>  # the base %s has no evidence for %s; a base gate is its three shas equal",
			p.Lane, p.State.Base, baseSHA, baseSHA, baseSHA, p.State.Base, Short(baseSHA))
	}
	p.basePending = state == "PENDING"
	return baseSHA, true
}

// baseGateGreen is RUN BASE's gate=green: A BASE GATE, told from an integration gate by
// its shas. The base merged onto itself is itself, so the record's three shas are all
// the base's own, the runner verifies the fetched object's sha and applies NO parent
// check -- no commit is its own parent -- and a base gate never satisfies an entry's
// predicate, because rule 18 asks for (oid, base sha) and oid is never the base.
func (p *Pass) baseGateGreen(baseSHA string) bool {
	for _, g := range p.State.Gates {
		if g.Branch == p.State.Base && g.Head == baseSHA && g.Base == baseSHA && g.Merge == baseSHA && g.Verdict == "green" {
			return true
		}
	}
	return false
}

// BaseSHA reads the base back from the remote every pass. A base read once is a base that
// can change afterwards, and a pull request whose base was not the lane's base was merged
// into the wrong branch that way.
func (p *Pass) BaseSHA() (string, error) {
	if _, err := p.Clone.Run("fetch", p.Remote, p.State.Base); err != nil {
		return "", err
	}
	return p.Clone.Out("rev-parse", "FETCH_HEAD")
}

// walk is the ordered lane: classify, print, and merge AT MOST ONE.
func (p *Pass) walk(baseSHA string, res *Result) {
	entries := p.State.Entries()
	list := bounded.Capped(p.Stdout, p.Max, "RUN", "entry",
		fmt.Sprintf("nova-merge status --lane %s --max 0", p.Lane))
	merged := false
	for _, e := range entries {
		if merged {
			// The base moved. Every entry behind it must be read again, and a pass that
			// merged two would be merging the second against a base no check had seen.
			list.Line(fmt.Sprintf("RUN ENTRY entry=%s head=%s checks=g0/p0/r0 read=0a/0h gate=- state=%s",
				oneline.Field(e.ID()), oneline.Field(dashIfEmpty(Short(e.OID))), StatePending))
			res.Waiting++
			continue
		}
		st := p.classify(e, baseSHA, res)
		list.Line(fmt.Sprintf("RUN ENTRY entry=%s head=%s checks=%s read=%s gate=%s state=%s",
			oneline.Field(e.ID()), oneline.Field(dashIfEmpty(Short(e.OID))), st.Checks.Field(),
			st.Reads.Field(), dashIfEmpty(st.Gate.Kind), st.State))
		// ONE PLACE COUNTS. A blocked entry is one entry, however many sites in the code
		// noticed it: the count on RUN OK is the truth about the LANE, never about the
		// number of times the code said so. It was two per blocked entry when the sites
		// that produce the state counted as well as this switch, and the fold's own
		// refusal -- which counted nowhere -- was zero.
		switch st.State {
		case StateMergeableGreen:
			if p.merge(e, st, baseSHA, res) {
				merged = true
				res.Merged++
				continue
			}
			// A PUBLICATION THAT WAS REFUSED STOPS THE PASS. Rule 21: "nothing was
			// published, the unpublished object is named, the pass exits 1 and stops",
			// and "a moved base is a refusal, not a report ... the pass stops there".
			// The walk used to fall through to the next entry, so a race printed its
			// refusal and then went on classifying, building and refusing entries
			// against a base the tool had just been told it does not know -- and the one
			// RUN NOTE ended up being the next entry's gate command rather than the
			// race's remedy.
			list.More()
			p.okLine(len(entries), res)
			return
		case StateBlocked:
			res.Blocked++
			res.Stopped = true
		case StateRed, StateHold, StateWrongBase, StateFork:
			res.Stopped = true
		default:
			res.Waiting++
		}
	}
	list.More()
	p.okLine(len(entries), res)
}

// okLine is RUN OK, and it has ONE statement so that the pass that stops and the pass that
// finishes cannot drift into printing different counts of the same lane.
func (p *Pass) okLine(lane int, res *Result) {
	fmt.Fprintf(p.Stdout, "RUN OK lane=%d merged=%d dropped=%d blocked=%d waiting=%d\n",
		lane, res.Merged, res.Dropped, res.Blocked, res.Waiting)
}

// note is RUN NOTE: EXACTLY ONE remedy line per pass, and it names the next command. Not
// a paragraph, not one per entry. A cap with no remedy is censorship; a cap with one is
// an index.
func (p *Pass) note(res *Result) {
	line := res.Note
	if line == "" {
		if res.Stopped {
			line = fmt.Sprintf("nova-merge status --lane %s  # a RED entry needs its failing job read; a BLOCKED one needs a hand", p.Lane)
		} else {
			line = fmt.Sprintf("nova-merge run --lane %s --loop 5m --hours 2  # nothing is blocked; this keeps watching", p.Lane)
		}
	}
	fmt.Fprintf(p.Stdout, "RUN NOTE %s\n", oneline.Escape(oneline.Cap(line, oneline.TailBytes)))
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
