package sprint

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// DefectRecord describes an attempt with blame=coordinator or blame=provider.
type DefectRecord struct {
	When    time.Time `json:"when"`
	Card    string    `json:"card"`
	Worker  string    `json:"worker"`
	Blame   string    `json:"blame"`
	Class   string    `json:"class"`
	Finding string    `json:"finding"`
	Fix     string    `json:"fix,omitempty"`
	Verdict string    `json:"verdict,omitempty"`
	Route   string    `json:"route,omitempty"`
	Stream  string    `json:"stream,omitempty"`
}

// FindDefects searches all attempt cards in s for blame=coordinator or blame=provider.
func FindDefects(s *Snapshot, since time.Time, defectClass string) []DefectRecord {
	var out []DefectRecord
	if s == nil || s.Fleet == nil {
		return out
	}
	for _, c := range s.Fleet.Column(Ready, Working, DoneOK, DoneFailed, Withdrawn, Refused, Provider) {
		blame := c.F(FieldBlame)
		class := c.F(FieldDefectClass)
		report := c.F("report")
		if blame == "" && (c.F("ok") == "no" || c.Col == DoneFailed || IsWithdrawn(c.Col)) {
			blame, class, _, _ = ClassifyAttempt(report, true)
		}
		if blame != BlameCoordinator && blame != BlameProvider {
			continue
		}
		if defectClass != "" && class != defectClass {
			continue
		}
		finished := stampAt(c, "finished")
		if finished.IsZero() {
			finished = stampAt(c, "withdrawn")
		}
		if finished.IsZero() {
			finished = stampAt(c, "taken")
		}
		if !since.IsZero() && !finished.IsZero() && finished.Before(since) {
			continue
		}
		worker := cmp.Or(c.F("member"), c.Row, "-")
		finding := c.F("finding")
		if finding == "" {
			finding = ExtractFindingFirstLine(report)
		}
		fix := c.F("fix")
		if fix == "" {
			fix = ExtractFix(report)
		}
		out = append(out, DefectRecord{
			When:    finished,
			Card:    c.ID,
			Worker:  worker,
			Blame:   blame,
			Class:   class,
			Finding: finding,
			Fix:     fix,
			Verdict: cmp.Or(c.F("verdict"), c.Col),
			Route:   c.F(FieldRoute),
			Stream:  c.F("stream"),
		})
	}
	slices.SortFunc(out, func(a, b DefectRecord) int {
		if !a.When.Equal(b.When) {
			if a.When.Before(b.When) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Card, b.Card)
	})
	return out
}
