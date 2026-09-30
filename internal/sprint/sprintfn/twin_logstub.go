package sprintfn

import (
	"encoding/json"
	"strconv"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// LogStub stands in for Layer 2's Go log twin (item J9, errata E7.2) until
// that lands, and this file is deleted with it. It keeps the lines of each
// epoch in memory, seq as the stream id, and each primary's history list of
// seqs: enough for the write path's tests. Its line is a plain semantic
// object, not Layer 2's compact stored line, and it serves last and lines
// but not cardlines.
type LogStub struct {
	mu   sync.Mutex
	logs map[string]*stubLog
}

type stubLog struct {
	lines   []json.RawMessage // line q at index q-1
	ids     []int             // the ids each line names, for a page's ids budget
	history map[string][]tset.Decimal
}

// maxSeq is the live sequence ceiling (L2 2: 2^53 - 1).
const maxSeq = 9007199254740991

// NewLogStub is an empty log.
func NewLogStub() *LogStub { return &LogStub{logs: map[string]*stubLog{}} }

func logKey(prefix string, epoch tset.Decimal) string { return prefix + "\x00" + string(epoch) }

// stubLine is the stub's line, before its seq is known.
type stubLine struct {
	Seq   string          `json:"seq"`
	Kind  string          `json:"kind"`
	AtMS  tset.Decimal    `json:"at_ms"`
	Table string          `json:"table,omitempty"`
	From  string          `json:"from,omitempty"`
	To    string          `json:"to,omitempty"`
	IDs   []string        `json:"ids,omitempty"`
	About []string        `json:"about,omitempty"`
	Add   []tset.RowRank  `json:"add,omitempty"`
	Del   []string        `json:"del,omitempty"`
	Meta  json.RawMessage `json:"meta,omitempty"`
}

// Plan plans one line per emitting entry, in the table plan's order, then one
// per note (L2 0, 1): a create, move or remove with an effective id, a rows
// entry that added or deleted a row, and an advance. Guards, counts and
// rowsets emit none. A line over 1 MiB or 2,000 ids is LIMIT, a step past the
// seq ceiling OVERFLOW, and nothing is written until the returned LogApply
// runs.
func (l *LogStub) Plan(in LogInput) (LogPlan, LogApply, *Refusal) {
	l.mu.Lock()
	last := 0
	if lg := l.logs[logKey(in.Prefix, in.Epoch)]; lg != nil {
		last = len(lg.lines)
	}
	l.mu.Unlock()

	var lines []stubLine
	for _, pe := range in.Table.Entries {
		e := pe.Entry
		switch e.Kind {
		case "create", "move", "remove":
			if len(e.IDs) != 0 {
				lines = append(lines, stubLine{Kind: e.Kind, Table: e.Table, From: e.From, To: e.To,
					IDs: e.IDs, About: dedup(e.About), Meta: e.Meta})
			}
		case "rows":
			if len(pe.Added) != 0 || len(e.Del) != 0 {
				lines = append(lines, stubLine{Kind: "rows", Table: e.Table, Add: pe.Added, Del: e.Del})
			}
		case "advance":
			lines = append(lines, stubLine{Kind: "advance", From: string(e.AdvanceFrom), To: string(in.Epoch)})
		}
	}
	firstNote := len(lines)
	for _, n := range in.Notes {
		lines = append(lines, stubLine{Kind: "note", About: dedup(n.About), Meta: n.Line.Meta})
	}
	if last+len(lines) > maxSeq {
		return LogPlan{}, nil, refuse(PhaseLog, "OVERFLOW", RefusalDetail{})
	}
	lp := LogPlan{FirstSeq: "0", LastSeq: "0", LineCount: len(lines), NoteSeqs: make([]tset.Decimal, len(in.Notes))}
	encoded := make([]json.RawMessage, len(lines))
	appends := map[string][]tset.Decimal{}
	var order []string
	for i := range lines {
		seq := tset.Decimal(strconv.Itoa(last + i + 1))
		lines[i].Seq, lines[i].AtMS = string(seq), in.NowMS
		if len(lines[i].IDs) > tset.MaxIDsPerLine {
			return LogPlan{}, nil, refuse(PhaseLog, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "line_ids"}})
		}
		b, err := json.Marshal(lines[i])
		if err != nil {
			return LogPlan{}, nil, refuse(PhaseLog, CodeRequest, RefusalDetail{})
		}
		if len(b) > tset.MaxLineBytes {
			return LogPlan{}, nil, refuse(PhaseLog, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "line_bytes"}})
		}
		encoded[i] = b
		lp.Commands++ // one XADD a line
		lp.ArgvBytes += len(b)
		for _, a := range lines[i].About {
			if _, ok := appends[a]; !ok {
				order = append(order, a)
			}
			appends[a] = append(appends[a], seq)
			lp.AboutAppends++
			lp.Commands++ // one RPUSH an append
			lp.ArgvBytes += len(a) + len(seq)
		}
		if i >= firstNote {
			lp.NoteSeqs[i-firstNote] = seq
		}
	}
	if len(lines) != 0 {
		lp.FirstSeq, lp.LastSeq = tset.Decimal(strconv.Itoa(last+1)), tset.Decimal(strconv.Itoa(last+len(lines)))
	}
	apply := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		key := logKey(in.Prefix, in.Epoch)
		lg := l.logs[key]
		if lg == nil {
			lg = &stubLog{history: map[string][]tset.Decimal{}}
			l.logs[key] = lg
		}
		for i, b := range encoded {
			lg.lines = append(lg.lines, b)
			lg.ids = append(lg.ids, len(lines[i].IDs))
		}
		for _, a := range order {
			lg.history[a] = append(lg.history[a], appends[a]...)
		}
	}
	return lp, apply, nil
}

// Read serves last, and lines as a page or as one atomic query (L2 2, 4).
// cardlines waits for Layer 2's twin.
func (l *LogStub) Read(prefix string, plan tset.ReadPlan) (tset.ReadReply, *Refusal) {
	if len(plan.Queries) != 1 {
		return tset.ReadReply{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	q := plan.Queries[0]
	l.mu.Lock()
	defer l.mu.Unlock()
	var lines []json.RawMessage
	var ids []int
	if lg := l.logs[logKey(prefix, plan.Epoch)]; lg != nil {
		lines, ids = lg.lines, lg.ids
	}
	last := len(lines)
	switch {
	case q.Kind == "last" && plan.Mode != "page":
		return tset.ReadReply{Status: "read", Epoch: plan.Epoch, Complete: true,
			Answers: []tset.ReadAnswer{{Kind: "last", LastSeq: tset.Decimal(strconv.Itoa(last))}}}, nil
	case q.Kind != "lines":
		return tset.ReadReply{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	after, err := strconv.Atoi(string(q.AfterSeq))
	if err != nil || after < 0 {
		return tset.ReadReply{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	through := last
	if q.ThroughSeq != nil {
		t, err := strconv.Atoi(string(*q.ThroughSeq))
		if err != nil || t < 0 {
			return tset.ReadReply{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
		}
		through = min(t, last)
	}
	var items []json.RawMessage
	budget := 0
	for seq := after + 1; seq <= through && len(items) < q.Limit; seq++ {
		n := ids[seq-1]
		if len(items) != 0 && q.IDsLimit != 0 && budget+n > q.IDsLimit {
			break // at least one line, whatever its size (1.1, E5)
		}
		budget += n
		items = append(items, lines[seq-1])
	}
	if plan.Mode != "page" {
		return tset.ReadReply{Status: "read", Epoch: plan.Epoch, Complete: true,
			Answers: []tset.ReadAnswer{{Kind: "lines", Lines: items}}}, nil
	}
	reply := tset.ReadReply{Status: "page", Epoch: plan.Epoch, Items: items,
		Through: json.RawMessage(strconv.Quote(strconv.Itoa(through))), Exhausted: after+len(items) >= through}
	if reply.Items == nil {
		reply.Items = []json.RawMessage{}
	}
	if len(items) != 0 {
		reply.Next = json.RawMessage(strconv.Quote(strconv.Itoa(after + len(items))))
	}
	return reply, nil
}

// Lines is a copy of an epoch's lines, line q at index q-1.
func (l *LogStub) Lines(prefix string, epoch tset.Decimal) []json.RawMessage {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lg := l.logs[logKey(prefix, epoch)]; lg != nil {
		return append([]json.RawMessage(nil), lg.lines...)
	}
	return nil
}

// History is a copy of one primary's list of seqs at an epoch.
func (l *LogStub) History(prefix string, epoch tset.Decimal, about string) []tset.Decimal {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lg := l.logs[logKey(prefix, epoch)]; lg != nil {
		return append([]tset.Decimal(nil), lg.history[about]...)
	}
	return nil
}

// dedup keeps the first of equal ids, in order (L2 1.2: per line).
func dedup(ids []string) []string {
	if ids == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
