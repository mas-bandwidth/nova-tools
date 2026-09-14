package wake

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
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
	// GitWall is the wall clock on one git process this read starts. The
	// two-minute law applied to a read the same way --gh-timeout applies it to
	// the forge.
	GitWall = 10 * time.Second
	// GapCap bounds the retained gap list: a bound on a read that becomes an
	// unbounded bound on the state is not a bound.
	GapCap = 64
	// ReceiptsFile is the lane's append-only receipt file (SPEC.md, nova-bus --
	// The receipt rule).
	ReceiptsFile = "RECEIPTS"
)

// Gap kinds. Which remedy applies depends on which of the three it is, and the
// line names it: a caller must never be told to raise a budget against a cap
// that is not a budget.
const (
	GapHeaderCap = "header-cap" // permanent at every budget (draft 9, K4a)
	GapBudget    = "budget"     // the budget's and not the item's (draft 9, K4b)
	GapObject    = "object"     // missing, corrupt, or a read that failed
)

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
	Wall     time.Duration

	// objects is this poll's one object reader.
	objects *objectReader
}

// LaneResult is what one poll of the bounded read learned.
type LaneResult struct {
	Answered  bool
	AnswerID  string
	AnswerVia string // "its Re line" or "receipt", where the correlation is exact

	Complete  bool
	Remaining string // items between the bookmark and the tip, or "-" past the counting allowance
	Gaps      int
	NewGaps   []string // the gaps first recorded this poll: named ONCE, here
	Reasons   map[string]string
	Bookmark  string // the seventh field: <commit>:<item>[;<gap>,...]
	Progress  bool
	Notes     []string
}

// position is a bookmark: the item the next poll attempts first.
type position struct {
	commit string
	item   int
}

// laneItem is one thing the read may take: a note header or an appended
// RECEIPTS line.
type laneItem struct {
	commit  string
	index   int
	path    string // "" for a receipt line
	size    int    // the blob's size, for the all-or-none cost check
	receipt string // the appended line, for a receipt item
}

func (i laneItem) at() string { return i.commit + ":" + strconv.Itoa(i.index) }

// cost is what taking this item spends of the byte budget: up to HeaderCap for
// a header, its own length for a receipt line. It is asked BEFORE each item, so
// no item is ever half-read and no bookmark ever points inside one.
func (i laneItem) cost() int {
	if i.path == "" {
		return len(i.receipt) + 1
	}
	if i.size > HeaderCap {
		return HeaderCap
	}
	return i.size
}

// Run performs one poll's read, resuming from the bookmark the record carries.
func (r *LaneRead) Run(ctx context.Context, bookmark string) LaneResult {
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
	if reader, err := r.reader(ctx); err == nil {
		r.objects = reader
		defer func() {
			reader.close()
			r.objects = nil
		}()
	}
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
		res.Bookmark = renderBookmark(from, gaps)
		res.Gaps = len(gaps)
		return res
	}

	floor := r.Anchor
	includeFloor := false
	if resumed {
		floor, includeFloor = from.commit, true
	}
	commits, more := r.walk(ctx, floor, includeFloor)

	budgetItems, budgetBytes := r.MaxItems, r.MaxBytes
	// A gap is a potential answer this read did not see, and that is the whole
	// of why it is kept: a later poll comes back to it first.
	for _, at := range gaps {
		if budgetItems <= 0 {
			break
		}
		item, ok := r.itemAt(ctx, commits, at)
		if !ok {
			continue
		}
		if item.cost() > r.MaxBytes {
			continue // still the budget's gap, at this budget
		}
		if item.cost() > budgetBytes {
			break
		}
		answered, via, gerr := r.take(ctx, item)
		budgetItems--
		budgetBytes -= item.cost()
		res.Progress = true
		if gerr != nil {
			continue // it stands, and it was named on the poll that recorded it
		}
		gaps = remove(gaps, at)
		if answered {
			res.Answered, res.AnswerID, res.AnswerVia = true, r.PingID, via
			res.Bookmark = renderBookmark(from, gaps)
			res.Gaps = len(gaps)
			res.Complete = !more && r.covered(ctx, commits, from) && len(gaps) == 0
			res.Remaining = "0"
			return res
		}
	}

	start := 0
	if resumed {
		for i, c := range commits {
			if c == from.commit {
				start = i
				break
			}
		}
	}
	stoppedEarly := false
	cur := from
	for ci := start; ci < len(commits); ci++ {
		commit := commits[ci]
		items, err := r.items(ctx, commit)
		if err != nil {
			// An object this read cannot get from git at all.
			at := commit + ":0"
			if len(gaps) < GapCap {
				gaps = append(gaps, at)
				res.NewGaps = append(res.NewGaps, at)
				res.Reasons[at] = GapObject + ": " + oneLineOf(err.Error())
				res.Progress = true
			}
			cur = position{commit: commit, item: 0}
			continue
		}
		first := 0
		if resumed && commit == from.commit {
			first = from.item
		}
		for ii := first; ii < len(items); ii++ {
			item := items[ii]
			cur = position{commit: commit, item: ii}
			if budgetItems <= 0 {
				stoppedEarly = true
				break
			}
			cost := item.cost()
			if cost > r.MaxBytes {
				// AN ITEM THAT CANNOT FIT A WHOLE BUDGET is a coverage gap, and
				// it is THE BUDGET'S and not the item's: it stands only while
				// the budget stands, and a raise that makes the item fit reads
				// it on the next poll and resolves it (draft 9, K4b).
				if len(gaps) >= GapCap {
					stoppedEarly = true
					break
				}
				at := item.at()
				gaps = append(gaps, at)
				res.NewGaps = append(res.NewGaps, at)
				res.Reasons[at] = GapBudget + ": the whole item costs " + strconv.Itoa(cost) +
					" bytes, which is more than the whole of --correlate-bytes; raise it"
				res.Progress = true
				cur = position{commit: commit, item: ii + 1}
				continue
			}
			if cost > budgetBytes {
				// A poll stops BEFORE an item that does not fit and never
				// advances past unread content.
				stoppedEarly = true
				break
			}
			answered, via, gerr := r.take(ctx, item)
			budgetItems--
			budgetBytes -= cost
			res.Progress = true
			if gerr != nil {
				if len(gaps) >= GapCap {
					stoppedEarly = true
					break
				}
				at := item.at()
				gaps = append(gaps, at)
				res.NewGaps = append(res.NewGaps, at)
				res.Reasons[at] = gerr.Error()
				cur = position{commit: commit, item: ii + 1}
				continue
			}
			cur = position{commit: commit, item: ii + 1}
			if answered {
				res.Answered, res.AnswerID, res.AnswerVia = true, r.PingID, via
				res.Bookmark = renderBookmark(cur, gaps)
				res.Gaps = len(gaps)
				res.Complete = !more && ci == len(commits)-1 && ii == len(items)-1 && len(gaps) == 0
				res.Remaining = "0"
				return res
			}
		}
		if stoppedEarly {
			break
		}
		cur = position{commit: commit, item: len(items)}
	}

	res.Bookmark = renderBookmark(cur, gaps)
	res.Gaps = len(gaps)
	uncovered := stoppedEarly || more
	// COMPLETE IS THE TIP *AND* ZERO GAPS, both halves (draft 8, K3a):
	// reaching the tip is not covering the lane.
	res.Complete = !uncovered && len(gaps) == 0
	if uncovered {
		res.Remaining = r.remaining(ctx, cur)
	} else {
		res.Remaining = "0"
	}
	return res
}

// covered answers whether a resumed poll that took only gap items is standing
// at the tip with nothing uncovered behind it.
func (r *LaneRead) covered(ctx context.Context, commits []string, at position) bool {
	if len(commits) == 0 {
		return true
	}
	last := commits[len(commits)-1]
	if at.commit != last {
		return false
	}
	items, err := r.items(ctx, last)
	return err == nil && at.item >= len(items)
}

// remaining counts ITEMS -- note headers plus appended RECEIPTS lines --
// between the bookmark and the tip, NEVER COMMITS (draft 8, K3b): nine hundred
// notes added in ONE commit are remaining=1 in commits and nine hundred items
// of work, so a caller reading remaining=1 would expect one more poll and need
// three.
//
// Counting what remains means walking the rest of the lane, which is the
// unbounded read this section exists to avoid. So the count is attempted only
// inside a COUNTING ALLOWANCE of at most --correlate-max further commits, names
// and paths only, no object opened; past it the field is `-`, which reads MORE
// THAN THIS POLL COULD COUNT and is never a number the tool did not measure.
func (r *LaneRead) remaining(ctx context.Context, at position) string {
	if at.commit == "" {
		return "-"
	}
	out, err := r.git(ctx, "log", "--first-parent", "--reverse", "--name-only",
		"--format=%x01%H", "--max-count="+strconv.Itoa(r.MaxItems+1),
		at.commit+"^.."+r.Ref, "--", r.Lane+"/")
	if err != nil {
		return "-"
	}
	commits := parseLog(out)
	if len(commits) > r.MaxItems {
		return "-"
	}
	total := 0
	for _, c := range commits {
		items := len(c.paths)
		if c.sha == at.commit {
			items -= at.item
			if items < 0 {
				items = 0
			}
		}
		total += items
	}
	return strconv.Itoa(total)
}

// walk lists the lane's commits in FIRST-PARENT ORDER FORWARD from the floor,
// bounded, and says whether there are more past the bound. Without a fixed
// order a bookmark is a number about one run on one machine, and a resume is a
// guess; with it, two implementations, two platforms and two polls resume at
// the same item.
func (r *LaneRead) walk(ctx context.Context, floor string, includeFloor bool) (commits []string, more bool) {
	limit := r.MaxItems + 1
	rng := floor + ".." + r.Ref
	if includeFloor {
		rng = floor + "^.." + r.Ref
	}
	out, err := r.git(ctx, "rev-list", "--first-parent", "--reverse",
		"--max-count="+strconv.Itoa(limit+1), rng, "--", r.Lane+"/")
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			commits = append(commits, line)
		}
	}
	if len(commits) > limit {
		commits, more = commits[:limit], true
	}
	return commits, more
}

// items is one commit's item sequence: THE NOTE FILES THAT COMMIT ADDED under
// the lane, by path compared bytewise, and then THE LINES THAT COMMIT APPENDED
// to the lane's RECEIPTS, in file order.
func (r *LaneRead) items(ctx context.Context, commit string) ([]laneItem, error) {
	out, err := r.git(ctx, "diff-tree", "--no-commit-id", "--name-status", "-r",
		"--first-parent", "--root", commit, "--", r.Lane+"/")
	if err != nil {
		return nil, err
	}
	var paths []string
	receipts := false
	for _, line := range strings.Split(out, "\n") {
		status, path, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || path == "" {
			continue
		}
		if strings.HasSuffix(path, "/"+ReceiptsFile) {
			receipts = true
			continue
		}
		if strings.HasPrefix(status, "A") {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	sizes, err := r.sizes(ctx, commit, paths)
	if err != nil {
		return nil, err
	}
	var items []laneItem
	for _, p := range paths {
		items = append(items, laneItem{commit: commit, index: len(items), path: p, size: sizes[p]})
	}
	if receipts {
		for _, line := range r.appended(ctx, commit) {
			items = append(items, laneItem{commit: commit, index: len(items), receipt: line})
		}
	}
	return items, nil
}

// sizes reads every candidate blob's size in ONE process, names and sizes only
// and no object opened, so the all-or-none check can be made before any item is
// read.
func (r *LaneRead) sizes(ctx context.Context, commit string, paths []string) (map[string]int, error) {
	out := map[string]int{}
	if len(paths) == 0 {
		return out, nil
	}
	var in bytes.Buffer
	for _, p := range paths {
		fmt.Fprintf(&in, "%s:%s\n", commit, p)
	}
	raw, err := r.gitStdin(ctx, in.String(), "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	for i, p := range paths {
		if i >= len(lines) {
			break
		}
		f := strings.Fields(lines[i])
		if len(f) == 3 {
			n, _ := strconv.Atoi(f[2])
			out[p] = n
		}
	}
	return out, nil
}

// appended is the lines this commit added to the lane's RECEIPTS, taken from a
// diff so that only the added lines cross into this process.
func (r *LaneRead) appended(ctx context.Context, commit string) []string {
	out, err := r.git(ctx, "diff", "--unified=0", "--no-color", commit+"^", commit,
		"--", r.Lane+"/"+ReceiptsFile)
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "+++") || !strings.HasPrefix(line, "+") {
			continue
		}
		if body := strings.TrimSpace(line[1:]); body != "" {
			lines = append(lines, body)
		}
	}
	return lines
}

// itemAt re-finds a retained gap's item so a later poll can attempt it again.
func (r *LaneRead) itemAt(ctx context.Context, commits []string, at string) (laneItem, bool) {
	commit, idx, ok := strings.Cut(at, ":")
	if !ok {
		return laneItem{}, false
	}
	n, err := strconv.Atoi(idx)
	if err != nil {
		return laneItem{}, false
	}
	items, ierr := r.items(ctx, commit)
	if ierr != nil || n >= len(items) {
		return laneItem{}, false
	}
	_ = commits
	return items[n], true
}

// take reads one item -- a header to at most HeaderCap, or a receipt line --
// and answers whether it answers the ping.
//
// AN ANSWER IS A NOTE OR A RECEIPT FROM THE LINE TO THE CALLER. A cursor
// commit, a receipt to somebody else, a note to a third line: none of them is
// an answer.
func (r *LaneRead) take(ctx context.Context, item laneItem) (answered bool, via string, gap error) {
	if item.path == "" {
		// A receipt is the one answer that is exact by construction.
		f := strings.Fields(item.receipt)
		if len(f) >= 2 && f[len(f)-1] == r.PingID {
			return true, "receipt", nil
		}
		return false, "", nil
	}
	raw, err := r.objects.head(item.commit+":"+item.path, item.cost())
	if err != nil {
		return false, "", fmt.Errorf("%s: %s", GapObject, oneLineOf(err.Error()))
	}
	note, _, _, perr := bus.ParseNoteBodyStream(item.path, bytes.NewReader(raw), 0, HeaderCap)
	if perr != nil {
		// A header this read cannot parse under the bus's own semantics is a
		// COVERAGE GAP and never a note ruled out. Where the header does not
		// end inside the fixed cap, the gap is PERMANENT AT EVERY BUDGET: the
		// fix belongs where that note was written or on the bus, never on a
		// flag here.
		if len(raw) >= HeaderCap && !bytes.Contains(raw, []byte("\n\n")) {
			return false, "", fmt.Errorf("%s: no blank line ends this header inside %d bytes, and that cap is not a budget", GapHeaderCap, HeaderCap)
		}
		return false, "", fmt.Errorf("%s: %s", GapHeaderCap, oneLineOf(perr.Error()))
	}
	if !r.resolves(note.Header.From, r.lineName()) {
		return false, "", nil
	}
	to, cc := r.recipients(note.Header)
	if !r.names(to, r.Caller) && !r.names(cc, r.Caller) {
		return false, "", nil
	}
	for _, re := range note.Header.Re {
		if re == r.PingID {
			return true, "its Re line", nil
		}
	}
	return true, "", nil
}

// objectReader is ONE git process per poll, handed one object spec at a time
// and answering the first n bytes of it. It is the "one item of buffer at a
// time" bound made literal: the read streams, holding at most one item's
// HeaderCap at once, so peak resident bytes are a constant and not a function
// of how deep the lane is -- and a poll costs one process rather than one per
// item, which is the two-minute rule applied to this tool's own reads.
//
// PHYSICAL I/O INSIDE GIT IS NOT BOUNDED HERE AND IS NOT CLAIMED TO BE (draft
// 8, K3b): git inflates objects, reads pack indexes and may walk a whole delta
// chain, none of it visible to the caller. What is bounded is what this process
// DECODES -- the n bytes below -- and its own time and its own buffers; the
// remainder of an object is drained to keep the stream in step and is never
// parsed.
type objectReader struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	dead bool
}

func (r *LaneRead) reader(ctx context.Context) (*objectReader, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &objectReader{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 64<<10)}, nil
}

func (o *objectReader) close() {
	if o == nil {
		return
	}
	_ = o.in.Close()
	_ = o.cmd.Wait()
}

// head answers the first n decoded bytes of one object.
func (o *objectReader) head(spec string, n int) ([]byte, error) {
	if o == nil || o.dead {
		return nil, fmt.Errorf("the object reader is not running")
	}
	if _, err := io.WriteString(o.in, spec+"\n"); err != nil {
		o.dead = true
		return nil, err
	}
	line, err := o.out.ReadString('\n')
	if err != nil {
		o.dead = true
		return nil, err
	}
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) != 3 {
		// "<spec> missing" -- the stream is still in step, and this object is a
		// coverage gap rather than a note ruled out.
		return nil, fmt.Errorf("%s", oneLineOf(strings.TrimSpace(line)))
	}
	size, err := strconv.Atoi(f[2])
	if err != nil {
		o.dead = true
		return nil, fmt.Errorf("git answered a size this read cannot parse")
	}
	take := n
	if size < take {
		take = size
	}
	buf := make([]byte, take)
	if _, err := io.ReadFull(o.out, buf); err != nil {
		o.dead = true
		return nil, err
	}
	// The rest of the object and its trailing newline are drained, never parsed.
	if _, err := io.CopyN(io.Discard, o.out, int64(size-take)+1); err != nil {
		o.dead = true
		return nil, err
	}
	return buf, nil
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

// logCommit is one commit of the counting allowance's names-and-paths read.
type logCommit struct {
	sha   string
	paths []string
}

func parseLog(out string) []logCommit {
	var commits []logCommit
	for _, block := range strings.Split(out, "\x01") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		c := logCommit{sha: strings.TrimSpace(lines[0])}
		for _, p := range lines[1:] {
			if p = strings.TrimSpace(p); p != "" {
				c.paths = append(c.paths, p)
			}
		}
		commits = append(commits, c)
	}
	return commits
}

// parseBookmark reads the record's seventh field: the item the next poll
// attempts first, followed by the gap positions still owed.
func parseBookmark(field string) (position, []string, bool) {
	if field == "" || field == "-" {
		return position{}, nil, false
	}
	head, rest, _ := strings.Cut(field, ";")
	var gaps []string
	for _, g := range strings.Split(rest, ",") {
		if g = strings.TrimSpace(g); g != "" {
			gaps = append(gaps, g)
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

func renderBookmark(at position, gaps []string) string {
	if at.commit == "" && len(gaps) == 0 {
		return "-"
	}
	head := "-"
	if at.commit != "" {
		head = at.commit + ":" + strconv.Itoa(at.item)
	}
	if len(gaps) == 0 {
		return head
	}
	return head + ";" + strings.Join(gaps, ",")
}

func remove(list []string, want string) []string {
	out := list[:0]
	for _, one := range list {
		if one != want {
			out = append(out, one)
		}
	}
	return out
}
