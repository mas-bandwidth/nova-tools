package sprint

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// PipelineCounts is the pipeline over streams on the table. Archived streams
// do not compete for its readers or lander.
type PipelineCounts struct {
	Working int `json:"working"`
	Review  int `json:"review"`
	Merging int `json:"merging"`
}

const (
	propBackupReads  = "backup_reads"
	propBackupMerges = "backup_merges"
)

// Pipeline counts the three active columns in one pass over the declared rows.
func Pipeline(s *Snapshot) PipelineCounts {
	var c PipelineCounts
	for _, row := range s.Work.Rows() {
		c.Working += s.Work.Count(row, Working)
		c.Review += s.Work.Count(row, Review)
		c.Merging += s.Work.Count(row, Merging)
	}
	return c
}

// Backup reports the downstream bottleneck when both columns are backed up.
// Equality retains the last observed side; a clear requires falling below.
func (c PipelineCounts) Backup(props map[string]string) string {
	reads, merges := props[propBackupReads] == "true", props[propBackupMerges] == "true"
	if c.Merging > c.Working+c.Review || c.Merging == c.Working+c.Review && merges {
		return "merges"
	}
	if c.Review > c.Working || c.Review == c.Working && reads {
		return "reads"
	}
	return "none"
}

// TickBackups commits each threshold crossing and its judgment together in
// the fenced plan. The properties survive acknowledgments and loop restarts;
// retrying a committed step sees the new side and raises nothing. The initial
// side is clear. Libraries considered: fmt/strings for text, existing table
// properties and Plan for atomic observation; no new storage or event layer.
func TickBackups(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	c := Pipeline(s)
	for _, edge := range []struct {
		name, prop, column string
		count, threshold   int
	}{
		{"reads", propBackupReads, Review, c.Review, c.Working},
		{"merges", propBackupMerges, Merging, c.Merging, c.Working + c.Review},
	} {
		was, had := s.Work.Prop(edge.prop)
		backed := was == "true"
		next := backed
		if edge.count > edge.threshold {
			next = true
		}
		if edge.count < edge.threshold {
			next = false
		}
		if next == backed {
			continue
		}
		p.Props = append(p.Props, PropWrite{Table: Work, Name: edge.prop, Value: fmt.Sprint(next), Was: was, WasAbsent: !had})
		typ := edge.name + " are clear"
		if next {
			typ = edge.name + " are backed up"
		}
		for _, open := range s.Open {
			if open.Note.SprintLevel && (open.Note.Type == edge.name+" are backed up" || open.Note.Type == edge.name+" are clear") {
				p.Closes = append(p.Closes, open)
			}
		}
		n := judgment(typ, "", s.Now, 0)
		n.SprintLevel, n.Who, n.Decisions = true, r.who(), []string{"ack"}
		n.What = fmt.Sprintf("%s: working=%d review=%d merging=%d; %s; %s", typ, c.Working, c.Review, c.Merging, backupOldest(s, edge.column), backupCapacity(s, edge.name))
		p.Notes = append(p.Notes, n)
	}
	return p, 0
}

func backupOldest(s *Snapshot, column string) string {
	field := FieldFinishedAt
	if column == Merging {
		field = "accepted"
	}
	var oldest *Card
	var at time.Time
	for _, c := range s.Work.Column(column) {
		stamp := stampAt(c, field)
		if oldest == nil || stamp.Before(at) || stamp.Equal(at) && c.ID < oldest.ID {
			oldest, at = c, stamp
		}
	}
	if oldest == nil {
		return "oldest=none age=none"
	}
	if at.IsZero() {
		return "oldest=" + oldest.ID + " age=unknown"
	}
	return fmt.Sprintf("oldest=%s age=%s", oldest.ID, max(time.Duration(0), s.Now.Sub(at)))
}

func backupCapacity(s *Snapshot, kind string) string {
	if kind == "reads" {
		var readers []string
		for _, reader := range s.Readers.Rows() {
			width := fmt.Sprint(s.ReaderWidth(reader))
			if s.ReaderWidth(reader) == math.MaxInt {
				width = "unbounded"
			}
			readers = append(readers, fmt.Sprintf("%s reading=%d width=%s", reader, s.Readers.Count(reader, Reading), width))
		}
		return "readers=[" + strings.Join(readers, ", ") + "]; run: nova-sprint reader set <reader> --tiers <tiers>; nova-sprint fleet up <member> --width <n>; route enable: nova-config route set <route> --enabled true --as <actor>, then nova-config apply --kind route --as <actor>"
	}
	last := time.Time{}
	for _, c := range s.Work.Column(Landed) {
		if at := stampAt(c, "landed"); at.After(last) {
			last = at
		}
	}
	landing := "none"
	if !last.IsZero() {
		landing = stamp(last)
	}
	var stopped []string
	for _, stream := range s.Streams() {
		if s.StreamCtl(stream).F("state") == StreamStopped {
			stopped = append(stopped, stream)
		}
	}
	return "lander last_landing=" + landing + " stopped_streams=[" + strings.Join(stopped, ", ") + "]; run: nova-sprint resume --stream <stream> --did <what was done>"
}
