package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/machine"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The read verbs (section 3: inbox, where, card, log; 1.4.1; item IT22). Each
// reads through the one read path (ns_sprint_read on the store, the composed
// twin before G0) and writes nothing. A read verb's Result carries the read
// and, in Said, the text the verb prints; the view a program reads is filled
// into the request's Out when the caller gives one (the driver's Result has no
// field for it: a shape for the integrator, see the pull request).

// ---- the machine, computed at read time (1.4.1): IT17's MachineLine and
// InboxGroups over the heartbeat and the clock the read found.

// sprintAnswer decodes one sprint answer of a kind.
func sprintAnswer[T any](rd *sprintfn.ReadReply, slot int, kind string) (T, error) {
	var zero T
	if rd == nil || slot >= len(rd.Sprint) {
		return zero, fmt.Errorf("verbs: the read has no %s answer", kind)
	}
	qr, err := sprintfn.DecodeResult(kind, rd.Sprint[slot])
	if err != nil {
		return zero, err
	}
	v, ok := qr.(T)
	if !ok {
		return zero, fmt.Errorf("verbs: the %s answer is of another kind", kind)
	}
	return v, nil
}

// wallOf is the read's time in milliseconds.
func wallOf(rd *sprintfn.ReadReply) int64 {
	n, _ := strconv.ParseInt(string(rd.TimeMS), 10, 64)
	return n
}

// ---- inbox

// InboxPage is the most open notes one inbox lists: the second read reads each
// note's line by its seq, one lines query a note, and a note's line names up
// to 2,000 subjects (J cuts a note there, 1.3.4), so 100 notes are 200,000
// ids, Layer 2's bound on the ids one read returns (L2 4: 200,000 ids and
// 8 MiB, about 2 MiB here); an atomic read over it is refused BUDGET whole.
// More says open notes lie past the page.
const InboxPage = 100

// inboxNoticeLines is the most lines after the cursor one inbox reads for
// the notices it lists (2.5); NoticesMore says lines lie past them.
const inboxNoticeLines = 1000

// InboxReq is inbox's request: the cursor after which notices are listed, and
// the page of open notes (0 is InboxPage). Read is --read, which moves the
// cursor: refused until the write path carries it. Wait is --wait, which
// blocks on the notification stream for the first judgment after the cursor
// (inboxWait).
type InboxReq struct {
	After uint64
	Limit int
	Read  bool
	Wait  *InboxWait
	Out   *InboxView
}

// InboxView is the inbox as a program reads it, in the command's JSON shape
// (cmd/nova-sprint's inbox --json): the groups, the last seq read and the
// cursor as given, the read's time and the machine's line. More says open
// notes lie past the page, and NoticesMore lines past the notices read (Last
// is the last of those read: the next inbox from it lists the rest). A
// judgment group's Count is the subjects its notes were raised on, not the
// ones still open on them (judgmentGroups).
type InboxView struct {
	Groups      []sprint.Group `json:"groups"`
	Last        string         `json:"last"`
	Cursor      string         `json:"cursor"`
	At          time.Time      `json:"at"`
	Machine     string         `json:"machine"`
	More        bool           `json:"more,omitempty"`
	NoticesMore bool           `json:"notices_more,omitempty"`
}

// noteSeqRE is a note id's seq and epoch.
var noteSeqRE = regexp.MustCompile(`^n([1-9][0-9]{0,15})(?:~([1-9][0-9]{0,19}))?$`)

// noteLine is a note's line as the inbox reads it (L2 4's semantic line: its
// seq, its time, what it is about and its meta).
type noteLine struct {
	Seq   json.RawMessage            `json:"seq"`
	Kind  string                     `json:"kind"`
	AtMS  string                     `json:"at_ms"`
	About []string                   `json:"about"`
	Meta  map[string]json.RawMessage `json:"meta"`
}

func (l noteLine) meta(name string) string {
	var s string
	if raw, ok := l.Meta[name]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func (l noteLine) decisions() []string {
	var out []string
	if raw, ok := l.Meta["decisions"]; ok {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (l noteLine) at() time.Time {
	n, err := strconv.ParseInt(l.AtMS, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(n).UTC()
}

// Inbox lists what the coordinator must answer and what happened (section 3,
// inbox; 1.4.1; 2.2; 2.5): the groups computed from the heartbeat and the
// clock, the open judgments from {p}jnotes@e, and the notices after the
// cursor. It reads O(notes), never the notes' subjects (decision 56): a need
// with 100,000 waiters is 50 notes of 2,000, and the inbox reads 50 lines. The
// first read takes the page of open notes (Layer 1's range over the sprint's
// sorted set, IT17's twin), the lines after the cursor, the heartbeat and the
// clock, in one snapshot; the second reads each open note's line by its seq,
// which never changes once written. Two round trips (the design's row says
// one: jnote reads every subject's jopen, O(subjects), so the notes are read
// by their lines instead; see the pull request). A judgment's decisions are
// the ones its row prints with no card read (Printed on an empty snapshot):
// a decision whose condition reads a card is left out.
func Inbox(ctx context.Context, e *Env, req InboxReq) (Result, error) {
	const verb = "inbox"
	if req.Read {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest,
			"inbox --read moves the coordinator's cursor, and the write path has no part that writes it yet (IT16's sprint part carries no inbox cursor)")
	}
	if req.Wait != nil {
		return inboxWait(ctx, e, req)
	}
	limit := req.Limit
	if limit <= 0 || limit > InboxPage {
		limit = InboxPage
	}
	after := tset.Decimal(strconv.FormatUint(req.After, 10))
	res, err := e.Do(ctx, Planned{Verb: verb, Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
		return &sprintfn.ReadRequest{Epoch: epoch,
			Tset: []tset.ReadQuery{
				{Kind: "range", Key: e.Names.Prefix + "sprint:jnotes@" + string(epoch), Min: "-inf", Max: "+inf", Limit: limit},
				{Kind: "lines", AfterSeq: after, Limit: inboxNoticeLines + 1},
			},
			Sprint: []sprintfn.SprintQuery{keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyHeartbeat}), keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyClock})}}
	}})
	if err != nil {
		return res, err
	}
	rd := res.Read
	if len(rd.Tset) != 2 {
		return res, errors.New("inbox: the read answered the wrong number of queries")
	}
	hbr, err := sprintAnswer[sprintfn.HeartbeatResult](rd, 0, sprintfn.KeyHeartbeat)
	if err != nil {
		return res, err
	}
	cr, err := sprintAnswer[sprintfn.ClockResult](rd, 1, sprintfn.KeyClock)
	if err != nil {
		return res, err
	}
	wall := wallOf(rd)
	hb, clock := machine.ParseHeartbeat(hbr.Fields), machine.ClockOf(cr)
	v := InboxView{Cursor: string(after), Last: string(after), At: time.UnixMilli(wall).UTC(), Machine: machine.MachineLine(hb, clock, wall),
		More: rd.Tset[0].HasMore}
	v.Groups = append(v.Groups, machine.InboxGroups(hb, clock, wall)...)

	// The open notes' lines, by seq (second round trip).
	epoch := string(rd.Epoch)
	var ids []string
	var lines []tset.ReadQuery
	for _, id := range rd.Tset[0].IDs {
		m := noteSeqRE.FindStringSubmatch(id)
		if m == nil || (m[2] != "" && m[2] != epoch) || (m[2] == "" && epoch != "0") {
			return res, fmt.Errorf("inbox: {p}jnotes@%s holds %q, which is no note of the epoch", epoch, id)
		}
		seq, _ := strconv.ParseUint(m[1], 10, 64)
		through := tset.Decimal(m[1])
		ids = append(ids, id)
		lines = append(lines, tset.ReadQuery{Kind: "lines", AfterSeq: tset.Decimal(strconv.FormatUint(seq-1, 10)), ThroughSeq: &through, Limit: 1})
	}
	if len(lines) > 0 {
		r, err := sprintfn.Read(ctx, e.C, &sprintfn.ReadRequest{Epoch: rd.Epoch, Tset: lines})
		res.Trips++
		if err != nil {
			return res, err
		}
		if r.Err != nil {
			return res, r.Err
		}
		if r.Refusal != nil {
			return res, &Refused{Verb: verb, Refusal: r.Refusal}
		}
		judg, err := judgmentGroups(ids, r.Read)
		if err != nil {
			return res, err
		}
		v.Groups = append(v.Groups, judg...)
	}

	// The notices after the cursor (2.5), grouped by type: one line more than
	// the page is read, to say lines lie past it.
	after1 := rd.Tset[1]
	if len(after1.Lines) > inboxNoticeLines {
		after1.Lines, v.NoticesMore = after1.Lines[:inboxNoticeLines], true
	}
	notices, last, err := noticeGroups(after1)
	if err != nil {
		return res, err
	}
	if last != "" {
		v.Last = last
	}
	v.Groups = append(v.Groups, notices...)
	if v.Groups == nil {
		v.Groups = []sprint.Group{}
	}
	if req.Out != nil {
		*req.Out = v
	}
	res.Said = inboxText(v)
	return res, nil
}

// judgmentGroups groups the open notes by type and cause: one group of the
// notes one cause raised (a need's 50 notes of 2,000 waiters are one group),
// its decisions the ones printed with no card read (IT06's Printed on an empty
// snapshot). Its subjects are the ones its notes' lines name, read in O(notes)
// (decision 56): the subjects the notes were raised on, counted and the first
// MaxListed listed. A subject answered since (closed or held on it alone) is
// still among them: which subjects are still open is each subject's jopen,
// O(subjects), and the count of them in O(notes) is IT30's (a notes-only
// jnote, owed). The inbox prints the count as "raised on", never as open.
func judgmentGroups(ids []string, rd *sprintfn.ReadReply) ([]sprint.Group, error) {
	if len(rd.Tset) != len(ids) {
		return nil, errors.New("inbox: the notes' read answered the wrong number of queries")
	}
	lines := make([]noteLine, len(ids))
	for i, id := range ids {
		if len(rd.Tset[i].Lines) != 1 {
			return nil, fmt.Errorf("inbox: note %s has no line", id)
		}
		if err := json.Unmarshal(rd.Tset[i].Lines[0], &lines[i]); err != nil {
			return nil, fmt.Errorf("inbox: note %s's line: %w", id, err)
		}
	}
	return groupLines(ids, lines), nil
}

// groupOfLine is one judgment note as a group of its own (inbox --wait).
func groupOfLine(id string, l noteLine) sprint.Group {
	return groupLines([]string{id}, []noteLine{l})[0]
}

// groupLines groups judgment notes' lines by type and cause (judgmentGroups).
func groupLines(ids []string, lines []noteLine) []sprint.Group {
	var out []sprint.Group
	at := map[string]int{}
	members := map[int]map[string]bool{}
	empty := &sprint.Snapshot{}
	for i, id := range ids {
		l := lines[i]
		typ, cause := l.meta("type"), l.meta("cause")
		k := typ + "\x00" + cause
		g, ok := at[k]
		if !ok {
			g = len(out)
			at[k] = g
			members[g] = map[string]bool{}
			n := sprint.Note{ID: id, Kind: sprint.Judgment, Type: typ}
			var ds []string
			if len(l.About) > 0 {
				for _, d := range sprint.Printed(empty, sprint.Open{Key: sprint.OpenKey(id, l.About[0]), Note: n}) {
					ds = append(ds, d.String())
				}
			}
			out = append(out, sprint.Group{ID: id, Kind: sprint.Judgment, Type: typ, Oldest: l.at(), What: l.meta("text"), Decisions: ds})
		}
		grp := &out[g]
		grp.Notes = append(grp.Notes, id)
		if t := l.at(); !t.IsZero() && (grp.Oldest.IsZero() || t.Before(grp.Oldest)) {
			grp.Oldest = t
		}
		for _, s := range l.About {
			if !members[g][s] {
				members[g][s] = true
				grp.Count++
				if len(grp.Primaries) < sprint.MaxListed {
					grp.Primaries = append(grp.Primaries, s)
				}
			}
		}
	}
	for g := range out {
		out[g].Members = sortedKeys(members[g])
		out[g].Size = len(out[g].Members)
	}
	return out
}

// noticeGroups groups the notices among the lines after the cursor by type,
// in time order, and says the last seq read.
func noticeGroups(a tset.ReadAnswer) ([]sprint.Group, string, error) {
	var out []sprint.Group
	at := map[string]int{}
	last := ""
	for _, raw := range a.Lines {
		var l noteLine
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, "", fmt.Errorf("inbox: a line after the cursor: %w", err)
		}
		var seq string
		if json.Unmarshal(l.Seq, &seq) != nil {
			seq = string(l.Seq)
		}
		last = seq
		if l.Kind != "note" || l.meta("kind") != sprint.Happened {
			continue
		}
		typ := l.meta("type")
		g, ok := at[typ]
		if !ok {
			g = len(out)
			at[typ] = g
			out = append(out, sprint.Group{ID: "n" + seq, Kind: sprint.Happened, Type: typ, Oldest: l.at(), What: l.meta("text")})
		}
		grp := &out[g]
		grp.Notes = append(grp.Notes, "n"+seq)
		grp.Count += max(1, len(l.About))
		for _, s := range l.About {
			if len(grp.Primaries) < sprint.MaxListed && !contains(grp.Primaries, s) {
				grp.Primaries = append(grp.Primaries, s)
			}
		}
	}
	return out, last, nil
}

// inboxText is the inbox as the verb prints it: a line a group, its
// decisions, then the machine's line.
func inboxText(v InboxView) string {
	var b strings.Builder
	judg, other := 0, 0
	for _, g := range v.Groups {
		if g.Kind == sprint.Judgment {
			judg++
		} else {
			other++
		}
		b.WriteString(groupText(g))
	}
	more := ""
	if v.More {
		more += " more=notes"
	}
	if v.NoticesMore {
		more += " more=notices"
	}
	fmt.Fprintf(&b, "INBOX OK judgments=%d happened=%d cursor=%s last=%s%s\n%s\n", judg, other, v.Cursor, v.Last, more, v.Machine)
	return b.String()
}

// groupText is a group as the inbox prints it: its line, with what a
// judgment was raised on (not what is still open on it, judgmentGroups) or a
// notice's count, and its commands.
func groupText(g sprint.Group) string {
	var b strings.Builder
	count := fmt.Sprintf("x%d", g.Count)
	if g.Kind == sprint.Judgment && len(g.Notes) > 0 {
		count = fmt.Sprintf("raised on %d", g.Count)
	}
	fmt.Fprintf(&b, "%s %s %s  %s", strings.ToUpper(g.Kind), g.ID, g.Type, count)
	if len(g.Primaries) > 0 {
		ps := g.Primaries
		if len(ps) > 8 {
			ps = ps[:8]
		}
		fmt.Fprintf(&b, "  (%s)", strings.Join(ps, ","))
	}
	if g.What != "" {
		b.WriteString("  " + g.What)
	}
	if len(g.Decisions) > 0 {
		b.WriteString("  -> " + strings.Join(g.Decisions, " | "))
	}
	b.WriteString("\n")
	for _, c := range g.Commands {
		fmt.Fprintf(&b, "  %s:\n", c.Decision)
		for _, l := range c.Lines {
			fmt.Fprintf(&b, "    %s\n", l)
		}
	}
	return b.String()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- where

// WhereReq is where's request. Rows are the rows of the work and merge tables
// the caller saw last (the Rows of the view before, as where --watch keeps
// them): the counts of their cells ride the first read, and when the rows read
// in the same snapshot are those, where is one round trip.
type WhereReq struct {
	Rows WhereRows
	Out  *WhereView
}

// WhereRows are the rows of the two tables of streams, each in its order.
type WhereRows struct {
	Work, Merge []string
}

// of is a table's rows.
func (r WhereRows) of(table string) []string {
	if table == sprint.Merge {
		return r.Merge
	}
	return r.Work
}

// same says two sets of rows are the same rows, table by table.
func (r WhereRows) same(o WhereRows) bool {
	eq := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	return eq(r.Work, o.Work) && eq(r.Merge, o.Merge)
}

// WhereView is where as a program reads it, in the command's JSON shape
// (cmd/nova-sprint's where --json, which where --watch prints a frame of):
// the tables as printed, the summary, the machine's line; and beside them,
// in MachineDetail, what 1.4.1 keeps out of the view: the heartbeat's fields,
// the clock, the lease, the backlog, the due count, the parked keys and the
// dropping marks. Frame is the text view: the title, the one line, and the
// tables with rows.
type WhereView struct {
	At            time.Time                               `json:"at"`
	Landed        int64                                   `json:"landed"`
	All           int64                                   `json:"all"`
	Summary       string                                  `json:"summary"`
	Tables        map[string]map[string]map[string]string `json:"tables"`
	Streams       []sprint.StreamClock                    `json:"streams"`
	Epoch         uint64                                  `json:"epoch"`
	Machine       string                                  `json:"machine,omitempty"`
	MachineDetail WhereMachine                            `json:"machine_detail"`
	Frame         string                                  `json:"-"`
	// Rows are the rows of the work and merge tables this read found: the next
	// where's WhereReq.Rows.
	Rows WhereRows `json:"-"`
}

// WhereMachine is the machine as where --json carries it (1.4.1).
type WhereMachine struct {
	Running   bool              `json:"running"`
	R         string            `json:"r"`
	Clock     map[string]string `json:"clock"`
	Heartbeat map[string]string `json:"heartbeat"`
	Lease     map[string]string `json:"lease"`
	Cur       string            `json:"cur"`
	Backlog   string            `json:"backlog"`
	Due       int               `json:"due"`
	Parked    int               `json:"parked"`
	Dropping  map[string]string `json:"dropping"`
	DropCount int               `json:"dropping_count"`
}

// whereRender is how each table of the view is drawn, as the command draws
// it: a table of streams hides a stream with no cards.
var whereRender = map[string]ntable.RenderOpts{
	sprint.Work:  {HideZeroRows: true},
	sprint.Merge: {HideZeroRows: true},
}

// The fields the view reads of a control card: a stream's (merge's text
// columns) and a member's (fleet's).
var (
	streamCtlFields = []string{"state", "ci", "since"}
	memberCtlFields = []string{"status", "load"}
)

// The sprint answers of where's read, in order.
const (
	whereSlotClock = iota
	whereSlotLease
	whereSlotTick
	whereSlotHeartbeat
	whereSlotDue
	whereSlotParked
	whereSlotDropping
	whereSlotStreams
	whereSlotFleet
	whereSlotReaders
)

// whereRead is where's one read (section 3's row): the rows of the work and
// merge tables (AL3) and the counts of the cells of the rows given, the
// members' and readers' rows with their counts and control cards (fleet,
// readers), the streams' control cards (streams), the clock, the lease,
// {p}tick@e, the heartbeat, the due count, the parked keys' count and the
// dropping marks of the rows given.
func whereRead(names sprint.Names, rows WhereRows) func(tset.Decimal) *sprintfn.ReadRequest {
	return func(epoch tset.Decimal) *sprintfn.ReadRequest {
		rr := &sprintfn.ReadRequest{Epoch: epoch, Tset: []tset.ReadQuery{
			{Kind: "rows", Table: sprint.Work}, {Kind: "rows", Table: sprint.Merge}, {Kind: "last"}}}
		for _, t := range []string{sprint.Work, sprint.Merge} {
			if cells := countCells(names, t, rows.of(t)); len(cells) > 0 {
				rr.Tset = append(rr.Tset, tset.ReadQuery{Kind: "count", Table: t, Cells: cells})
			}
		}
		comp := func(q sprint.SprintQ) sprintfn.SprintQuery {
			sq, ref := sprintfn.EncodeSprintQ(q)
			if ref != nil {
				panic(fmt.Sprintf("verbs: where's fixed query refused: %v", ref))
			}
			return sq
		}
		drop := dedup(append(append([]string{}, rows.Work...), rows.Merge...))
		rr.Sprint = []sprintfn.SprintQuery{
			keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyClock}),
			keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyLease}),
			keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyTick}),
			keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyHeartbeat}),
			keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyDueCount}),
			keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyParked, Keys: []string{}}),
			keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyDropping, Streams: drop}),
			comp(sprint.SprintQ{Kind: sprint.QueryStreams, Fields: streamCtlFields}),
			comp(sprint.SprintQ{Kind: sprint.QueryFleet, Fields: memberCtlFields}),
			comp(sprint.SprintQ{Kind: sprint.QueryReaders, Fields: []string{}}),
		}
		return rr
	}
}

// countColumns are a table's columns whose cells where counts: every count
// column of its definition (the text, formula and first columns are not sets
// it counts).
func countColumns(names sprint.Names, table string) []string {
	for _, def := range names.Definitions() {
		if names.Logical(def.Name) != table {
			continue
		}
		var out []string
		for _, c := range def.Columns {
			if c.Projection == ntable.Count {
				out = append(out, c.Name)
			}
		}
		return out
	}
	return nil
}

// countCells are the cells of the rows' count columns, row by row.
func countCells(names sprint.Names, table string, rows []string) []string {
	cols := countColumns(names, table)
	var out []string
	for _, r := range rows {
		for _, c := range cols {
			out = append(out, r+":"+c)
		}
	}
	return out
}

// rowNames are the rows of a rows answer, in their order.
func rowNames(a tset.ReadAnswer) []string {
	rs := append([]tset.RowRank{}, a.Rows...)
	sort.SliceStable(rs, func(i, j int) bool { return compareRank(rs[i].Rank, rs[j].Rank) < 0 })
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Row
	}
	return out
}

// Where is the sprint's view (section 3, where; 1.4.1): the four tables with
// the count of every cell, the one line under the title (STOPPED; NOT TICKING
// and its seconds; or the summary, with the backlog when the machine is
// catching up), and for a program the machine beside them. It writes nothing.
// One atomic read, one round trip, when the rows of the work and merge tables
// are the ones the request names (the view before's, as where --watch keeps
// them); otherwise the read finds the rows and a second read, the same with
// their counts, draws the view from one snapshot: two round trips, and one
// more for each time the rows move between them (at most the driver's
// retries). The members and readers are counted by the fleet and readers
// queries in the same read (IT30), 250 members in one query.
func Where(ctx context.Context, e *Env, req WhereReq) (Result, error) {
	const verb = "where"
	guess := req.Rows
	var res Result
	for tries := 0; ; tries++ {
		r, err := e.Do(ctx, Planned{Verb: verb, Read: whereRead(e.Names, guess)})
		res.Trips += r.Trips
		res.Retries += r.Retries
		res.Epoch, res.Read = r.Epoch, r.Read
		if err != nil {
			return res, err
		}
		found := WhereRows{Work: rowNames(r.Read.Tset[0]), Merge: rowNames(r.Read.Tset[1])}
		if found.same(guess) {
			break
		}
		if tries >= Retries {
			return res, &Refused{Verb: verb, Retries: tries, Refusal: &sprintfn.Refusal{Code: sprintfn.CodeStale,
				Message: "the streams kept changing between where's reads"}}
		}
		guess = found
	}
	res.Verb = verb
	v, err := whereView(e.Names, res.Read, guess)
	if err != nil {
		return res, err
	}
	if req.Out != nil {
		*req.Out = v
	}
	res.Said = v.Frame
	return res, nil
}

// whereView draws the view from where's read.
func whereView(names sprint.Names, rd *sprintfn.ReadReply, rows WhereRows) (WhereView, error) {
	var v WhereView
	wall := wallOf(rd)
	v.At = time.UnixMilli(wall).UTC()
	v.Epoch, _ = strconv.ParseUint(string(rd.Epoch), 10, 64)
	v.Rows = rows
	work, merge := rows.Work, rows.Merge
	counts := map[string]map[string]uint64{sprint.Work: {}, sprint.Merge: {}}
	next := 3
	for _, t := range []string{sprint.Work, sprint.Merge} {
		cells := countCells(names, t, rows.of(t))
		if len(cells) == 0 {
			continue
		}
		a := rd.Tset[next]
		next++
		if len(a.Counts) != len(cells) {
			return v, fmt.Errorf("where: %s's count answered %d cells, asked %d", t, len(a.Counts), len(cells))
		}
		for i, c := range cells {
			counts[t][c] = a.Counts[i]
		}
	}

	clock, err := sprintAnswer[sprintfn.ClockResult](rd, whereSlotClock, sprintfn.KeyClock)
	if err != nil {
		return v, err
	}
	lease, err := sprintAnswer[sprintfn.LeaseResult](rd, whereSlotLease, sprintfn.KeyLease)
	if err != nil {
		return v, err
	}
	tick, err := sprintAnswer[sprintfn.TickResult](rd, whereSlotTick, sprintfn.KeyTick)
	if err != nil {
		return v, err
	}
	hbr, err := sprintAnswer[sprintfn.HeartbeatResult](rd, whereSlotHeartbeat, sprintfn.KeyHeartbeat)
	if err != nil {
		return v, err
	}
	due, err := sprintAnswer[sprintfn.DueCountResult](rd, whereSlotDue, sprintfn.KeyDueCount)
	if err != nil {
		return v, err
	}
	parked, err := sprintAnswer[sprintfn.ParkedResult](rd, whereSlotParked, sprintfn.KeyParked)
	if err != nil {
		return v, err
	}
	dropping, err := sprintAnswer[sprintfn.DroppingResult](rd, whereSlotDropping, sprintfn.KeyDropping)
	if err != nil {
		return v, err
	}
	streams, err := sprintAnswer[sprintfn.StreamsResult](rd, whereSlotStreams, sprint.QueryStreams)
	if err != nil {
		return v, err
	}
	fleet, err := sprintAnswer[sprintfn.ListingResult](rd, whereSlotFleet, sprint.QueryFleet)
	if err != nil {
		return v, err
	}
	readers, err := sprintAnswer[sprintfn.ListingResult](rd, whereSlotReaders, sprint.QueryReaders)
	if err != nil {
		return v, err
	}

	hb, c := machine.ParseHeartbeat(hbr.Fields), machine.ClockOf(clock)
	v.Machine = machine.MachineLine(hb, c, wall)
	m := WhereMachine{Running: !c.Stopped, R: clock.R, Clock: map[string]string{}, Heartbeat: hbr.Fields, Lease: map[string]string{},
		Due: due.Due, Parked: parked.Count, Dropping: dropping.Marks, DropCount: dropping.Count}
	for name, p := range map[string]*string{"stopped_ms": clock.Clock.StoppedMS, "stopped_since_ms": clock.Clock.StoppedSinceMS,
		"stophold_ms": clock.Clock.StopholdMS, "due_since_ms": clock.Clock.DueSinceMS, "stopraised_ms": clock.Clock.StopraisedMS} {
		if p != nil {
			m.Clock[name] = *p
		}
	}
	for name, p := range map[string]*string{"owner": lease.Owner, "name": lease.Name, "until_ms": lease.UntilMS, "gen": lease.Gen} {
		if p != nil {
			m.Lease[name] = *p
		}
	}
	if tick.Cur != nil {
		m.Cur = *tick.Cur
	}
	if len(rd.Tset) > 2 {
		last, _ := strconv.ParseUint(string(rd.Tset[2].LastSeq), 10, 64)
		cur, _ := strconv.ParseUint(m.Cur, 10, 64)
		if last > cur {
			m.Backlog = strconv.FormatUint(last-cur, 10)
		} else {
			m.Backlog = "0"
		}
	}
	v.MachineDetail = m

	// The tables, as the definitions draw them.
	ctl := map[string]map[string]string{} // stream -> its control card's fields
	for _, it := range streams.Items {
		if it.Control != nil {
			ctl[it.Stream] = recordFields(*it.Control)
		}
	}
	tables := map[string]ntable.Table{}
	for _, def := range names.Definitions() {
		t := def
		logical := names.Logical(def.Name)
		switch logical {
		case sprint.Work:
			for _, r := range work {
				t.Rows = append(t.Rows, countRow(t, r, counts[sprint.Work], nil))
			}
		case sprint.Merge:
			for _, r := range merge {
				t.Rows = append(t.Rows, countRow(t, r, counts[sprint.Merge], ctl[r]))
			}
		case sprint.Fleet:
			for _, it := range fleet.Items {
				t.Rows = append(t.Rows, listingRow(t, it))
			}
		case sprint.Readers:
			for _, it := range readers.Items {
				t.Rows = append(t.Rows, listingRow(t, it))
			}
		}
		tables[logical] = t
	}
	for _, r := range work {
		sc := sprint.StreamClock{Stream: r}
		if f, ok := ctl[r]; ok {
			sc.State = f["state"]
		}
		sc.Empty = rowEmpty(tables[sprint.Work], r)
		v.Streams = append(v.Streams, sc)
	}
	if v.Streams == nil {
		v.Streams = []sprint.StreamClock{}
	}
	v.Landed, v.All = landedOf(tables[sprint.Work])
	pct := "0.0%"
	if v.All > 0 {
		pct = strconv.FormatFloat(100*float64(v.Landed)/float64(v.All), 'f', 1, 64) + "%"
	}
	v.Summary = fmt.Sprintf("%d/%d %s -> ETA", v.Landed, v.All, pct)

	v.Tables = map[string]map[string]map[string]string{}
	var parts []string
	for _, logical := range sprint.ViewOrder {
		t := tables[logical]
		cells := map[string]map[string]string{}
		for _, r := range t.Rows {
			row := map[string]string{}
			for j, col := range t.Columns {
				row[col.Name] = ntable.CellText(t.Columns, r, j)
			}
			cells[r.Key] = row
		}
		v.Tables[logical] = cells
		opts := whereRender[logical]
		opts.Title = logical
		if out := ntable.Render(t, opts); out != "" {
			parts = append(parts, out)
		}
	}
	v.Frame = v.At.Local().Format("2006-01-02 15:04:05 MST") + "\n\nSPRINT TABLE\n\n" + viewLine(v.Summary, v.Machine) + "\n\n" + strings.Join(parts, "\n")
	return v, nil
}

// viewLine is the one line under the view's title (1.4.1): the word STOPPED,
// NOT TICKING and its seconds, or the summary with the backlog when the
// machine is catching up (T3), from IT17's MachineLine. Nothing of the
// heartbeat beside that.
func viewLine(summary, line string) string {
	switch {
	case strings.HasPrefix(line, "STOPPED"), strings.HasPrefix(line, "NOT TICKING"):
		return line
	case strings.HasPrefix(line, "running "):
		return summary + " " + strings.TrimPrefix(line, "running ")
	}
	return summary
}

// recordFields are a record's present fields.
func recordFields(r tset.MemberRecord) map[string]string {
	out := map[string]string{}
	for k, f := range r.Fields {
		if f.Present {
			out[k] = f.Value
		}
	}
	return out
}

// countRow is a row of a table of streams: each count column's cell from the
// counts read, and each text column from the stream's control card.
func countRow(t ntable.Table, row string, counts map[string]uint64, ctl map[string]string) ntable.Row {
	r := ntable.Row{Key: row, Cells: make([]ntable.Cell, len(t.Columns)), Texts: map[string]string{}}
	for j, c := range t.Columns {
		switch c.Projection {
		case ntable.Count:
			r.Cells[j].Count = int64(counts[row+":"+c.Name])
		case ntable.Text:
			r.Texts[c.Name] = ctl[c.Name]
		}
	}
	return r
}

// listingRow is a member's or a reader's row from its listing: each count
// column's cell, and each text column from its control card.
func listingRow(t ntable.Table, it sprintfn.ListingItem) ntable.Row {
	counts := map[string]int{}
	for _, c := range it.Counts {
		counts[c.Col] = c.N
	}
	var ctl map[string]string
	if it.Control != nil {
		ctl = recordFields(*it.Control)
	}
	r := ntable.Row{Key: it.Row, Cells: make([]ntable.Cell, len(t.Columns)), Texts: map[string]string{}}
	for j, c := range t.Columns {
		switch c.Projection {
		case ntable.Count:
			r.Cells[j].Count = int64(counts[c.Name])
		case ntable.Text:
			r.Texts[c.Name] = ctl[c.Name]
		}
	}
	return r
}

// rowEmpty says a row of the table has no card in any count column.
func rowEmpty(t ntable.Table, row string) bool {
	for _, r := range t.Rows {
		if r.Key != row {
			continue
		}
		for j, c := range t.Columns {
			if c.Projection == ntable.Count && r.Cells[j].Count > 0 {
				return false
			}
		}
	}
	return true
}

// landedOf is the landed primaries and all of them, from the work table: as
// the command's summary counts them.
func landedOf(t ntable.Table) (landed, all int64) {
	for _, r := range t.Rows {
		for j, c := range t.Columns {
			if c.Projection != ntable.Count {
				continue
			}
			all += r.Cells[j].Count
			if c.Name == sprint.Landed {
				landed += r.Cells[j].Count
			}
		}
	}
	return landed, all
}

// ---- card

// cardFields are the fields card shows of each record it reads: what the rules
// read of a card (1.3.1), its stream and its words.
var cardFields = func() []string {
	fs := append([]string{}, sprint.IndexFields()...)
	for _, f := range []string{"stream", sprint.PrimaryField, "attempt", "result", "head", "ci", "ci_head", "rcards", "member", "reader",
		"report", "finding", "reason", "state", "status"} {
		if !contains(fs, f) {
			fs = append(fs, f)
		}
	}
	sort.Strings(fs)
	return fs
}()

// CardReq is card's request: the card, and where its view goes.
type CardReq struct {
	ID  string
	Out *CardView
}

// CardView is everything one read finds about a card (section 3, card): its
// record and what `related` follows from it (its live and withdrawn work
// cards, its read cards, its merge card, its stream's control card, its
// member's, its needs, its judgments' count, its due entries and its
// indexes). Quarantined says the card is in {p}quarantine@e (1.3.5): every
// sprint query leaves it out, so nothing of it is read; NotShown says what
// of it the view cannot show, and why.
type CardView struct {
	ID          string             `json:"id"`
	Record      *tset.MemberRecord `json:"record,omitempty"`
	Follows     *sprintfn.Follows  `json:"follows,omitempty"`
	Quarantined bool               `json:"quarantined"`
	NotShown    string             `json:"not_shown,omitempty"`
}

// Card shows one card (section 3, card): one read, one round trip, of
// `related` over the card with every follow of 1.0 (IT30). A quarantined card
// is shown as quarantined, left out of every read (1.3.5); what
// {p}quarantine@e kept of its refusal (the code, the rule, the cells) has no
// read in IT30's sprint-key kinds yet, and its history (Layer 2's cardlines)
// no read on the twin: both are named on the pull request.
func Card(ctx context.Context, e *Env, req CardReq) (Result, error) {
	const verb = "card"
	if !sprint.ValidID(req.ID) && !validCardID(req.ID) {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a card id", req.ID)
	}
	q, ref := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Fields: cardFields,
		Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: []string{req.ID}}, Follow: append([]string{}, sprint.Follows...)})
	if ref != nil {
		return Result{Verb: verb}, &Refused{Verb: verb, Refusal: ref, Local: true}
	}
	res, err := e.Do(ctx, Planned{Verb: verb, Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
		return &sprintfn.ReadRequest{Epoch: epoch, Sprint: []sprintfn.SprintQuery{q}}
	}})
	if err != nil {
		return res, err
	}
	rel, err := sprintAnswer[sprintfn.RelatedResult](res.Read, 0, sprint.QueryRelated)
	if err != nil {
		return res, err
	}
	v := CardView{ID: req.ID, Quarantined: contains(rel.LeftOut, req.ID)}
	if v.Quarantined {
		v.NotShown = quarantineUnshown
	}
	for _, it := range rel.Items {
		if it.ID == req.ID {
			rec := it.Record
			v.Record, v.Follows = &rec, it.Follows
		}
	}
	if req.Out != nil {
		*req.Out = v
	}
	switch {
	case v.Quarantined:
		res.Said = fmt.Sprintf("card %s is quarantined: a lower layer refused it, and every read leaves it out (1.3.5); %s", req.ID, quarantineUnshown)
	case v.Record == nil || !v.Record.Exists:
		res.Said = fmt.Sprintf("card %s has no record", req.ID)
	default:
		place := ""
		if v.Record.Place != nil {
			place = v.Record.Place.Row + ":" + v.Record.Place.Col
		}
		res.Said = fmt.Sprintf("card %s at %s, revision %s", req.ID, place, v.Record.Revision)
	}
	return res, nil
}

// quarantineUnshown is what card cannot show of a quarantined card, and why:
// the design's row shows what {p}quarantine@e kept of the refusal (code, rule,
// cells), and no read of the query set reads that key yet (IT30's list of
// sprint-key reads, errata 1); the card's history is cardlines, which the
// twin does not serve yet.
const quarantineUnshown = "the refusal's code, rule and cells are not shown: no read of the quarantine record exists yet (owed by the sprint-key reads, IT30)"

// cardIDRE is a card id of the sprint's alphabet: a primary and its derived
// parts (<p>.w<k>, <p>.r<k>.<reader>).
var cardIDRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,255}$`)

func validCardID(id string) bool { return cardIDRE.MatchString(id) }

// ---- log

// LogPage is the most lines one page of log --since or --stream reads (Layer
// 2's lines page takes up to 5,000, L2 4).
const LogPage = 1000

// LogCardPage is the most lines one page of log --card reads: Layer 1's and
// Layer 2's bound on a cardlines page (L1 7; L2 4: at most 500 lines across
// the page's slots).
const LogCardPage = 500

// LogReq is log's request: --card, --stream or --since (the seq after which
// to read), a page's size (0 is LogPage, or LogCardPage for --card), the
// cursor of --card's next page, and where the view goes.
type LogReq struct {
	Card   string
	Stream string
	Since  uint64
	// CardCursor is --card's next page: the CardNext of the page before (L2
	// 4's cursor, {epoch, fields, include_meta, positions}); nil is the first.
	CardCursor *tset.CardCursor
	Limit      int
	Out        *LogView
}

// LogView is one page of the log: the lines (Layer 2's semantic lines, L2 4)
// and, for --stream, only the stream's; the epoch they are of; Next, the seq
// to read after for the next page of --since or --stream, and CardNext, the
// cursor of --card's next page; Exhausted says the page reached the last line.
type LogView struct {
	Lines     []json.RawMessage `json:"lines"`
	Epoch     uint64            `json:"epoch"`
	Next      uint64            `json:"next"`
	CardNext  *tset.CardCursor  `json:"card_next,omitempty"`
	Exhausted bool              `json:"exhausted"`
}

// Log reads the log (section 3, log): --since and --stream read Layer 2's
// lines in pages after a seq (L2 4), --stream keeping the lines of the
// stream; --card reads the card's lines through Layer 2's cardlines in pages
// of at most 500, each page's cursor the next's (L2 4). One round trip a
// page. The page is read at the Env's epoch, and a page that names another
// active epoch is read again at it (the Env's epoch moves, as a verb's read
// moves it, AL2): one round trip more when the Env was behind a clear. A
// --card page after the first is read at its cursor's epoch, which the store
// keeps until teardown. A page item of cardlines is refused REQUEST by the
// write path's client until its page item admits cardlines beside lines
// (sprintfn's checkPage; errata 1 E2 names lines only), and the twin serves
// no cardlines until Layer 2's log twin does (J9).
func Log(ctx context.Context, e *Env, req LogReq) (Result, error) {
	const verb = "log"
	res := Result{Verb: verb, Epoch: e.epoch()}
	if req.Card != "" && req.Stream != "" {
		return res, refuseLocal(verb, sprintfn.CodeRequest, "log takes --card or --stream, not both")
	}
	var q tset.ReadQuery
	if req.Card != "" {
		if !validCardID(req.Card) {
			return res, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a card id", req.Card)
		}
		limit := req.Limit
		if limit <= 0 || limit > LogCardPage {
			limit = LogCardPage
		}
		q = tset.ReadQuery{Kind: "cardlines", Abouts: []string{req.Card}, Limit: limit, IncludeMeta: true, Cursor: req.CardCursor}
	} else {
		if req.CardCursor != nil {
			return res, refuseLocal(verb, sprintfn.CodeRequest, "a cardlines cursor is --card's")
		}
		if req.Stream != "" && !sprint.ValidID(req.Stream) {
			return res, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a stream", req.Stream)
		}
		limit := req.Limit
		if limit <= 0 || limit > LogPage {
			limit = LogPage
		}
		q = tset.ReadQuery{Kind: "lines", AfterSeq: tset.Decimal(strconv.FormatUint(req.Since, 10)), Limit: limit}
	}
	page, epoch, err := logPage(ctx, e, &res, q, req.CardCursor)
	if err != nil {
		return res, err
	}
	res.Epoch = epoch
	v := LogView{Epoch: epoch, Next: req.Since, Exhausted: page.Exhausted}
	if req.Card != "" {
		v.Lines = page.Items
		if len(page.Next) > 0 && string(page.Next) != "null" {
			var c tset.CardCursor
			if err := json.Unmarshal(page.Next, &c); err != nil {
				return res, fmt.Errorf("log --card: the page's cursor: %w", err)
			}
			v.CardNext = &c
		}
	} else {
		if len(page.Next) > 0 && string(page.Next) != "null" {
			var next string
			if json.Unmarshal(page.Next, &next) != nil {
				next = string(page.Next)
			}
			n, err := strconv.ParseUint(next, 10, 64)
			if err != nil {
				return res, fmt.Errorf("log: the page's next %s is not a seq", page.Next)
			}
			v.Next = n
		}
		for _, raw := range page.Items {
			if req.Stream != "" {
				var l struct {
					Seq string `json:"seq"`
				}
				if json.Unmarshal(raw, &l) != nil {
					continue
				}
				ev, err := sprint.ParseEvent(l.Seq+"-0", raw)
				if err != nil || !lineOfStream(ev, req.Stream) {
					continue
				}
			}
			v.Lines = append(v.Lines, raw)
		}
	}
	if v.Lines == nil {
		v.Lines = []json.RawMessage{}
	}
	if req.Out != nil {
		*req.Out = v
	}
	var b strings.Builder
	for _, l := range v.Lines {
		b.Write(l)
		b.WriteString("\n")
	}
	res.Said = b.String()
	return res, nil
}

// logPage reads one page of the log: at the cursor's epoch when there is one,
// else at the Env's, moving to the active epoch a reply names (STALE and
// EPOCHAHEAD reload it; a page of an older epoch than the active one is read
// again at the active one), at most Retries times.
func logPage(ctx context.Context, e *Env, res *Result, q tset.ReadQuery, cursor *tset.CardCursor) (tset.ReadReply, uint64, error) {
	const verb = "log"
	pinned := cursor != nil
	epoch := e.epoch()
	if pinned {
		n, ok := undec(cursor.Epoch)
		if !ok {
			return tset.ReadReply{}, 0, refuseLocal(verb, sprintfn.CodeRequest, "the cursor's epoch %q is not an epoch", cursor.Epoch)
		}
		epoch = n
	}
	for tries := 0; ; tries++ {
		out, err := e.C.Pipeline(ctx, []sprintfn.Item{{Page: &tset.ReadPlan{Epoch: dec(epoch), Space: e.Names.Prefix, Mode: "page",
			Queries: []tset.ReadQuery{q}}}})
		res.Trips++
		if err != nil {
			return tset.ReadReply{}, epoch, err
		}
		if len(out) != 1 {
			return tset.ReadReply{}, epoch, fmt.Errorf("log: a pipeline of one returned %d results", len(out))
		}
		if out[0].Err != nil {
			return tset.ReadReply{}, epoch, out[0].Err
		}
		if ref := out[0].Refusal; ref != nil {
			if !pinned && epochMoved(ref.Code) && tries < Retries && e.reload(ref) {
				res.Retries++
				epoch = e.epoch()
				continue
			}
			return tset.ReadReply{}, epoch, &Refused{Verb: verb, Refusal: ref, Retries: res.Retries}
		}
		page := out[0].Page
		if page == nil {
			return tset.ReadReply{}, epoch, errors.New("log: the page has no reply")
		}
		if active, ok := undec(page.ActiveEpoch); ok && active != epoch && !pinned {
			if tries >= Retries {
				return tset.ReadReply{}, epoch, &Refused{Verb: verb, Retries: tries, Refusal: &sprintfn.Refusal{Code: sprintfn.CodeStale,
					Message: "the epoch kept moving under log"}}
			}
			res.Retries++
			e.setEpoch(active)
			epoch = active
			continue
		}
		return *page, epoch, nil
	}
}

// lineOfStream says a line is the stream's: its stream, or a place in its row
// of the work or merge table.
func lineOfStream(ev sprint.Event, stream string) bool {
	if ev.Stream == stream {
		return true
	}
	for _, p := range []string{ev.From, ev.To} {
		if row, _, ok := strings.Cut(p, ":"); ok && row == stream && (ev.Table == sprint.Work || ev.Table == sprint.Merge) {
			return true
		}
	}
	return false
}
