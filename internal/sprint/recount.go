package sprint

import (
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// RowRecount is one friend, member, route or stream row before and after a recount.
type RowRecount struct {
	Row        string `json:"row"`
	BeforeOK   int    `json:"before_ok"`
	AfterOK    int    `json:"after_ok"`
	BeforeFail int    `json:"before_failed"`
	AfterFail  int    `json:"after_failed"`
	BeforeWith int    `json:"before_withdrawn"`
	AfterWith  int    `json:"after_withdrawn"`
}

// RecountPlan re-derives friend, member, route and stream counters from the
// attempt records. Exclusions move out of the failed column into withdrawn or
// defect; a second call writes nothing. Route and stream rows are derived:
// nothing stores those counters apart from the cards.
func RecountPlan(s *Snapshot) (Plan, []RowRecount) {
	var p Plan
	if s == nil || s.Fleet == nil {
		return p, nil
	}
	beforeOK := map[string]int{}
	beforeFail := map[string]int{}
	beforeWith := map[string]int{}
	rowsSet := map[string]bool{}
	for _, r := range s.Fleet.Rows() {
		rowsSet[r] = true
		beforeOK[r] = s.Fleet.Count(r, DoneOK)
		beforeFail[r] = s.Fleet.Count(r, DoneFailed)
		beforeWith[r] = s.Fleet.Count(r, Withdrawn)
	}
	afterOK := map[string]int{}
	afterFail := map[string]int{}
	afterWith := map[string]int{}

	type derived struct{ before, after WorkerCounters }
	routes := map[string]*derived{}
	streams := map[string]*derived{}
	derive := func(m map[string]*derived, key string) *derived {
		if key == "" {
			return nil
		}
		d := m[key]
		if d == nil {
			d = &derived{}
			m[key] = d
		}
		return d
	}

	for _, c := range s.Fleet.Column(Ready, Working, DoneOK, DoneFailed, DoneDefect, Withdrawn) {
		blame, class, finding, fix, col := recountPlacement(c)
		cardSet := map[string]string{}
		if blame != "" && blame != c.F(FieldBlame) {
			cardSet[FieldBlame] = blame
		}
		if class != "" && class != c.F(FieldDefectClass) {
			cardSet[FieldDefectClass] = class
		}
		if finding != "" && finding != c.F("finding") {
			cardSet["finding"] = finding
		}
		if fix != "" && fix != c.F("fix") {
			cardSet["fix"] = fix
		}
		switch col {
		case DoneOK:
			afterOK[c.Row]++
		case DoneFailed:
			afterFail[c.Row]++
		case Withdrawn:
			afterWith[c.Row]++
		}
		if col != c.Col || len(cardSet) > 0 {
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, c.Row, col, cardSet))}})
		}
		afterCard := c
		if col != c.Col || len(cardSet) > 0 {
			afterCard = withField(c, "ok", c.F("ok"))
			afterCard.Col = col
			if afterCard.Fields == nil {
				afterCard.Fields = map[string]string{}
			}
			maps.Copy(afterCard.Fields, cardSet)
		}
		if d := derive(routes, c.F(FieldRoute)); d != nil {
			d.before = addCounters(d.before, WorkerStats([]*Card{c}))
			d.after = addCounters(d.after, WorkerStats([]*Card{afterCard}))
		}
		if d := derive(streams, c.F("stream")); d != nil {
			d.before = addCounters(d.before, WorkerStats([]*Card{c}))
			d.after = addCounters(d.after, WorkerStats([]*Card{afterCard}))
		}
	}

	var results []RowRecount
	for _, r := range slices.Sorted(maps.Keys(rowsSet)) {
		results = append(results, RowRecount{
			Row: r, BeforeOK: beforeOK[r], AfterOK: afterOK[r],
			BeforeFail: beforeFail[r], AfterFail: afterFail[r],
			BeforeWith: beforeWith[r], AfterWith: afterWith[r],
		})
	}
	for _, name := range slices.Sorted(maps.Keys(routes)) {
		d := routes[name]
		results = append(results, rowFromCounters("route "+name, d.before, d.after))
	}
	for _, name := range slices.Sorted(maps.Keys(streams)) {
		d := streams[name]
		results = append(results, rowFromCounters("stream "+name, d.before, d.after))
	}
	return p, results
}

func rowFromCounters(name string, before, after WorkerCounters) RowRecount {
	return RowRecount{
		Row:      name,
		BeforeOK: before.OK, AfterOK: after.OK,
		BeforeFail: before.Failed, AfterFail: after.Failed,
		BeforeWith: before.Refused + before.Withdrawn + before.Provider,
		AfterWith:  after.Refused + after.Withdrawn + after.Provider,
	}
}

func addCounters(a, b WorkerCounters) WorkerCounters {
	a.OK += b.OK
	a.Failed += b.Failed
	a.Refused += b.Refused
	a.Withdrawn += b.Withdrawn
	a.Provider += b.Provider
	a.Coordinator += b.Coordinator
	a.Done = a.OK + a.Failed
	a.OKPct = ComputeOKPercent(a.OK, a.Failed)
	return a
}

// recountPlacement is where a placed card sits once exclusions are out of the
// failed column, and the attempt record it should carry.
func recountPlacement(c *Card) (blame, class, finding, fix, col string) {
	report := c.F("report")
	blame = c.F(FieldBlame)
	class = c.F(FieldDefectClass)
	finding = c.F("finding")
	fix = c.F("fix")
	failed := c.F("ok") == "no" || c.Col == DoneFailed || c.Col == DoneDefect
	if blame == "" && failed {
		blame, class, finding, fix = ClassifyAttempt(report, true)
	}
	if blame == "" && c.Col == Withdrawn {
		b, cl, fnd, fx := ClassifyAttempt(report, true)
		if b == BlameCoordinator || b == BlameProvider {
			blame, class, finding, fix = b, cl, fnd, fx
		}
	}
	if c.F(FieldTakenBack) != "" && (blame == "" || blame == BlameWorker) {
		blame, class = BlameCoordinator, DefectTakeBack
		if finding == "" {
			finding = c.F(FieldTakenBack)
		}
	}
	col = c.Col
	switch {
	case c.F("ok") == "yes" || c.Col == DoneOK:
		col = DoneOK
	case class == DefectLaunchRefused || strings.HasPrefix(strings.TrimSpace(report), cardhdr.EndLaunch) || IsStagingRefusal(report):
		col = Withdrawn
	case class == DefectProvider || IsProviderFailure(report) || IsNoResult(report):
		col = Withdrawn
	case class == DefectTakeBack || handedBack(strings.ToLower(report)) || c.F(FieldTakenBack) != "":
		col = Withdrawn
	case noLaneDeadline(c, report) && failed:
		col = Withdrawn
		if blame == "" || blame == BlameWorker {
			blame, class = BlameCoordinator, DefectTakeBack
		}
	case blame == BlameCoordinator && coordinatorBriefClass(class) && (failed || c.Col == DoneFailed):
		col = DoneDefect
	case c.Col == Withdrawn:
		col = Withdrawn
	case c.Col == DoneDefect:
		col = DoneDefect
	case failed:
		col = DoneFailed
	}
	return blame, class, finding, fix, col
}
