// Package storetest is what the tests of the sprint store and of its callers share: the
// twin's check (TwinDiff, a test's Store.CheckTwin). It imports the sprint package alone,
// so the store's own tests import it too; no program does.
package storetest

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nova-tools/internal/sprint"
)

// TwinDiff is how a snapshot planned on from the twin differs from a fresh
// read of the same generation: "" when they are the same state (the tables'
// records, rows, texts, properties and revisions, the open judgments and the
// coordinator). A test's Store.CheckTwin.
func TwinDiff(twin, fresh *sprint.Snapshot) string {
	var out []string
	for _, name := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} { // the store's four tables (store.All)
		a, b := twin.T(name), fresh.T(name)
		if a == nil || b == nil {
			if (a == nil) != (b == nil) {
				out = append(out, name+": loaded in one read and not the other")
			}
			continue
		}
		if a.Epoch != b.Epoch {
			out = append(out, fmt.Sprintf("%s: epoch %d/%d", name, a.Epoch, b.Epoch))
		}
		// A table's revision, rows and texts move with the writes outside the
		// fence too (the display cells, the rows a step declares), which the
		// fresh read, made after, may see and the twin's read not: they are
		// compared when the two reads saw the same revision. The records and
		// properties are written only under the fence: always compared.
		if a.Revision == b.Revision {
			if !slices.Equal(a.Rows(), b.Rows()) {
				out = append(out, fmt.Sprintf("%s: rows %v, fresh %v", name, a.Rows(), b.Rows()))
			}
			if fmt.Sprint(a.Texts) != fmt.Sprint(b.Texts) {
				out = append(out, fmt.Sprintf("%s: texts %v, fresh %v", name, a.Texts, b.Texts))
			}
		}
		if fmt.Sprint(a.Props()) != fmt.Sprint(b.Props()) {
			out = append(out, fmt.Sprintf("%s: props %v, fresh %v", name, a.Props(), b.Props()))
		}
		ac, bc := a.Cards(), b.Cards()
		byID := map[string]*sprint.Card{}
		for _, c := range bc {
			byID[c.ID] = c
		}
		for _, c := range ac {
			f := byID[c.ID]
			delete(byID, c.ID)
			switch {
			case f == nil:
				out = append(out, fmt.Sprintf("%s: %s in the twin (%s:%s rev %d), not in the fresh read", name, c.ID, c.Row, c.Col, c.Rev))
			case c.Row != f.Row || c.Col != f.Col || c.Score != f.Score || c.Rev != f.Rev || fmt.Sprint(c.Fields) != fmt.Sprint(f.Fields):
				out = append(out, fmt.Sprintf("%s: %s twin %s:%s %v rev %d %v, fresh %s:%s %v rev %d %v", name, c.ID, c.Row, c.Col, c.Score, c.Rev, c.Fields, f.Row, f.Col, f.Score, f.Rev, f.Fields))
			}
		}
		for id, f := range byID {
			out = append(out, fmt.Sprintf("%s: %s in the fresh read (%s:%s rev %d), not in the twin", name, id, f.Row, f.Col, f.Rev))
		}
	}
	if fmt.Sprint(twin.Open) != fmt.Sprint(fresh.Open) || fmt.Sprint(twin.Acked) != fmt.Sprint(fresh.Acked) {
		out = append(out, "the open judgments differ")
	}
	if twin.QueueLen != fresh.QueueLen || twin.Running != fresh.Running {
		out = append(out, fmt.Sprintf("the queue %d/%d, running %v/%v", twin.QueueLen, fresh.QueueLen, twin.Running, fresh.Running))
	}
	if twin.Coordinator != fresh.Coordinator || twin.SeatGeneration != fresh.SeatGeneration {
		out = append(out, "the coordinator or the seat's generation differs")
	}
	slices.Sort(out)
	if len(out) > 8 {
		out = append(out[:8], fmt.Sprintf("and %d more", len(out)-8))
	}
	return strings.Join(out, "; ")
}
