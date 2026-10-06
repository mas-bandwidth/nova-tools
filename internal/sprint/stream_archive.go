package sprint

import (
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// Stream archive (docs/SPEC-SPRINT.md section 11; the owner, 2026-10-05: "I would like
// you to remove all the already landed work streams"): a stream whose every card has
// landed leaves the drawn work and merge tables and keeps its record. Its rows are hidden
// as the table layer hides a row (ntable.RowsHide): every card stays placed in its
// cells, every count and cost stays in the folds, so the footer, where's summary, the
// roadmap and the release records count it as before, and nothing of it is drawn. Unlike
// stream remove it loses nothing, so it needs no STOPPED machine; stream unarchive shows
// the rows again. The tick archives a stream itself when its last card lands
// (ArchiveDue), and puts one back that holds a card not landed (ArchiveStray). The
// machine is modelled in testdata/tla/StreamArchive.tla: TLC checks MCStreamArchive.cfg
// green, and MCStreamArchiveBrokenStray.cfg, the tick without ArchiveStray, is its
// reversed witness, a card added to an archived stream left where nothing draws it.

// NStreamArchived is the tick's note that it archived streams whose last card landed:
// information for the coordinator, no judgment to answer.
const NStreamArchived = "stream archived"

// StreamArchive is the rule of stream archive: why each named stream may not be
// archived, none when every one may. A stream is archived only when it is a row of the
// work or the merge table and every card it holds has landed: no primary or sentinel
// in a work column but landed, no merge card but merged (its control card is none of
// its cards). The refusal names the cards not landed. The verb names several and
// applies all or none.
func StreamArchive(s *Snapshot, streams []string) []Refusal {
	var out []Refusal
	refused := map[string]bool{}
	for _, st := range streams {
		if why := streamArchiveWhy(s, st); why != "" {
			out = append(out, Refusal{Key: st, Why: why})
			refused[st] = true
		}
	}
	return allOrNone("archived", "archives", streams, out, refused)
}

// StreamUnarchive is the rule of stream unarchive: a stream it names is a row of the
// work or the merge table. One that is not archived is shown already, and is no refusal.
func StreamUnarchive(s *Snapshot, streams []string) []Refusal {
	var out []Refusal
	refused := map[string]bool{}
	for _, st := range streams {
		if !s.Work.HasRow(st) && !s.Merge.HasRow(st) {
			out = append(out, Refusal{Key: st, Why: noStream(s, st)})
			refused[st] = true
		}
	}
	return allOrNone("unarchived", "unarchives", streams, out, refused)
}

func noStream(s *Snapshot, st string) string {
	return fmt.Sprintf("no stream %s on the work or merge table (streams: %s); nothing was changed", st, strings.Join(s.Streams(), ","))
}

func streamArchiveWhy(s *Snapshot, st string) string {
	if !s.Work.HasRow(st) && !s.Merge.HasRow(st) {
		return noStream(s, st)
	}
	open := Unlanded(s, st)
	if len(open) == 0 {
		return ""
	}
	const named = 5
	shown := open[:min(len(open), named)]
	more := ""
	if len(open) > named {
		more = fmt.Sprintf(" and %d more", len(open)-named)
	}
	return fmt.Sprintf("stream %s holds %d %s not landed (%s%s); nothing was changed; a stream is archived when every card of it has landed, and the tick archives it itself then",
		st, len(open), map[bool]string{true: "card", false: "cards"}[len(open) == 1], strings.Join(shown, ", "), more)
}

// Unlanded is the cards of the stream that have not landed, each as "id (column)",
// work cards first, each table's in id order: a primary or sentinel placed in its
// work row in a column but landed, a merge card placed in its merge row in a column
// but merged and its control card.
func Unlanded(s *Snapshot, st string) []string {
	var out []string
	for _, c := range s.Work.Cards() {
		if c.Placed() && c.Row == st && c.Col != Landed {
			out = append(out, c.ID+" ("+c.Col+")")
		}
	}
	for _, c := range s.Merge.Cards() {
		if c.Placed() && c.Row == st && c.Col != Merged && c.Col != Ctl {
			out = append(out, c.ID+" ("+c.Col+")")
		}
	}
	return out
}

// openCounts is the cards of each row of the shapes that have not landed, from the
// count cells alone: every work column but landed, every merge column but merged
// (stuck, queued and the hidden returned). A row absent from the merge table counts
// its work row alone.
func openCounts(work, merge ntable.Table) (open, landed map[string]int64) {
	open, landed = map[string]int64{}, map[string]int64{}
	add := func(t ntable.Table, done string, into map[string]int64) {
		for _, r := range t.Rows {
			for k, c := range t.Columns {
				if !c.HasSet() || c.Name == Ctl || k >= len(r.Cells) {
					continue
				}
				if c.Name == done {
					into[r.Key] += r.Cells[k].Count
					continue
				}
				open[r.Key] += r.Cells[k].Count
			}
		}
	}
	add(work, Landed, landed)
	add(merge, Merged, map[string]int64{})
	return open, landed
}

// ArchiveDue is the streams the tick archives itself, from the work and merge tables'
// shapes alone (no card read): each row of the work table not archived whose cards
// have all landed, at least one, with none open on its merge row. kept is the landed
// count of each stream the coordinator unarchived (stream unarchive): the tick leaves
// it shown until another of its cards lands.
func ArchiveDue(work, merge ntable.Table, kept map[string]int64) []string {
	open, landed := openCounts(work, merge)
	var out []string
	for _, r := range work.Rows {
		if r.Hidden || open[r.Key] > 0 || landed[r.Key] == 0 {
			continue
		}
		if n, ok := kept[r.Key]; ok && n == landed[r.Key] {
			continue
		}
		out = append(out, r.Key)
	}
	return out
}

// ArchiveStray is the archived streams that hold a card not landed (one added to the
// stream after it was archived): the tick shows them again, so no card of the sprint
// waits where nothing draws it.
func ArchiveStray(work, merge ntable.Table) []string {
	open, _ := openCounts(work, merge)
	var out []string
	for _, r := range work.Rows {
		if r.Hidden && open[r.Key] > 0 {
			out = append(out, r.Key)
		}
	}
	return out
}

// ArchiveSum is what where says of the archived streams in one line: how many, the
// cards they landed and what those cost.
type ArchiveSum struct {
	Streams []string `json:"streams"`
	Landed  int64    `json:"landed"`
	Cost    string   `json:"cost"`
}

// ArchiveSumOf is the archived streams of the work table's shape (its hidden rows),
// in its order, with their landed cards and the sum of their cost cells; nil when
// none is archived.
func ArchiveSumOf(work ntable.Table) *ArchiveSum {
	j := work.Column(Landed)
	sum := new(big.Rat)
	var a ArchiveSum
	for _, r := range work.Rows {
		if !r.Hidden {
			continue
		}
		a.Streams = append(a.Streams, r.Key)
		if j >= 0 && j < len(r.Cells) {
			a.Landed += r.Cells[j].Count
		}
		if c := r.Texts[Cost]; len(c) > 1 && c[0] == '$' {
			if v, err := amountOf(c[1:]); err == nil && v != nil {
				sum.Add(sum, v)
			}
		}
	}
	if len(a.Streams) == 0 {
		return nil
	}
	a.Cost = cardcost.Cents(sum)
	return &a
}

// Line is the archived streams as where draws them under the work table:
// "107 archived streams, 2,843 cards landed, $1,234.56".
func (a *ArchiveSum) Line() string {
	return fmt.Sprintf("%d archived %s, %s %s landed, %s", len(a.Streams), map[bool]string{true: "stream", false: "streams"}[len(a.Streams) == 1],
		groupThousands(a.Landed), map[bool]string{true: "card", false: "cards"}[a.Landed == 1], a.Cost)
}

// Has is whether the stream is one of the archived.
func (a *ArchiveSum) Has(stream string) bool { return a != nil && slices.Contains(a.Streams, stream) }

func groupThousands(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
