package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Replay (L2 6, work item J5): an epoch's log, read in pages from seq 1
// through the last, folded into tables and written into a Mem twin, never
// into the live store (the cold read's decision 9), then compared with the
// store's tables. It is run on a closed epoch or while the machine is
// STOPPED (the model's MCSetTableLogReplayClosed; a live epoch races, as its
// MCSetTableLogReplay counterexample shows). The comparison is of values, not
// of DUMP bytes.

// Replayed is an epoch's tables and histories as its log says they are.
type Replayed struct {
	Epoch     Decimal
	Lines     int
	Rows      map[string]map[string]Decimal   // table, row: rank
	Records   map[string]map[string]MemRecord // table, id: the record
	Histories map[string][]Decimal            // primary: its seqs
}

// ReplayLines folds an epoch's lines, which must be seq 1 through the last
// with no hole, into its tables. A create makes a record, a move and a remove
// change one, a remove keeps the record with no place, a rows line adds and
// deletes rows, and each member line and note appends its seq to the history
// of each distinct about.
func ReplayLines(epoch Decimal, lines []LogLine) (*Replayed, error) {
	r := &Replayed{Epoch: epoch, Rows: map[string]map[string]Decimal{},
		Records: map[string]map[string]MemRecord{}, Histories: map[string][]Decimal{}}
	for i, line := range lines {
		if want := strconv.Itoa(i + 1); string(line.Seq) != want {
			return nil, fmt.Errorf("replay: line %d has seq %s: the log has a hole", i+1, line.Seq)
		}
		b, err := ParseLogBody(line.D)
		if err != nil {
			return nil, fmt.Errorf("replay: line %s: %w", line.Seq, err)
		}
		if n, err := strconv.Atoi(line.N); err != nil || n != b.Count() {
			return nil, fmt.Errorf("replay: line %s: n %q disagrees with its body", line.Seq, line.N)
		}
		if err := r.apply(line.Seq, b); err != nil {
			return nil, fmt.Errorf("replay: line %s: %w", line.Seq, err)
		}
		r.Lines++
	}
	return r, nil
}

func (r *Replayed) apply(seq Decimal, b LogBody) error {
	switch b.K {
	case "w":
		rows := r.Rows[b.Tbl]
		if rows == nil {
			rows = map[string]Decimal{}
			r.Rows[b.Tbl] = rows
		}
		for _, a := range b.Add {
			rows[a.Row] = a.Rank
		}
		for _, row := range b.Del {
			delete(rows, row)
		}
	case "a":
		// The advance is the first event of the new epoch; the rows it
		// restores are the rows lines after it.
	case "n":
		for _, a := range dedupStrings(b.About) {
			r.Histories[a] = append(r.Histories[a], seq)
		}
	case "c", "m", "x":
		records := r.Records[b.Tbl]
		if records == nil {
			records = map[string]MemRecord{}
			r.Records[b.Tbl] = records
		}
		for j, id := range b.IDs {
			rec, ok := records[id]
			switch {
			case b.K == "c" && ok:
				return fmt.Errorf("create of %s, which the log already made", id)
			case b.K != "c" && !ok:
				return fmt.Errorf("%s of %s, which the log never made", LogWords[b.K], id)
			case b.K == "c":
				rec = MemRecord{Epoch: r.Epoch, Fields: map[string]string{}}
			}
			if b.Rev[j][1] == nil {
				return fmt.Errorf("%s of %s has no revision after", LogWords[b.K], id)
			}
			rec.Revision = Decimal(*b.Rev[j][1])
			for f, v := range b.Shared {
				rec.Fields[f] = v
			}
			if b.Set != nil {
				for f, v := range b.Set[j] {
					rec.Fields[f] = v
				}
			}
			if b.Unset != nil {
				for _, f := range b.Unset[j] {
					delete(rec.Fields, f)
				}
			}
			if b.K == "x" {
				rec.Row, rec.Column, rec.Score = "", "", ""
			} else {
				place := b.From
				if b.To != nil {
					place = b.To
				}
				if place == nil || b.Score[j][1] == nil {
					return fmt.Errorf("%s of %s has no place or score after", LogWords[b.K], id)
				}
				row, col, ok := strings.Cut(*place, ":")
				if !ok {
					return fmt.Errorf("%s of %s has the place %q", LogWords[b.K], id, *place)
				}
				rec.Row, rec.Column, rec.Score = row, col, *b.Score[j][1]
			}
			records[id] = rec
		}
		for _, a := range dedupStrings(b.About) {
			r.Histories[a] = append(r.Histories[a], seq)
		}
	}
	return nil
}

// Into writes the replayed tables into a Mem at the epoch: its rows, then its
// records and their cells. The Mem holds the definitions and an empty epoch
// (DefineTable, SetActiveEpoch) and none of these records.
func (r *Replayed) Into(m *Mem, space string) error {
	for table, rows := range r.Rows {
		for row, rank := range rows {
			if err := m.SeedRow(space, table, r.Epoch, row, rank); err != nil {
				return fmt.Errorf("replay into the twin: row %s of %s: %w", row, table, err)
			}
		}
	}
	for table, records := range r.Records {
		for id, rec := range records {
			if err := m.SeedMember(space, table, r.Epoch, id, rec); err != nil {
				return fmt.Errorf("replay into the twin: %s of %s: %w", id, table, err)
			}
		}
	}
	return nil
}

// LineReader is a store that serves Layer 2's lines query: RedisStore.
type LineReader interface {
	Read(ctx context.Context, plan ReadPlan) (ReadReply, error)
}

// ReadEpochLines reads an epoch's whole log in lines pages of at most
// pageLimit lines, from seq 1 to the tail the first page fixes (L2 6): each
// page's last returned seq is the next page's after_seq.
func ReadEpochLines(ctx context.Context, store LineReader, space string, epoch Decimal, pageLimit int) ([]LogLine, error) {
	var out []LogLine
	after := Decimal("0")
	var through *Decimal
	for {
		q := ReadQuery{Kind: "lines", AfterSeq: after, ThroughSeq: through, Limit: pageLimit}
		reply, err := store.Read(ctx, ReadPlan{Epoch: epoch, Space: space, Mode: "page", Queries: []ReadQuery{q}})
		if err != nil {
			return nil, fmt.Errorf("read the log of epoch %s after %s: %w", epoch, after, err)
		}
		for _, raw := range reply.Items {
			var line LogLine
			if err := json.Unmarshal(raw, &line); err != nil {
				return nil, fmt.Errorf("read the log of epoch %s: an item %s: %w", epoch, raw, err)
			}
			out = append(out, line)
			after = line.Seq
		}
		if through == nil {
			var high Decimal
			if err := json.Unmarshal(reply.Through, &high); err != nil {
				return nil, fmt.Errorf("read the log of epoch %s: the page's through %s: %w", epoch, reply.Through, err)
			}
			through = &high
		}
		if reply.Exhausted {
			return out, nil
		}
		if len(reply.Items) == 0 {
			return nil, fmt.Errorf("read the log of epoch %s: a page after %s returned nothing and is not exhausted", epoch, after)
		}
	}
}
