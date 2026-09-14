package wake

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// THE BOUNDED READ -- one primitive, and SPEC-WAKE.md owns the definition
// (draft 8; the gap kinds redrawn draft 9, K4a-c).
//
// A read-only walk of a bus lane from a floor commit forward, taking note
// HEADERS and appended RECEIPTS lines and never a body, under five bounds, with
// all-or-none item handling, a fixed within-commit order, a resumable bookmark,
// retained coverage gaps -- of which the fixed-header-cap kind is permanent at
// every budget -- and `complete` only on the tip with zero gaps, withheld from a
// poll that made no progress only while uncovered items or gaps remain.
//
// WHY THE LANE AND NOT THE INBOX (draft 7, K3). A plain `nova-bus inbox` lists a
// note only while the caller's own cursor stands behind it, so an answer's
// visibility depended on where that cursor happened to stand -- a window reading
// its own mail with --advance between two probes moved the reply onto its OPEN
// list, and the next probe read UNAVAILABLE of a line that had answered. And a
// plain inbox prints every NEW note IN FULL, so its size was never this tool's
// to bound: the only way to cap it from here would be to ask for a friend's mail
// and truncate it, which is a capture and a cut and not a bound. The lane read
// has no cursor in it, reads headers and not bodies, and its size is this tool's
// own to bound BEFORE the bytes are read rather than after.
//
// THE HEADER THIS READ PARSES IS THE BUS'S HEADER, UNDER THE BUS'S OWN
// SEMANTICS. This tool writes no second header grammar and no second identity
// rule: internal/bus's ParseNoteBodyStream and Config are what decide whether a
// note addresses the caller, so `addressed to the caller` cannot mean one thing
// in nova-bus inbox and another here.
//
// NOTHING THIS READ COULD NOT DO IS EVER `complete`. A rev-list that failed, a
// commit whose listing failed, a RECEIPTS diff that failed: each is a gap or a
// partial read and never an empty lane, because an empty lane is COMPLETE
// NEGATIVE EVIDENCE about a friend (draft 9, K4c) and a failed read is no
// evidence at all.

const (
	// HeaderCap is the depth one header is read to looking for the blank line
	// that ends it. IT IS A FIXED CAP, NOT A FLAG AND NOT A SHARE OF
	// --correlate-bytes (draft 9, K4a): it does not move when the byte budget
	// moves and no option in this spec raises it, so a header that does not end
	// inside it is unreadable by this probe AT EVERY BUDGET.
	HeaderCap = 4 << 10
	// DefaultCorrelateMax is --correlate-max's default: the bound State already
	// uses for its sighting memory.
	DefaultCorrelateMax = 300
	// DefaultCorrelateBytes is --correlate-bytes's default.
	DefaultCorrelateBytes = 262144
	// GitWall is the wall clock on ONE git process this read starts. The
	// two-minute law applied to a read the same way --gh-timeout applies it to
	// the forge -- and it is a real deadline on a real process: the context
	// kills it, and the pipe it writes to carries a read deadline of its own, so
	// a wedged git cannot hold a poll open with a child still running.
	GitWall = 10 * time.Second
	// WholeReadCap is the ceiling on the whole read: --interval or 30s,
	// whichever is smaller, and 30s where no --interval was given.
	WholeReadCap = 30 * time.Second
	// GapCap bounds the retained gap list: a bound on a read that becomes an
	// unbounded bound on the state is not a bound.
	GapCap = 64
	// ReceiptsFile is the lane's append-only receipt file (SPEC.md, nova-bus --
	// The receipt rule).
	ReceiptsFile = "RECEIPTS"
)

// The gap kinds. WHICH REMEDY APPLIES DEPENDS ON WHICH KIND IT IS, AND THE LINE
// NAMES IT: a caller must never be told to raise a budget against a cap that is
// not a budget, and must never be told a cap is fixed where a raise would in
// fact read the item.
const (
	// GapHeaderCap: no blank line ends this header inside the fixed 4 KiB. It
	// is PERMANENT AT EVERY BUDGET and no --correlate-bytes raise clears it.
	GapHeaderCap = "header-cap"
	// GapHeader: the header ended inside the cap and the bus's own semantics
	// could not parse it. No budget change clears that either; the fix is where
	// the note was written.
	GapHeader = "header"
	// GapBudget: the item's whole cost is larger than the whole of
	// --correlate-bytes, so no poll AT THIS BUDGET can take it. It stands only
	// while the budget stands.
	GapBudget = "budget"
	// GapObject: an object this read could not get from git at all. Restoring
	// it resolves the gap and no budget change does.
	GapObject = "object"
	// GapCommit: a commit whose own listing could not be read, so the items it
	// added are unknown rather than absent.
	GapCommit = "commit"
)

// permanentGap answers whether a poll can ever resolve this kind by trying
// again with what it has. A permanent one is retained, named once and NEVER
// RETRIED: retrying it would spend the item budget a lane still uncovered
// needs, and with --correlate-max 1 it would spend all of it, for ever.
func permanentGap(kind string) bool { return kind == GapHeaderCap || kind == GapHeader }

// LaneRead is one poll's bounded read.
type LaneRead struct {
	Dir      string // the bus checkout
	Ref      string // the ref read to: <remote>/<branch> where this call fetched, HEAD where it did not
	Lane     string // from-<slug>
	Anchor   string // the ping's anchor: the correlation range's FLOOR and nothing else
	Caller   string // the name an answer must be addressed to
	PingID   string // the ping's own id, which a RECEIPTS line may name
	MaxItems int
	MaxBytes int
	Config   *bus.Config
	// Wall is the deadline on one git process, and Whole the deadline on the
	// whole read. Both are bounds the spec names among its five.
	Wall  time.Duration
	Whole time.Duration

	objects    *objectReader
	clockGapRecorded bool
	// streams are the receipt diffs this poll opened. They are CLOSED WHEN THE
	// POLL ENDS, whether it ended at its budget, at its answer or at its clock:
	// a poll that returned with a git process still writing into a pipe nobody
	// reads is the orphaned shell of 2026-09-09 wearing this tool's name.
	streams []*receiptStream
	// taken, decoded and peak are what this poll actually spent, so a test can
	// assert the bounds rather than trust them.
	taken, decoded, peak int
}

// LaneResult is what one poll of the bounded read learned.
type LaneResult struct {
	Answered  bool
	AnswerID  string // the ANSWERING note's own id, not the ping's
	AnswerVia string // "Re line" or "receipt", where the correlation is exact

	Complete  bool
	Remaining string // items between the bookmark and the tip, or "-" past the counting allowance
	Gaps      int
	NewGaps   []string // the gaps first recorded this poll: named ONCE, here
	Reasons   map[string]string
	Bookmark  string // the seventh field: <commit>:<item>[;<gap>,...]
	Progress  bool
	Notes     []string

	// What this poll spent, for the assertions the spec's test 19 demands.
	Items   int
	Bytes   int
	Peak    int
	Elapsed time.Duration

	// answeredAtTip records whether the answering item was the last of the
	// range, so an ANSWERED poll says what it MEASURED rather than asserting
	// complete on every answer (the Fable read, F7).
	answeredAtTip bool
}

// position is a bookmark: the item the next poll attempts first.
type position struct {
	commit string
	item   int
}

func (p position) at() string { return p.commit + ":" + strconv.Itoa(p.item) }

// gap is one retained coverage gap: where it is and what made it one.
type gap struct {
	at   string
	kind string
}

// laneItem is one thing the read may take: a note header or an appended
// RECEIPTS line.
type laneItem struct {
	commit  string
	index   int
	path    string // "" for a receipt line
	blob    string // the object this read asks for BY SHA, never by path
	size    int    // the blob's size, which is what taking it consumes
	receipt string // the appended line, for a receipt item
}

func (i laneItem) at() string { return i.commit + ":" + strconv.Itoa(i.index) }

// cost is what taking this item spends of the byte budget: the WHOLE BLOB for a
// note, because that is what the object reader hands back to this process, and
// the line's own length for a receipt. It is asked BEFORE each item, so no item
// is ever half-read and no bookmark ever points inside one.
//
// The DECODED half is bounded separately and more tightly: at most HeaderCap
// bytes are parsed, and the read stops at the blank line that ends the header,
// so no note body is ever opened however large the blob is.
func (i laneItem) cost() int {
	if i.path == "" {
		return len(i.receipt) + 1
	}
	return i.size
}

// Run performs one poll's read, resuming from the bookmark the record carries.
func (r *LaneRead) Run(ctx context.Context, bookmark string) LaneResult {
	started := time.Now()
	res := LaneResult{Remaining: "-", Reasons: map[string]string{}}
	if r.MaxItems <= 0 {
		r.MaxItems = DefaultCorrelateMax
	}
	if r.MaxBytes <= 0 {
		r.MaxBytes = DefaultCorrelateBytes
	}
	if r.Wall <= 0 {
		r.Wall = GitWall
	}
	if r.Whole <= 0 || r.Whole > WholeReadCap {
		r.Whole = WholeReadCap
	}
	r.taken, r.decoded, r.peak = 0, 0, 0
	r.streams = nil
	defer func() {
		for _, s := range r.streams {
			s.close()
		}
		r.streams = nil
	}()
	// THE WHOLE READ'S OWN DEADLINE, the fourth of the five bounds: --interval
	// or 30s, whichever is smaller. Every git process this read starts is a
	// child of it, so a poll that runs out of time leaves nothing running.
	ctx, cancelWhole := context.WithTimeout(ctx, r.Whole)
	defer cancelWhole()

	if reader, err := r.reader(ctx); err == nil {
		r.objects = reader
		defer func() {
			reader.close()
			r.objects = nil
		}()
	}
	defer func() {
		res.Items, res.Bytes, res.Peak = r.taken, r.decoded, r.peak
		res.Elapsed = time.Since(started)
	}()

	from, gaps, resumed := parseBookmark(bookmark)
	// Where the recorded position is no longer an ancestor of the ref -- a bus
	// whose history was rewritten -- the read restarts from the anchor and the
	// retained gaps are dropped with the bookmark that named them, because
	// those positions no longer name anything and a gap nobody can return to is
	// not a gap but a claim.
	if resumed && !r.ancestor(ctx, from.commit) {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"the recorded position %s is no longer on this lane; the read restarts from the anchor and its gaps are dropped", from.commit))
		from, gaps, resumed = position{}, nil, false
	}
	if !r.ancestor(ctx, r.Anchor) {
		// Where the ANCHOR is not an ancestor either, no range can be formed at
		// all, and that is partial -- never an UNAVAILABLE by inference.
		res.Notes = append(res.Notes, fmt.Sprintf(
			"the ping's anchor %s is not on this lane, so no correlation range can be formed", r.Anchor))
		res.Bookmark, res.Gaps = renderBookmark(from, gaps), len(gaps)
		return res
	}

	floor, includeFloor := r.Anchor, false
	if resumed {
		floor, includeFloor = from.commit, true
	}
	commits, more, walkErr := r.walk(ctx, floor, includeFloor)
	if walkErr != nil {
		// A FAILED READ IS NOT AN EMPTY LANE. Answering `complete remaining=0
		// gaps=0` here would be exactly the complete negative evidence K4c
		// reserves for a lane this read covered, handed out for a lane it never
		// reached -- and the next poll would retire the ping on it.
		res.Notes = append(res.Notes, fmt.Sprintf(
			"the lane's commits could not be listed (%s); the correlation is partial and no unavailability is declared",
			oneLineOf(walkErr.Error())))
		res.Bookmark, res.Gaps = renderBookmark(from, gaps), len(gaps)
		return res
	}

	budgetItems, budgetBytes := r.MaxItems, r.MaxBytes
	carried := append([]gap(nil), gaps...)
	cur, stoppedEarly := from, false

	// PHASE ONE: the gaps a raise resolves. A budget gap is the one kind a
	// caller can clear by asking for more, so it is attempted first -- and
	// K4b's raise reaches the answer on the poll after the raise rather than
	// after the lane is walked again.
	answered := r.retry(ctx, &res, &gaps, carried, GapBudget, &budgetItems, &budgetBytes)
	if !answered {
		// PHASE TWO: the uncovered lane, from the bookmark forward.
		var stop bool
		answered, stop, cur = r.walkItems(ctx, &res, &gaps, commits, from, resumed, &budgetItems, &budgetBytes)
		stoppedEarly = stop
	}
	if !answered && !stoppedEarly && !more {
		// PHASE THREE: the gaps only the world resolves -- a restored object, a
		// commit whose listing works again. They are retried LAST and only with
		// nothing uncovered left, so a permanent-looking gap can never starve
		// the lane's own items; a permanent one is never retried at all.
		answered = r.retry(ctx, &res, &gaps, carried, GapObject, &budgetItems, &budgetBytes) ||
			r.retry(ctx, &res, &gaps, carried, GapCommit, &budgetItems, &budgetBytes)
	}

	res.Bookmark, res.Gaps = renderBookmark(cur, gaps), len(gaps)
	// COMPLETE IS THE TIP *AND* ZERO GAPS, both halves (draft 8, K3a):
	// reaching the tip is not covering the lane.
	res.Complete = !stoppedEarly && !more && len(gaps) == 0 && !res.Answered
	if res.Answered {
		// An answer stops the read where it stands, so what is complete is what
		// was measured: the answer was the last item and nothing stands behind
		// it, or the read is partial and says so (F7).
		res.Complete = res.answeredAtTip && len(gaps) == 0 && !more
	}
	if res.Complete {
		res.Remaining = "0"
	} else {
		res.Remaining = r.remaining(ctx, cur)
	}
	return res
}

// setAnswer records the answer and whether it stood at the tip of the range.
func (res *LaneResult) setAnswer(id, via string, atTip bool) {
	res.Answered, res.AnswerID, res.AnswerVia, res.answeredAtTip = true, id, via, atTip
}

// retry attempts the carried gaps of one kind. A gap resolved leaves the list;
// one that fails again stands and is NOT named a second time.
func (r *LaneRead) retry(ctx context.Context, res *LaneResult, gaps *[]gap, carried []gap,
	kind string, budgetItems, budgetBytes *int) bool {
	for _, g := range carried {
		if g.kind != kind || permanentGap(g.kind) || !held(*gaps, g.at) {
			continue
		}
		if *budgetItems <= 0 {
			return false
		}
		item, ok := r.itemAt(ctx, g.at)
		if !ok {
			continue
		}
		cost := item.cost()
		if cost > r.MaxBytes || cost > *budgetBytes {
			continue // still the budget's gap, at this budget
		}
		answered, id, via, gerr := r.take(ctx, item)
		*budgetItems--
		*budgetBytes -= cost
		res.Progress = true
		if gerr != nil {
			kind, _ := splitGapError(gerr)
			if permanentGap(kind) && g.kind != kind {
				updateKind(*gaps, g.at, kind)
				continue
			}
			continue
		}
		*gaps = drop(*gaps, g.at)
		if answered {
			res.setAnswer(id, via, true)
			return true
		}
	}
	return false
}

// walkItems is phase two: the uncovered lane, item by item, all-or-none.
func (r *LaneRead) walkItems(ctx context.Context, res *LaneResult, gaps *[]gap,
	commits []string, from position, resumed bool, budgetItems, budgetBytes *int) (answered, stoppedEarly bool, cur position) {
	cur = from
	start := 0
	if resumed {
		for i, c := range commits {
			if c == from.commit {
				start = i
				break
			}
		}
	}
	for ci := start; ci < len(commits); ci++ {
		commit := commits[ci]
		items, err := r.items(ctx, commit)
		if err != nil {
			// A commit whose listing failed: what it added is UNKNOWN rather
			// than absent, so it is a gap and never a walked-past nothing.
			at := position{commit: commit, item: 0}.at()
			if !r.record(res, gaps, at, GapCommit, oneLineOf(err.Error())) {
				return false, true, position{commit: commit, item: 0}
			}
			cur = position{commit: commit, item: 0}
			continue
		}
		first := 0
		if resumed && commit == from.commit {
			first = from.item
		}
		for ii := first; ; ii++ {
			item, ok, ierr := items.at(ctx, r, ii)
			if ierr != nil {
				// The stream behind this commit's items broke. Its remainder is
				// UNKNOWN rather than absent, so it is one gap and the commit
				// ends here: stepping to the next index would ask the same
				// broken stream the same question for ever.
				at := position{commit: commit, item: ii}.at()
				if !r.record(res, gaps, at, GapCommit, oneLineOf(ierr.Error())) {
					return false, true, position{commit: commit, item: ii}
				}
				res.Progress = true
				cur = position{commit: commit, item: ii + 1}
				break
			}
			if !ok {
				break
			}
			cur = position{commit: commit, item: ii}
			if *budgetItems <= 0 {
				return false, true, cur
			}
			cost := item.cost()
			if cost > r.MaxBytes {
				// AN ITEM THAT CANNOT FIT A WHOLE BUDGET is a coverage gap, and
				// it is THE BUDGET'S and not the item's.
				if !r.record(res, gaps, item.at(), GapBudget, budgetRemedy(cost)) {
					return false, true, cur
				}
				res.Progress = true
				cur = position{commit: commit, item: ii + 1}
				continue
			}
			if cost > *budgetBytes {
				// A poll stops BEFORE an item that does not fit and never
				// advances past unread content.
				return false, true, cur
			}
			hit, id, via, gerr := r.take(ctx, item)
			*budgetItems--
			*budgetBytes -= cost
			res.Progress = true
			if gerr != nil {
				kind, reason := splitGapError(gerr)
				if kind == GapObject && strings.Contains(reason, "the object reader is not running") {
					if r.clockGapRecorded {
						continue
					}
					r.clockGapRecorded = true
				}
				if !r.record(res, gaps, item.at(), kind, reason) {
					return false, true, cur
				}
				cur = position{commit: commit, item: ii + 1}
				continue
			}
			cur = position{commit: commit, item: ii + 1}
			if hit {
				atTip := ci == len(commits)-1 && !items.more(ii)
				res.setAnswer(id, via, atTip)
				return true, false, cur
			}
		}
		cur = position{commit: commit, item: items.count()}
	}
	return false, false, cur
}

// record retains one gap, ONCE, and answers whether there was room for it. A
// poll that would record a sixty-fifth stops BEFORE that item and leaves the
// bookmark there, so the rest stays behind the bookmark as ordinary uncovered
// lane rather than growing the state file without limit -- and a position
// already retained is never appended twice, so a tip commit whose listing keeps
// failing cannot make gaps= climb for ever.
func (r *LaneRead) record(res *LaneResult, gaps *[]gap, at, kind, reason string) bool {
	if held(*gaps, at) {
		return true
	}
	if len(*gaps) >= GapCap {
		return false
	}
	*gaps = append(*gaps, gap{at: at, kind: kind})
	res.NewGaps = append(res.NewGaps, at)
	res.Reasons[at] = kind + ": " + reason
	return true
}

func budgetRemedy(cost int) string {
	return fmt.Sprintf("the whole item costs %d bytes, which is more than the whole of --correlate-bytes; a raise to at least that lets this read attempt the item", cost)
}

func splitGapError(err error) (kind, reason string) {
	text := err.Error()
	for _, k := range []string{GapHeaderCap, GapHeader, GapObject} {
		if rest, ok := strings.CutPrefix(text, k+": "); ok {
			return k, rest
		}
	}
	return GapObject, oneLineOf(text)
}

func held(gaps []gap, at string) bool {
	for _, g := range gaps {
		if g.at == at {
			return true
		}
	}
	return false
}

func drop(gaps []gap, at string) []gap {
	out := gaps[:0]
	for _, g := range gaps {
		if g.at != at {
			out = append(out, g)
		}
	}
	return out
}

func updateKind(gaps []gap, at, newKind string) bool {
	for i, g := range gaps {
		if g.at == at {
			gaps[i].kind = newKind
			return true
		}
	}
	return false
}

// remaining counts ITEMS -- note headers plus appended RECEIPTS lines --
// between the bookmark and the tip, NEVER COMMITS and never paths (draft 8,
// K3b; the paths half is the Fable read's F4: one note file added beside five
// appended receipt lines is SIX items and two paths, and a caller told `2`
// would expect one more poll and need two).
//
// Counting what remains means walking the rest of the lane, which is the
// unbounded read this section exists to avoid. So the count is attempted only
// inside a COUNTING ALLOWANCE of at most --correlate-max further commits, and
// past it the field is `-`, which reads MORE THAN THIS POLL COULD COUNT and is
// never a number the tool did not measure.
func (r *LaneRead) remaining(ctx context.Context, at position) string {
	if at.commit == "" {
		return "-"
	}
	out, err := r.git(ctx, "log", "--first-parent", "--reverse", "--numstat",
		"--format=%x01%H", "--max-count="+strconv.Itoa(r.MaxItems+1),
		at.commit+"^.."+r.Ref, "--", r.Lane+"/")
	if err != nil {
		return "-"
	}
	commits := parseNumstat(out)
	if len(commits) > r.MaxItems {
		return "-"
	}
	total := 0
	for _, c := range commits {
		if c.sha == at.commit {
			if n := c.items - at.item; n > 0 {
				total += n
			}
			continue
		}
		total += c.items
	}
	return strconv.Itoa(total)
}

// walk lists the lane's commits in FIRST-PARENT ORDER FORWARD from the floor,
// bounded, and says whether there are more past the bound -- or that it could
// not list them at all, which is never an empty lane.
func (r *LaneRead) walk(ctx context.Context, floor string, includeFloor bool) (commits []string, more bool, err error) {
	limit := r.MaxItems + 1
	rng := floor + ".." + r.Ref
	if includeFloor {
		rng = floor + "^.." + r.Ref
	}
	out, err := r.git(ctx, "rev-list", "--first-parent", "--reverse",
		"--max-count="+strconv.Itoa(limit+1), rng, "--", r.Lane+"/")
	if err != nil {
		return nil, false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			commits = append(commits, line)
		}
	}
	if len(commits) > limit {
		commits, more = commits[:limit], true
	}
	return commits, more, nil
}

// commitItems is one commit's item sequence: THE NOTE FILES THAT COMMIT ADDED
// under the lane, by path compared bytewise, and then THE LINES THAT COMMIT
// APPENDED to the lane's RECEIPTS, in file order.
//
// The notes are known up front, because their names and sizes are one cheap
// call. The receipt lines are STREAMED, one at a time, so a 64 KiB append is
// never held in this process's memory to be charged item by item afterwards:
// the one-item buffer is a bound on what is held and not only on what is
// counted.
type commitItems struct {
	commit   string
	notes    []laneItem
	receipts *receiptStream
}

func (c *commitItems) count() int {
	n := len(c.notes)
	if c.receipts != nil {
		n += c.receipts.produced
	}
	return n
}

// at answers the item at index i, streaming a receipt line if that is what i
// names. ok is false where the sequence has ended.
func (c *commitItems) at(ctx context.Context, r *LaneRead, i int) (laneItem, bool, error) {
	if i < len(c.notes) {
		return c.notes[i], true, nil
	}
	if c.receipts == nil {
		return laneItem{}, false, nil
	}
	line, ok, err := c.receipts.upTo(i - len(c.notes))
	if err != nil {
		return laneItem{}, false, err
	}
	if !ok {
		return laneItem{}, false, nil
	}
	return laneItem{commit: c.commit, index: i, receipt: line}, true, nil
}

// more answers whether anything follows index i.
func (c *commitItems) more(i int) bool {
	if i+1 < len(c.notes) {
		return true
	}
	if c.receipts == nil {
		return false
	}
	_, ok, err := c.receipts.upTo(i - len(c.notes) + 1)
	return err == nil && ok
}

func (r *LaneRead) items(ctx context.Context, commit string) (*commitItems, error) {
	// -z AND THE RAW FORMAT, because a path git would QUOTE -- a non-ASCII
	// name, a tab, a newline -- comes back C-quoted from --name-status, and a
	// quoted name names no object: both cat-file calls answer `missing`, the
	// item becomes a permanent object gap, and UNAVAILABLE is unreachable for
	// that lane for ever (the Fable read, F2). The raw format also hands back
	// the BLOB SHA, so this read asks for objects by sha and never by path.
	out, err := r.git(ctx, "diff-tree", "--no-commit-id", "-r", "-z",
		"--first-parent", "--root", commit, "--", r.Lane+"/")
	if err != nil {
		return nil, err
	}
	type added struct{ path, blob string }
	var notes []added
	receipts := ""
	rec := strings.Split(out, "\x00")
	for i := 0; i+1 < len(rec); i += 2 {
		info, path := rec[i], rec[i+1]
		if !strings.HasPrefix(info, ":") || path == "" {
			continue
		}
		f := strings.Fields(info)
		if len(f) < 5 {
			continue
		}
		blob, status := f[3], f[4]
		if strings.HasSuffix(path, "/"+ReceiptsFile) || path == ReceiptsFile {
			receipts = path
			continue
		}
		if strings.HasPrefix(status, "A") {
			notes = append(notes, added{path: path, blob: blob})
		}
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].path < notes[j].path })
	shas := make([]string, 0, len(notes))
	for _, n := range notes {
		shas = append(shas, n.blob)
	}
	sizes, err := r.sizes(ctx, shas)
	if err != nil {
		return nil, err
	}
	c := &commitItems{commit: commit}
	for _, n := range notes {
		c.notes = append(c.notes, laneItem{commit: commit, index: len(c.notes),
			path: n.path, blob: n.blob, size: sizes[n.blob]})
	}
	if receipts != "" {
		c.receipts = r.receipts(ctx, commit)
		r.streams = append(r.streams, c.receipts)
	}
	return c, nil
}

// sizes reads every candidate blob's size in ONE process, shas and sizes only
// and no object opened, so the all-or-none check can be made before any item is
// read.
func (r *LaneRead) sizes(ctx context.Context, shas []string) (map[string]int, error) {
	out := map[string]int{}
	if len(shas) == 0 {
		return out, nil
	}
	var in bytes.Buffer
	for _, s := range shas {
		fmt.Fprintf(&in, "%s\n", s)
	}
	raw, err := r.gitStdin(ctx, in.String(), "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	for i, s := range shas {
		if i >= len(lines) {
			break
		}
		f := strings.Fields(lines[i])
		if len(f) == 3 {
			n, _ := strconv.Atoi(f[2])
			out[s] = n
		}
	}
	return out, nil
}

// receiptStream is the lane's RECEIPTS append, read a line at a time from a
// diff so that only the added lines cross into this process and only one of
// them is held at once.
type receiptStream struct {
	cmd      *exec.Cmd
	pipe     *os.File
	reader   *bufio.Reader
	lines    []string // the lines produced so far, so a bookmark can resume
	produced int
	done     bool
	err      error
	cancel   context.CancelFunc
}

func (r *LaneRead) receipts(ctx context.Context, commit string) *receiptStream {
	procCtx, cancel := context.WithTimeout(ctx, r.Wall)
	pr, pw, err := os.Pipe()
	if err != nil {
		cancel()
		return &receiptStream{done: true, err: err}
	}
	cmd := exec.CommandContext(procCtx, "git", "-C", r.Dir, "diff", "--unified=0",
		"--no-color", commit+"^", commit, "--", r.Lane+"/"+ReceiptsFile)
	cmd.Stdout = pw
	cmd.Stderr = nil
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		cancel()
		return &receiptStream{done: true, err: err}
	}
	_ = pw.Close()
	_ = pr.SetReadDeadline(time.Now().Add(r.Wall))
	return &receiptStream{cmd: cmd, pipe: pr, reader: bufio.NewReaderSize(pr, 4096), cancel: cancel}
}

// upTo answers the nth appended line, streaming forward only as far as n.
func (s *receiptStream) upTo(n int) (string, bool, error) {
	for s.produced <= n {
		if s.done {
			return "", false, s.err
		}
		line, err := s.next()
		if err != nil {
			s.done, s.err = true, err
			s.close()
			return "", false, err
		}
		if line == nil {
			s.done = true
			s.close()
			return "", false, nil
		}
		s.lines = append(s.lines, *line)
		s.produced++
	}
	return s.lines[n], true, nil
}

// next reads forward to the next added line and answers it, or nil at the end.
// Nothing but the line itself is retained.
func (s *receiptStream) next() (*string, error) {
	for {
		raw, err := s.reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && strings.TrimSpace(raw) == "" {
				return nil, nil
			}
			if !errors.Is(err, io.EOF) {
				return nil, err
			}
		}
		line := strings.TrimRight(raw, "\n")
		if strings.HasPrefix(line, "+++") || !strings.HasPrefix(line, "+") {
			if errors.Is(err, io.EOF) {
				return nil, nil
			}
			continue
		}
		body := strings.TrimSpace(line[1:])
		if body == "" {
			if errors.Is(err, io.EOF) {
				return nil, nil
			}
			continue
		}
		return &body, nil
	}
}

// close ends the diff the moment the read stops wanting lines, so a lane whose
// receipts append is a megabyte long costs this poll the lines it took and not
// the megabyte.
func (s *receiptStream) close() {
	if s.pipe != nil {
		_ = s.pipe.Close()
		s.pipe = nil
	}
	if s.cmd != nil {
		_ = s.cmd.Wait()
		s.cmd = nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// itemAt re-finds a retained gap's item so a later poll can attempt it again.
func (r *LaneRead) itemAt(ctx context.Context, at string) (laneItem, bool) {
	commit, idx, ok := strings.Cut(at, ":")
	if !ok {
		return laneItem{}, false
	}
	n, err := strconv.Atoi(idx)
	if err != nil {
		return laneItem{}, false
	}
	items, ierr := r.items(ctx, commit)
	if ierr != nil {
		return laneItem{}, false
	}
	item, found, aerr := items.at(ctx, r, n)
	if aerr != nil || !found {
		return laneItem{}, false
	}
	return item, true
}

// take reads one item -- a header to at most HeaderCap, or a receipt line --
// and answers whether it answers the ping, and with what id.
//
// AN ANSWER IS A NOTE OR A RECEIPT FROM THE LINE TO THE CALLER. A cursor
// commit, a receipt to somebody else, a note to a third line: none of them is
// an answer.
func (r *LaneRead) take(ctx context.Context, item laneItem) (answered bool, id, via string, gap error) {
	if item.path == "" {
		// A receipt is the one answer that is exact by construction, and the
		// id a person checks it by is the ping's own: `nova-bus receipt --note
		// <that id>` is the correlation anybody can repeat by hand.
		r.taken++
		r.decoded += item.cost()
		if item.cost() > r.peak {
			r.peak = item.cost()
		}
		f := strings.Fields(item.receipt)
		if len(f) >= 2 && f[len(f)-1] == r.PingID {
			return true, r.PingID, "receipt", nil
		}
		return false, "", "", nil
	}
	raw, consumed, err := r.objects.head(item.blob, HeaderCap)
	r.taken++
	r.decoded += consumed
	if len(raw) > r.peak {
		r.peak = len(raw)
	}
	if err != nil {
		return false, "", "", fmt.Errorf("%s: %s", GapObject, oneLineOf(err.Error()))
	}
	note, _, _, perr := bus.ParseNoteBodyStream(item.path, bytes.NewReader(raw), 0, HeaderCap)
	if perr != nil {
		// A header this read cannot parse under the bus's own semantics is a
		// COVERAGE GAP and never a note ruled out -- and WHICH KIND it is
		// decides which remedy the line names.
		if !bytes.Contains(raw, []byte("\n\n")) && len(raw) >= HeaderCap {
			return false, "", "", fmt.Errorf("%s: no blank line ends this header inside the fixed %d-byte cap, which is not a budget; no --correlate-bytes raise reads it", GapHeaderCap, HeaderCap)
		}
		return false, "", "", fmt.Errorf("%s: the header ends inside the fixed cap and the bus's own semantics cannot parse it (%s); no budget change clears that", GapHeader, oneLineOf(perr.Error()))
	}
	if !r.resolves(note.Header.From, r.lineName()) {
		return false, "", "", nil
	}
	to, cc := r.recipients(note.Header)
	if !r.names(to, r.Caller) && !r.names(cc, r.Caller) {
		return false, "", "", nil
	}
	answerID := note.Header.ID
	if answerID == "" {
		answerID = "-"
	}
	for _, re := range note.Header.Re {
		if re == r.PingID {
			return true, answerID, "Re line", nil
		}
	}
	return true, answerID, "", nil
}

// objectReader is ONE git process per poll, handed one object sha at a time and
// answering the first n DECODED bytes of it. It is the "one item of buffer at a
// time" bound made literal: the read holds at most one item's HeaderCap at
// once, so peak resident bytes are a constant and not a function of how deep
// the lane is -- and a poll costs one process rather than one per item, which
// is the two-minute rule applied to this tool's own reads.
//
// IT HAS A DEADLINE OF ITS OWN, and that is the repair of the fourth bound: the
// process runs under a 10s context that kills it, the pipe it writes to carries
// a read deadline, and WaitDelay ends it if it ignores both -- so a wedged git
// cannot hold a poll open with a child still running.
//
// PHYSICAL I/O INSIDE GIT IS NOT BOUNDED HERE AND IS NOT CLAIMED TO BE (draft
// 8, K3b). What crosses into this process IS: the whole object is consumed and
// CHARGED to the byte budget, and at most HeaderCap of it is ever decoded --
// the read stops at the blank line that ends the header, so no note body is
// opened however large the blob is.
type objectReader struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	pipe   *os.File
	out    *bufio.Reader
	wall   time.Duration
	cancel context.CancelFunc
	dead   bool
	clockGap bool
}

func (r *LaneRead) reader(ctx context.Context) (*objectReader, error) {
	procCtx, cancel := context.WithTimeout(ctx, r.Wall)
	cmd := exec.CommandContext(procCtx, "git", "-C", r.Dir, "cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stdout = pw
	cmd.Stderr = nil
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		cancel()
		return nil, err
	}
	_ = pw.Close()
	return &objectReader{cmd: cmd, in: in, pipe: pr,
		out: bufio.NewReaderSize(pr, 64<<10), wall: r.Wall, cancel: cancel}, nil
}

func (o *objectReader) close() {
	if o == nil {
		return
	}
	_ = o.in.Close()
	if o.pipe != nil {
		_ = o.pipe.Close()
	}
	if o.cancel != nil {
		o.cancel()
	}
	if o.cmd != nil {
		_ = o.cmd.Wait()
	}
}

// head answers the first n decoded bytes of one object, stopping at the blank
// line that ends its header, and the number of bytes the object cost.
func (o *objectReader) head(sha string, n int) (raw []byte, consumed int, err error) {
	if o == nil || o.dead {
		if !o.clockGap {
			o.clockGap = true
		}
		return nil, 0, fmt.Errorf("the object reader is not running")
	}
	_ = o.pipe.SetReadDeadline(time.Now().Add(o.wall))
	if _, err := io.WriteString(o.in, sha+"\n"); err != nil {
		o.dead = true
		return nil, 0, err
	}
	line, err := o.out.ReadString('\n')
	if err != nil {
		o.dead = true
		return nil, 0, err
	}
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) != 3 {
		// "<sha> missing" -- the stream is still in step, and this object is a
		// coverage gap rather than a note ruled out.
		return nil, 0, fmt.Errorf("%s", oneLineOf(strings.TrimSpace(line)))
	}
	size, err := strconv.Atoi(f[2])
	if err != nil {
		o.dead = true
		return nil, 0, fmt.Errorf("git answered a size this read cannot parse")
	}
	limit := n
	if size < limit {
		limit = size
	}
	buf := make([]byte, 0, limit)
	blank := 0
	read := 0
	for read < limit {
		b, rerr := o.out.ReadByte()
		if rerr != nil {
			o.dead = true
			return nil, read, rerr
		}
		read++
		buf = append(buf, b)
		// The header ends at the first blank line, and the read stops there:
		// NO NOTE BODY IS EVER OPENED.
		if b == '\n' {
			blank++
			if blank == 2 {
				break
			}
		} else if b != '\r' {
			blank = 0
		}
	}
	// The rest of the object and its trailing newline are consumed to keep the
	// stream in step, and never decoded. They are CHARGED, because they crossed
	// into this process.
	if rest := int64(size-read) + 1; rest > 0 {
		if _, err := io.CopyN(io.Discard, o.out, rest); err != nil {
			o.dead = true
			return buf, size, err
		}
	}
	return buf, size, nil
}

func (r *LaneRead) lineName() string {
	name := strings.TrimPrefix(r.Lane, "from-")
	if r.Config != nil {
		if p, ok := r.Config.LaneOwner(r.Lane); ok {
			return p.Name
		}
	}
	return name
}

// resolves and names are the bus's identity rule and not a second one: a name
// is what the roster says it is.
func (r *LaneRead) resolves(value, want string) bool {
	if r.Config == nil {
		return strings.EqualFold(strings.TrimSpace(value), want)
	}
	p, ok := r.Config.Lookup(strings.TrimSpace(value))
	if !ok {
		return strings.EqualFold(strings.TrimSpace(value), want)
	}
	return strings.EqualFold(p.Name, want)
}

// recipients is the bus's own resolution where a roster could be loaded, and
// the header's plain names where it could not -- never a second identity RULE,
// only the absence of the roster that would apply it.
func (r *LaneRead) recipients(h bus.Header) (to, cc []string) {
	if r.Config != nil {
		return h.Recipients(r.Config)
	}
	split := func(v string) []string {
		var out []string
		for _, one := range strings.Split(v, ",") {
			if one = strings.TrimSpace(one); one != "" {
				out = append(out, one)
			}
		}
		return out
	}
	return split(h.To), split(h.Cc)
}

func (r *LaneRead) names(list []string, want string) bool {
	for _, one := range list {
		if strings.EqualFold(one, want) {
			return true
		}
	}
	return false
}

func (r *LaneRead) ancestor(ctx context.Context, sha string) bool {
	if sha == "" {
		return false
	}
	_, err := r.git(ctx, "merge-base", "--is-ancestor", sha, r.Ref)
	return err == nil
}

func (r *LaneRead) git(ctx context.Context, args ...string) (string, error) {
	return r.gitStdin(ctx, "", args...)
}

func (r *LaneRead) gitStdin(ctx context.Context, stdin string, args ...string) (string, error) {
	wall := r.Wall
	if wall <= 0 {
		wall = GitWall
	}
	ctx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir}, args...)...)
	cmd.WaitDelay = time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %s", args[0], oneLineOf(errb.String()+" "+err.Error()))
	}
	return out.String(), nil
}

// numstatCommit is one commit of the counting allowance's read: its sha and how
// many ITEMS it added.
type numstatCommit struct {
	sha   string
	items int
}

// parseNumstat counts items and not paths: a note file added is ONE item
// whatever its line count, and a RECEIPTS path is as many items as the lines it
// gained.
func parseNumstat(out string) []numstatCommit {
	var commits []numstatCommit
	for _, block := range strings.Split(out, "\x01") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		c := numstatCommit{sha: strings.TrimSpace(lines[0])}
		for _, l := range lines[1:] {
			f := strings.Split(strings.TrimSpace(l), "\t")
			if len(f) != 3 || f[2] == "" {
				continue
			}
			if strings.HasSuffix(f[2], "/"+ReceiptsFile) || f[2] == ReceiptsFile {
				if n, err := strconv.Atoi(f[0]); err == nil {
					c.items += n
				}
				continue
			}
			if f[1] == "0" {
				c.items++
			}
		}
		commits = append(commits, c)
	}
	return commits
}

// parseBookmark reads the record's seventh field: the item the next poll
// attempts first, followed by the gap positions still owed and what made each
// one a gap -- because a permanent gap is never retried and a budget one is
// retried first, and a poll that could not tell them apart would do neither.
func parseBookmark(field string) (position, []gap, bool) {
	if field == "" || field == "-" {
		return position{}, nil, false
	}
	head, rest, _ := strings.Cut(field, ";")
	var gaps []gap
	for _, g := range strings.Split(rest, ",") {
		if g = strings.TrimSpace(g); g == "" {
			continue
		}
		parts := strings.Split(g, ":")
		switch len(parts) {
		case 2:
			gaps = append(gaps, gap{at: g, kind: GapObject})
		case 3:
			gaps = append(gaps, gap{at: parts[0] + ":" + parts[1], kind: parts[2]})
		}
	}
	commit, idx, ok := strings.Cut(head, ":")
	if !ok || commit == "" {
		return position{}, gaps, false
	}
	n, err := strconv.Atoi(idx)
	if err != nil {
		return position{}, gaps, false
	}
	return position{commit: commit, item: n}, gaps, true
}

func renderBookmark(at position, gaps []gap) string {
	if at.commit == "" && len(gaps) == 0 {
		return "-"
	}
	head := "-"
	if at.commit != "" {
		head = at.at()
	}
	if len(gaps) == 0 {
		return head
	}
	var spelled []string
	for _, g := range gaps {
		spelled = append(spelled, g.at+":"+g.kind)
	}
	return head + ";" + strings.Join(spelled, ",")
}
