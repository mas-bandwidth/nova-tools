package sprint

import (
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// RowRecount holds before and after counters for one row.
type RowRecount struct {
	Row          string `json:"row"`
	BeforeOK     int    `json:"before_ok"`
	AfterOK      int    `json:"after_ok"`
	BeforeFail   int    `json:"before_failed"`
	AfterFail    int    `json:"after_failed"`
	BeforeWith   int    `json:"before_withdrawn"`
	AfterWith    int    `json:"after_withdrawn"`
	BeforeRefuse int    `json:"before_refused"`
	AfterRefuse  int    `json:"after_refused"`
	BeforeProv   int    `json:"before_provider"`
	AfterProv    int    `json:"after_provider"`
}

// RecountPlan re-derives every friend, member, route and stream counter from attempt records.
func RecountPlan(s *Snapshot) (Plan, []RowRecount) {
	var p Plan
	if s == nil || s.Fleet == nil {
		return p, nil
	}
	coord := s.Coordinator
	if coord == "" {
		coord = "coordinator"
	}
	coordRow := FriendRow(coord)
	declared := map[string]bool{}

	// Capture before counts per row:
	beforeOK := map[string]int{}
	beforeFail := map[string]int{}
	beforeWith := map[string]int{}
	beforeRefuse := map[string]int{}
	beforeProv := map[string]int{}
	rowsSet := map[string]bool{}

	for _, r := range s.Fleet.Rows() {
		rowsSet[r] = true
		beforeOK[r] = s.Fleet.Count(r, DoneOK)
		beforeFail[r] = s.Fleet.Count(r, DoneFailed)
		beforeWith[r] = s.Fleet.Count(r, Withdrawn)
		beforeRefuse[r] = s.Fleet.Count(r, Refused)
		beforeProv[r] = s.Fleet.Count(r, Provider)
	}

	afterOK := map[string]int{}
	afterFail := map[string]int{}
	afterWith := map[string]int{}
	afterRefuse := map[string]int{}
	afterProv := map[string]int{}

	// Only a finished card carries a counter to re-derive: a card back in ready or
	// working is a live attempt (a provider take re-dealt), and its stale blame and
	// class must not be read as a finished withdrawal (redeal).
	for _, c := range s.Fleet.Column(DoneOK, DoneFailed, Withdrawn, Refused, Provider) {
		report := c.F("report")
		blame := c.F(FieldBlame)
		class := c.F(FieldDefectClass)
		finding := c.F("finding")
		fix := c.F("fix")

		needsClassify := blame == "" && (c.F("ok") == "no" || c.Col == DoneFailed || IsWithdrawn(c.Col))
		if needsClassify {
			blame, class, finding, fix = ClassifyAttempt(report, c.F("ok") == "no" || c.Col == DoneFailed)
		}

		targetRow := c.Row
		targetCol := c.Col
		cardSet := map[string]string{}

		if blame != c.F(FieldBlame) && blame != "" {
			cardSet[FieldBlame] = blame
		}
		if class != c.F(FieldDefectClass) && class != "" {
			cardSet[FieldDefectClass] = class
		}
		if finding != c.F("finding") && finding != "" {
			cardSet["finding"] = finding
		}
		if fix != c.F("fix") && fix != "" {
			cardSet["fix"] = fix
		}

		isRefusal := class == DefectLaunchRefused || strings.HasPrefix(strings.TrimSpace(report), cardhdr.EndLaunch)
		isProvider := class == DefectProvider || IsProviderFailure(report) || IsNoResult(report)
		isTakeBack := class == DefectTakeBack || strings.Contains(strings.ToLower(report), "not started") || strings.Contains(strings.ToLower(report), "handed back")

		switch {
		case c.F("ok") == "yes" || c.Col == DoneOK:
			targetCol = DoneOK
			afterOK[targetRow]++
		case isRefusal:
			targetCol = Refused
			afterRefuse[targetRow]++
		case isProvider:
			targetCol = Provider
			afterProv[targetRow]++
		case isTakeBack:
			targetCol = Withdrawn
			afterWith[targetRow]++
		case blame == BlameCoordinator:
			targetRow = coordRow
			targetCol = DoneFailed
			if !s.Fleet.HasRow(targetRow) && !declared[targetRow] {
				p.Rows = append(p.Rows, RowAdd{Fleet, targetRow})
				declared[targetRow] = true
			}
			rowsSet[targetRow] = true
			afterFail[targetRow]++
		case IsWithdrawn(c.Col):
			targetCol = Withdrawn
			afterWith[targetRow]++
		default:
			targetCol = DoneFailed
			afterFail[targetRow]++
		}

		if targetRow != c.Row || targetCol != c.Col || len(cardSet) > 0 {
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, targetRow, targetCol, cardSet))}})
		}
	}

	var results []RowRecount
	for _, r := range slices.Sorted(maps.Keys(rowsSet)) {
		results = append(results, RowRecount{
			Row:          r,
			BeforeOK:     beforeOK[r],
			AfterOK:      afterOK[r],
			BeforeFail:   beforeFail[r],
			AfterFail:    afterFail[r],
			BeforeWith:   beforeWith[r],
			AfterWith:    afterWith[r],
			BeforeRefuse: beforeRefuse[r],
			AfterRefuse:  afterRefuse[r],
			BeforeProv:   beforeProv[r],
			AfterProv:    afterProv[r],
		})
	}
	return p, results
}
