package sprintfn

import (
	"encoding/json"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// MemLog is Layer 2's Go twin as the sprint's twin composes it (item J9,
// errata E7.2): tset.MemLog, which writes the stored lines of
// lua/table_set_log.lua byte for byte and serves last, lines and cardlines
// as the store does, keyed by the deployment's prefix. A lines item is
// {seq, n, d} with d the stored body verbatim (L1 7).
type MemLog struct {
	m *tset.MemLog
}

// NewMemLog is an empty log.
func NewMemLog() *MemLog { return &MemLog{m: tset.NewMemLog()} }

// Plan plans one line per emitting entry of the table plan, in its order,
// then one per note, with seqs after the epoch's last (L2 1, 2); nothing is
// written until the returned LogApply runs.
func (l *MemLog) Plan(in LogInput) (LogPlan, LogApply, *Refusal) {
	entries := make([]tset.MemPlanEntry, len(in.Table.Entries))
	for i, pe := range in.Table.Entries {
		entries[i] = tset.MemPlanEntry{Index: i, Entry: pe.Entry, Before: pe.Before, After: pe.After,
			FieldChanges: pe.FieldChanges, Added: pe.Added, Deleted: pe.Entry.Del}
	}
	requestEpoch := in.RequestEpoch
	if requestEpoch == "" {
		requestEpoch = in.Epoch
	}
	lp, apply, ref := l.m.Plan(in.Prefix, tset.LogLinesInput{NowMS: in.NowMS, RequestEpoch: requestEpoch,
		WriteEpoch: in.Epoch, Entries: entries, Request: in.Request, Notes: in.Notes})
	if ref != nil {
		return LogPlan{}, nil, fromTset(PhaseLog, ref)
	}
	return LogPlan{FirstSeq: lp.FirstSeq, LastSeq: lp.LastSeq, LineCount: lp.LineCount, NoteSeqs: lp.NoteSeqs,
		AboutAppends: lp.AboutAppends, Commands: lp.Commands, ArgvBytes: lp.ArgvBytes}, LogApply(apply), nil
}

// Read serves last, lines and cardlines, as a page or one atomic query.
func (l *MemLog) Read(prefix string, plan tset.ReadPlan) (tset.ReadReply, *Refusal) {
	rep, ref := l.m.Read(prefix, plan)
	if ref != nil {
		return tset.ReadReply{}, fromTset(PhaseOpen, ref)
	}
	return rep, nil
}

// Stored is a copy of an epoch's stored lines.
func (l *MemLog) Stored(prefix string, epoch tset.Decimal) []tset.LogLine {
	return l.m.Stored(prefix, epoch)
}

// Lines is an epoch's lines in the semantic words (L2 4), line q at index
// q-1: seq, kind, at_ms, table, from, to, ids, about, score, rev, shared, set,
// unset, meta, add and del, as each line has them. It is the tests' view; the
// store's reads return the stored body.
func (l *MemLog) Lines(prefix string, epoch tset.Decimal) []json.RawMessage {
	stored := l.m.Stored(prefix, epoch)
	out := make([]json.RawMessage, 0, len(stored))
	for _, line := range stored {
		out = append(out, SemanticLine(line))
	}
	return out
}

// SemanticLine is a stored line in the semantic words; a body that does not
// decode is the stored item itself.
func SemanticLine(line tset.LogLine) json.RawMessage {
	b, err := tset.ParseLogBody(line.D)
	if err != nil {
		raw, _ := json.Marshal(line)
		return raw
	}
	m := map[string]any{"seq": line.Seq, "kind": tset.LogWords[b.K], "at_ms": b.MS}
	if b.Tbl != "" {
		m["table"] = b.Tbl
	}
	if b.From != nil {
		m["from"] = *b.From
	}
	if b.To != nil {
		m["to"] = *b.To
	}
	if b.IDs != nil {
		m["ids"] = b.IDs
	}
	if b.About != nil {
		m["about"] = b.About
	}
	if b.Score != nil {
		m["score"], m["rev"] = b.Score, b.Rev
	}
	if b.Shared != nil {
		m["shared"] = b.Shared
	}
	if b.Set != nil {
		m["set"] = b.Set
	}
	if b.Unset != nil {
		m["unset"] = b.Unset
	}
	if b.Meta != nil {
		m["meta"] = b.Meta
	}
	if b.Add != nil {
		m["add"] = b.Add
	}
	if b.Del != nil {
		m["del"] = b.Del
	}
	raw, _ := json.Marshal(m)
	return raw
}

// History is a copy of one primary's list of seqs at an epoch.
func (l *MemLog) History(prefix string, epoch tset.Decimal, about string) []tset.Decimal {
	return l.m.History(prefix, epoch, about)
}

// Twin is the tset twin under this one, for the fault seams of the refusal
// tests.
func (l *MemLog) Twin() *tset.MemLog { return l.m }

// maxSeq is the live sequence ceiling (L2 2: 2^53 - 1).
const maxSeq = 9007199254740991
