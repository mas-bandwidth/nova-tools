package tset

import (
	"encoding/json"
	"sort"
	"strconv"
	"sync"
)

// MemLog is Layer 2's Go twin (work item J9): the log of each epoch in memory,
// with the stored lines of table_set_log.lua byte for byte (LogLines), the
// seq as the stream id, and each primary's history list of seqs. It composes
// with Mem: Mem plans the tables, MemLog plans the lines from that plan, and
// the caller commits both or neither. It serves the log's reads, last, lines
// and cardlines, as the Lua does (L2 2, 4, 6; L1 7), with the store's
// refusals: LOGID, CURSOR, BUDGET, DRIFT, OVERFLOW. The model is
// tla/SetTableLog.tla.
//
// The byte budgets of a read are the Lua's in shape (lines and ids charged
// before a line is returned, a line never cut, 7 maximum lines a fetch) but
// the fetched-byte count is the twin's estimate of the store's payload.
type MemLog struct {
	mu   sync.Mutex
	logs map[string]*memLogEpoch
}

type memLogEpoch struct {
	exists  bool   // the stream key exists
	added   uint64 // entries-added
	length  uint64 // the entries the stream holds
	autoID  bool   // a raw XADD * made the last id something other than <added>-0
	lines   map[uint64]LogLine
	history map[string][]Decimal
}

// The live ceiling of a seq (L2 2).
const logSeqCeiling = 9007199254740991

// Read bounds (L1 6, 7; L2 4, 6).
const (
	logLineRaw     = MaxLineBytes + 64
	logMaxReply    = 8 << 20
	logEnvelope    = 4096
	logItemCap     = 512 << 10
	logReadIDs     = 200000
	logReadFields  = 1280000
	logFetchedCap  = 8 << 20
	logDefaultUsed = 512
)

// NewMemLog is an empty log.
func NewMemLog() *MemLog { return &MemLog{logs: map[string]*memLogEpoch{}} }

func memLogKey(space string, epoch Decimal) string { return space + "\x00" + string(epoch) }

func (l *MemLog) epoch(space string, epoch Decimal, create bool) *memLogEpoch {
	lg := l.logs[memLogKey(space, epoch)]
	if lg == nil && create {
		lg = &memLogEpoch{lines: map[uint64]LogLine{}, history: map[string][]Decimal{}}
		l.logs[memLogKey(space, epoch)] = lg
	}
	return lg
}

// LogPlan is Layer 2's log_plan (L1 1.3): the seqs, the counts, each note's
// seq in the step's note order, the lines with their seqs, and the planned
// commands and argv bytes the shared bounds count.
type LogPlan struct {
	FirstSeq, LastSeq Decimal
	LineCount         int
	AboutAppends      int
	NoteSeqs          []Decimal
	Lines             []LogLine
	Commands          int
	ArgvBytes         int
}

// Plan plans one step's lines on the epoch it writes, writing nothing: the
// lines of LogLines, their seqs from the epoch's head, and one RPUSH per
// history key with its seqs ascending (L2 1.2, 2). The returned apply appends
// them; the caller runs it only when the step commits. A step with no line
// plans nothing and reads nothing.
func (l *MemLog) Plan(space string, in LogLinesInput) (LogPlan, func(), *Refusal) {
	lines, ref := LogLines(in)
	if ref != nil {
		return LogPlan{}, nil, ref
	}
	plan := LogPlan{FirstSeq: "0", LastSeq: "0", NoteSeqs: make([]Decimal, len(in.Notes))}
	if len(lines) == 0 {
		return plan, func() {}, nil
	}
	var order []string
	seen := map[string]bool{}
	for _, line := range lines {
		for _, a := range line.About {
			if !seen[a] {
				seen[a] = true
				order = append(order, a)
			}
		}
	}
	l.mu.Lock()
	lg := l.epoch(space, in.WriteEpoch, false)
	head, ref := writeHead(lg, in.WriteEpoch != in.RequestEpoch, order)
	l.mu.Unlock()
	if ref != nil {
		return LogPlan{}, nil, ref
	}
	if head+uint64(len(lines)) > logSeqCeiling {
		return LogPlan{}, nil, NewRefusal("OVERFLOW", RefusalDetail{Budget: "log_seq"})
	}
	key := space + "sprint:log@" + string(in.WriteEpoch)
	byAbout := map[string][]Decimal{}
	for i, line := range lines {
		seq := Decimal(strconv.FormatUint(head+uint64(i)+1, 10))
		plan.Lines = append(plan.Lines, LogLine{Seq: seq, N: line.N, D: line.D})
		plan.Commands++
		plan.ArgvBytes += len("XADD") + len(key) + len(seq) + len("-0") + len("n") + len(line.N) + len("d") + len(line.D)
		for _, a := range line.About {
			byAbout[a] = append(byAbout[a], seq)
			plan.AboutAppends++
		}
		if line.Note >= 0 {
			plan.NoteSeqs[line.Note] = seq
		}
	}
	for _, a := range order {
		plan.Commands++
		plan.ArgvBytes += len("RPUSH") + len(space+"sprint:cl:"+a+"@"+string(in.WriteEpoch))
		for _, seq := range byAbout[a] {
			plan.ArgvBytes += len(seq)
		}
	}
	plan.LineCount = len(lines)
	plan.FirstSeq, plan.LastSeq = plan.Lines[0].Seq, plan.Lines[len(lines)-1].Seq
	stored := append([]LogLine(nil), plan.Lines...)
	apply := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		lg := l.epoch(space, in.WriteEpoch, true)
		lg.exists = true
		for _, line := range stored {
			n, _ := strconv.ParseUint(string(line.Seq), 10, 64)
			lg.lines[n] = line
			lg.added, lg.length = n, lg.length+1
		}
		for _, a := range order {
			lg.history[a] = append(lg.history[a], byAbout[a]...)
		}
	}
	return plan, apply, nil
}

// writeHead is the head of the log a step appends to (table_set_log.lua's
// write_head with Layer 1's prepare check of the stream head): an absent log
// is a fresh epoch at 0 unless a touched history exists (DRIFT); an advance's
// new log must be absent (DRIFT); the last id must be <entries-added>-0 and
// the stream must hold every entry it added (LOGID).
func writeHead(lg *memLogEpoch, advancing bool, abouts []string) (uint64, *Refusal) {
	if lg == nil || !lg.exists {
		if lg != nil {
			for _, a := range abouts {
				if len(lg.history[a]) != 0 {
					return 0, NewRefusal("DRIFT", RefusalDetail{IDs: []string{a}})
				}
			}
		}
		return 0, nil
	}
	if advancing {
		return 0, NewRefusal("DRIFT", RefusalDetail{})
	}
	if lg.added > logSeqCeiling {
		return 0, NewRefusal("LOGID", RefusalDetail{Budget: "entries_added"})
	}
	if lg.autoID {
		return 0, NewRefusal("LOGID", RefusalDetail{Budget: "last_generated_id"})
	}
	if lg.length != lg.added {
		return 0, NewRefusal("LOGID", RefusalDetail{Budget: "length"})
	}
	return lg.added, nil
}

// readTail is the checked tail a read sees: "0" for an absent log, LOGID for
// a head that is not the equality or a stream missing an entry.
func readTail(lg *memLogEpoch) (uint64, *Refusal) {
	if lg == nil || !lg.exists {
		return 0, nil
	}
	if lg.added > logSeqCeiling {
		return 0, NewRefusal("LOGID", RefusalDetail{Budget: "entries_added"})
	}
	if lg.autoID {
		return 0, NewRefusal("LOGID", RefusalDetail{Budget: "last_generated_id"})
	}
	if lg.length != lg.added {
		return 0, NewRefusal("LOGID", RefusalDetail{Budget: "length"})
	}
	return lg.added, nil
}

// Read serves one Layer 2 query, last, lines or cardlines, as a page or one
// atomic query, at the plan's epoch (L1 7; L2 4, 6). A refusal carries the
// query's index, 0, and writes nothing.
func (l *MemLog) Read(space string, plan ReadPlan) (ReadReply, *Refusal) {
	fail := func(code string, d RefusalDetail) (ReadReply, *Refusal) {
		d.QueryIndex = memIndex(0)
		return ReadReply{}, NewRefusal(code, d)
	}
	if len(plan.Queries) != 1 {
		return fail("REQUEST", RefusalDetail{})
	}
	q := plan.Queries[0]
	page := plan.Mode == "page"
	l.mu.Lock()
	defer l.mu.Unlock()
	lg := l.epoch(space, plan.Epoch, false)
	switch q.Kind {
	case "last":
		if page {
			return fail("REQUEST", RefusalDetail{})
		}
		tail, ref := readTail(lg)
		if ref != nil {
			return fail(ref.Code, ref.Detail)
		}
		return ReadReply{Status: "read", Epoch: plan.Epoch, Complete: true,
			Answers: []ReadAnswer{{Kind: "last", LastSeq: Decimal(strconv.FormatUint(tail, 10))}}}, nil
	case "lines":
		return l.lines(lg, plan, q, page, fail)
	case "cardlines":
		return l.cardlines(space, lg, plan, q, page, fail)
	}
	return fail("REQUEST", RefusalDetail{})
}

func quoted(s string) json.RawMessage { return json.RawMessage(strconv.Quote(s)) }

// lineItem is a lines item {seq, n, d} with d verbatim, as cjson writes it.
func lineItem(line LogLine) json.RawMessage {
	return json.RawMessage(`{"seq":` + cjsonString(string(line.Seq)) + `,"n":` + cjsonString(line.N) +
		`,"d":` + cjsonString(line.D) + `}`)
}

func (l *MemLog) lines(lg *memLogEpoch, plan ReadPlan, q ReadQuery, page bool,
	fail func(string, RefusalDetail) (ReadReply, *Refusal)) (ReadReply, *Refusal) {
	tail, ref := readTail(lg)
	if ref != nil {
		return fail(ref.Code, ref.Detail)
	}
	after, err := strconv.ParseUint(string(q.AfterSeq), 10, 64)
	if err != nil {
		return fail("REQUEST", RefusalDetail{})
	}
	high := tail
	if q.ThroughSeq != nil {
		through, err := strconv.ParseUint(string(*q.ThroughSeq), 10, 64)
		if err != nil {
			return fail("REQUEST", RefusalDetail{})
		}
		if through > tail {
			return fail("CURSOR", RefusalDetail{Budget: "through_seq"})
		}
		high = through
	}
	if after > high {
		return fail("CURSOR", RefusalDetail{Budget: "after_seq"})
	}
	idsCap := q.IDsLimit
	if idsCap == 0 {
		idsCap = logReadIDs
	}
	room := logMaxReply - logDefaultUsed - 1 - logEnvelope
	items := []json.RawMessage{}
	cur, ids, bytes, fetched := after, 0, 0, 0
	stop := ""
	for cur < high && len(items) < q.Limit {
		if logFetchedCap-fetched < logLineRaw {
			stop = "fetched_bytes"
			break
		}
		line, ok := lg.lines[cur+1]
		if !ok {
			return fail("LOGID", RefusalDetail{Budget: "log_gap"})
		}
		fetched += len(line.Seq) + 2 + 2 + len(line.N) + len(line.D)
		count, _ := strconv.Atoi(line.N)
		if count > idsCap-ids {
			stop = "log_id"
			break
		}
		item := lineItem(line)
		if bytes+len(item)+1 > room {
			stop = "encoded_reply"
			break
		}
		items = append(items, item)
		bytes, ids, cur = bytes+len(item)+1, ids+count, cur+1
	}
	if len(items) == 0 && cur < high {
		if stop == "" {
			stop = "fetched_bytes"
		}
		return fail("BUDGET", RefusalDetail{Budget: stop})
	}
	next := strconv.FormatUint(cur+1, 10)
	if page {
		return ReadReply{Status: "page", Epoch: plan.Epoch, Items: items, Next: quoted(next),
			Through: quoted(strconv.FormatUint(high, 10)), Exhausted: cur == high}, nil
	}
	through := "0"
	if len(items) != 0 {
		through = strconv.FormatUint(cur, 10)
	}
	return ReadReply{Status: "read", Epoch: plan.Epoch, Complete: true,
		Answers: []ReadAnswer{{Kind: "lines", Lines: items, Next: quoted(next), Through: quoted(through)}}}, nil
}

// cardSlot is one about's slot of a cardlines answer.
type cardSlot struct {
	About string            `json:"about"`
	Lines []json.RawMessage `json:"lines"`
}

type cardPos struct {
	about                   string
	next, through, nextItem int64
}

func (l *MemLog) cardlines(space string, lg *memLogEpoch, plan ReadPlan, q ReadQuery, page bool,
	fail func(string, RefusalDetail) (ReadReply, *Refusal)) (ReadReply, *Refusal) {
	seen := map[string]bool{}
	for _, a := range q.Abouts {
		if seen[a] {
			return fail("REQUEST", RefusalDetail{IDs: []string{a}})
		}
		seen[a] = true
	}
	fields := append([]string{}, q.Fields...)
	if c := q.Cursor; c != nil {
		same := c.Epoch == plan.Epoch && c.IncludeMeta == q.IncludeMeta && len(c.Fields) == len(fields) &&
			len(c.Positions) == len(q.Abouts)
		for i := 0; same && i < len(fields); i++ {
			same = c.Fields[i] == fields[i]
		}
		for i := 0; same && i < len(q.Abouts); i++ {
			same = c.Positions[i].About == q.Abouts[i]
		}
		if !same {
			return fail("CURSOR", RefusalDetail{})
		}
	}
	positions := make([]cardPos, len(q.Abouts))
	slots := make([]cardSlot, len(q.Abouts))
	for i, a := range q.Abouts {
		var hist []Decimal
		if lg != nil {
			hist = lg.history[a]
		}
		llen := int64(len(hist))
		pos := cardPos{about: a, through: llen - 1}
		if q.Cursor != nil {
			c := q.Cursor.Positions[i]
			if c.ThroughIndex > llen-1 || c.NextIndex > c.ThroughIndex+1 || c.NextItem < 0 ||
				(c.NextItem > 0 && c.NextIndex > c.ThroughIndex) {
				return fail("CURSOR", RefusalDetail{IDs: []string{a}})
			}
			pos.next, pos.through, pos.nextItem = c.NextIndex, c.ThroughIndex, c.NextItem
		}
		positions[i] = pos
		slots[i] = cardSlot{About: a, Lines: []json.RawMessage{}}
	}
	fieldsJSON, _ := json.Marshal(fields)
	fixed := 2*len(fieldsJSON) + 128
	for _, a := range q.Abouts {
		fixed += 2*len(cjsonString(a)) + 160
	}
	room := logMaxReply - logDefaultUsed - 1 - logEnvelope - fixed
	total, bytes, logIDs, fieldCharge, fetched := 0, 0, 0, 0, 0
	fetchedSeqs := map[Decimal]bool{}
	stop := ""
	var stopActual int64
	for i := range positions {
		pos := &positions[i]
		for stop == "" && pos.next <= pos.through {
			if total >= q.Limit {
				stop = "limit"
				break
			}
			seq := lg.history[pos.about][pos.next]
			n, _ := strconv.ParseUint(string(seq), 10, 64)
			line, ok := lg.lines[n]
			if !ok {
				return fail("LOGID", RefusalDetail{Budget: "log_line"})
			}
			if !fetchedSeqs[seq] {
				if logFetchedCap-fetched < logLineRaw {
					stop = "fetched_bytes"
					break
				}
				fetched += len(line.Seq) + 2 + 2 + len(line.N) + len(line.D)
				fetchedSeqs[seq] = true
			}
			projected, ref := ProjectLine(line, pos.about, fields, q.IncludeMeta)
			if ref != nil {
				return fail(ref.Code, ref.Detail)
			}
			if pos.nextItem >= int64(len(projected)) {
				return fail("CURSOR", RefusalDetail{IDs: []string{pos.about}})
			}
			for j := pos.nextItem; j < int64(len(projected)); j++ {
				if total >= q.Limit {
					stop = "limit"
					break
				}
				item := projected[j]
				size := len(item)
				if size > logItemCap {
					stop, stopActual = "cardlines_item", int64(size)
					break
				}
				if bytes+size+1 > room {
					stop = "encoded_reply"
					break
				}
				charge := 0
				if isMemberItem(item) {
					charge = len(fields)
				}
				if logReadIDs-logIDs < 1 {
					stop = "log_id"
					break
				}
				if logReadFields-fieldCharge < charge {
					stop = "field"
					break
				}
				logIDs++
				fieldCharge += charge
				slots[i].Lines = append(slots[i].Lines, item)
				total, bytes, pos.nextItem = total+1, bytes+size+1, j+1
			}
			if stop != "" {
				break
			}
			pos.next, pos.nextItem = pos.next+1, 0
		}
		if stop != "" {
			break
		}
	}
	exhausted := true
	for _, pos := range positions {
		if pos.next <= pos.through {
			exhausted = false
		}
	}
	budget := func() (ReadReply, *Refusal) {
		d := RefusalDetail{Budget: stop}
		if stop == "" {
			d.Budget = "limit"
		}
		if stop == "cardlines_item" {
			d.Actual, d.Limit = memInt64(stopActual), memInt64(logItemCap)
		}
		return fail("BUDGET", d)
	}
	raw := make([]json.RawMessage, len(slots))
	for i, s := range slots {
		raw[i], _ = json.Marshal(s)
	}
	if !page {
		if !exhausted {
			return budget()
		}
		answer, _ := json.Marshal(map[string]any{"kind": "cardlines", "lines": slots, "next": nil, "through": nil})
		return ReadReply{Status: "read", Epoch: plan.Epoch, Complete: true,
			Answers: []ReadAnswer{{Kind: "cardlines", Lines: raw, Raw: answer}}}, nil
	}
	if total == 0 && !exhausted {
		return budget()
	}
	through := make([]int64, len(positions))
	cursor := CardCursor{Epoch: plan.Epoch, Fields: fields, IncludeMeta: q.IncludeMeta}
	for i, pos := range positions {
		through[i] = pos.through
		cursor.Positions = append(cursor.Positions, CardCursorPosition{About: pos.about, NextIndex: pos.next,
			ThroughIndex: pos.through, NextItem: pos.nextItem})
	}
	next := json.RawMessage("null")
	if !exhausted {
		next, _ = json.Marshal(cursor)
	}
	throughJSON, _ := json.Marshal(through)
	return ReadReply{Status: "page", Epoch: plan.Epoch, Items: raw, Next: next, Through: throughJSON,
		Exhausted: exhausted}, nil
}

func isMemberItem(item json.RawMessage) bool {
	var probe struct {
		ID *string `json:"id"`
	}
	return json.Unmarshal(item, &probe) == nil && probe.ID != nil
}

// Stored is a copy of an epoch's stored lines in seq order.
func (l *MemLog) Stored(space string, epoch Decimal) []LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	lg := l.epoch(space, epoch, false)
	if lg == nil {
		return nil
	}
	seqs := make([]uint64, 0, len(lg.lines))
	for n := range lg.lines {
		seqs = append(seqs, n)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	out := make([]LogLine, len(seqs))
	for i, n := range seqs {
		out[i] = lg.lines[n]
	}
	return out
}

// History is a copy of one primary's list of seqs at an epoch.
func (l *MemLog) History(space string, epoch Decimal, about string) []Decimal {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lg := l.epoch(space, epoch, false); lg != nil {
		return append([]Decimal(nil), lg.history[about]...)
	}
	return nil
}

// Histories is a copy of every primary's list at an epoch.
func (l *MemLog) Histories(space string, epoch Decimal) map[string][]Decimal {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string][]Decimal{}
	if lg := l.epoch(space, epoch, false); lg != nil {
		for a, h := range lg.history {
			out[a] = append([]Decimal(nil), h...)
		}
	}
	return out
}

// The faults a writer outside the supported one can leave, for the refusal
// tests (L2 2, 8): each changes the twin as the raw store command would.

// PlantAutoID is a raw XADD * of one entry: entries-added and the length grow,
// and the last id is no longer <entries-added>-0.
func (l *MemLog) PlantAutoID(space string, epoch Decimal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lg := l.epoch(space, epoch, true)
	lg.exists, lg.autoID = true, true
	lg.added++
	lg.length++
}

// DeleteLine is a raw XDEL of one seq: the length drops and entries-added
// stays.
func (l *MemLog) DeleteLine(space string, epoch Decimal, seq Decimal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lg := l.epoch(space, epoch, false)
	n, err := strconv.ParseUint(string(seq), 10, 64)
	if lg == nil || err != nil {
		return
	}
	if _, ok := lg.lines[n]; ok {
		delete(lg.lines, n)
		lg.length--
	}
}

// DeleteLog is a raw DEL of the stream: the histories stay.
func (l *MemLog) DeleteLog(space string, epoch Decimal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lg := l.epoch(space, epoch, false); lg != nil {
		lg.exists, lg.added, lg.length, lg.autoID = false, 0, 0, false
		lg.lines = map[uint64]LogLine{}
	}
}

// SetHead makes the stream's entries-added and length n with no stored line,
// as a long-lived log would stand, for the ceiling's tests.
func (l *MemLog) SetHead(space string, epoch Decimal, n uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lg := l.epoch(space, epoch, true)
	lg.exists, lg.added, lg.length = true, n, n
}
