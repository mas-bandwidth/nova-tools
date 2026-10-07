package sprint

import (
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Blame is who an attempt's outcome is charged to. A finish sets it from the
// report's finding class; the brief-defect judgment may set coordinator later.
const (
	BlameCoordinator = "coordinator"
	BlameWorker      = "worker"
	BlameProvider    = "provider"
	BlameNone        = "none"
)

// Defect classes the defects verb lists. DefectWork is a worker's own failure
// and is not a coordinator or provider defect.
const (
	DefectBrief         = "brief"
	DefectPaths         = "paths"
	DefectTestName      = "test-name"
	DefectRouting       = "routing"
	DefectLaunchRefused = "launch-refused"
	DefectProvider      = "provider"
	DefectStore         = "store"
	DefectTakeBack      = "take-back"
	DefectWork          = "work"
)

// FieldBlame and FieldDefectClass are the attempt record on a work card.
const (
	FieldBlame       = "blame"
	FieldDefectClass = "class"
)

// WorkerCounters is one row's work under the ok% definition: landed-or-ok over
// attempts a worker ran to an end. Refused, Withdrawn and Provider are the
// exclusions, counted beside ok% and never inside it.
type WorkerCounters struct {
	OK          int     `json:"ok"`
	Failed      int     `json:"failed"`
	Refused     int     `json:"refused"`
	Withdrawn   int     `json:"withdrawn"`
	Provider    int     `json:"provider"`
	Coordinator int     `json:"coordinator"`
	Done        int     `json:"done"`
	OKPct       float64 `json:"okpct"`
}

// ExclusionCounts is the three columns beside ok% for one fleet or friend row.
type ExclusionCounts struct {
	Refused   int `json:"refused,omitempty"`
	Withdrawn int `json:"withdrawn,omitempty"`
	Provider  int `json:"provider,omitempty"`
}

// ComputeOKPercent is landed-or-ok over attempts a worker ran (ok + failed).
// An empty denominator is zero.
func ComputeOKPercent(ok, failed int) float64 {
	denom := ok + failed
	if denom == 0 {
		return 0
	}
	return float64(ok) / float64(denom) * 100
}

var fixRegex = regexp.MustCompile(`(?i)(?:fix|fix-card|fix_card)\s*[:=]\s*([a-zA-Z0-9_\-\.~]+)`)

// ExtractFix is a fix card named in a report, or "".
func ExtractFix(report string) string {
	m := fixRegex.FindStringSubmatch(report)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// ExtractFindingFirstLine is the report's finding line, else its first line
// that is not a status stamp.
func ExtractFindingFirstLine(report string) string {
	report = strings.TrimSpace(report)
	if report == "" {
		return ""
	}
	for _, line := range strings.Split(report, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "finding:") {
			val := strings.TrimSpace(trimmed[len("finding:"):])
			if val != "" {
				return firstSentence(val)
			}
		}
	}
	for _, line := range strings.Split(report, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "status:") || strings.HasPrefix(lower, "verdict:") || strings.HasPrefix(lower, "head:") {
			continue
		}
		return firstSentence(trimmed)
	}
	return firstSentence(strings.Split(report, "\n")[0])
}

// ClassifyAttempt reads a finish report. failed is false for a landed-or-ok
// finish: blame is none. A typed member end (launch refused, staging refused,
// provider failure, no result) is infrastructure. Words such as 402 or 429
// inside a worker's finding are not. A handed-back line or a coordinator
// take-back is the coordinator's. A brief defect the worker names (paths, a
// missing test, a missing file, a STOP that contradicts the tree, a misroute,
// a store or bench fault) is the coordinator's too.
func ClassifyAttempt(report string, failed bool) (blame, class, finding, fix string) {
	finding = ExtractFindingFirstLine(report)
	fix = ExtractFix(report)
	if !failed {
		return BlameNone, "", finding, fix
	}
	lower := strings.ToLower(report)
	trimmed := strings.TrimSpace(report)
	if strings.HasPrefix(trimmed, cardhdr.EndLaunch) || IsStagingRefusal(trimmed) {
		return BlameCoordinator, DefectLaunchRefused, finding, fix
	}
	if IsProviderFailure(trimmed) || IsNoResult(trimmed) {
		return BlameProvider, DefectProvider, finding, fix
	}
	if handedBack(lower) {
		return BlameCoordinator, DefectTakeBack, finding, fix
	}
	// A PATHS finding is the brief's: the named code is not in the brief.
	// "outside PATHS" and PATHS-PROPOSED are the widen and paths rules'
	// own holds, and stay the worker's failed work for those rules.
	if strings.Contains(lower, "paths that do not hold") ||
		strings.Contains(lower, "paths does not exist") ||
		strings.Contains(lower, "paths does not hold") {
		return BlameCoordinator, DefectPaths, finding, fix
	}
	if strings.Contains(lower, "test name does not exist") ||
		strings.Contains(lower, "test does not exist") ||
		strings.Contains(lower, "no such test") ||
		strings.Contains(lower, "no test named") ||
		strings.Contains(lower, "test not found") {
		return BlameCoordinator, DefectTestName, finding, fix
	}
	// BriefDefectOf is the brief's own reader: a negated label ("not a brief
	// defect", "no brief defect") and a hyphenated card id are not one.
	if BriefDefectOf(report) != "" {
		return BlameCoordinator, DefectBrief, finding, fix
	}
	if strings.Contains(lower, "missing file") ||
		strings.Contains(lower, "stop contradicts") ||
		strings.Contains(lower, "contradicts the tree") ||
		strings.Contains(lower, "defect already fixed") ||
		strings.Contains(lower, "wrong brief") {
		return BlameCoordinator, DefectBrief, finding, fix
	}
	if strings.Contains(lower, "misrouted") ||
		strings.Contains(lower, "wrong route") ||
		strings.Contains(lower, "tier mismatch") {
		return BlameCoordinator, DefectRouting, finding, fix
	}
	if strings.Contains(lower, "store fault") ||
		strings.Contains(lower, "bench fault") ||
		strings.Contains(lower, "redis error") ||
		strings.Contains(lower, "store error") {
		return BlameCoordinator, DefectStore, finding, fix
	}
	return BlameWorker, DefectWork, finding, fix
}

// handedBack is a coordinator take-back or a worker's "not started; handed back".
// A finding that merely says "take back" is the worker's own sentence.
func handedBack(lower string) bool {
	if strings.Contains(lower, "not started") && strings.Contains(lower, "handed back") {
		return true
	}
	return strings.Contains(lower, "take-back") || strings.Contains(lower, "coordinator take") || strings.Contains(lower, "taken back")
}

// coordinatorBriefClass is a finding class charged to the brief's author.
// The lane ran, so the attempt counts, and the card sits in the defect column.
func coordinatorBriefClass(class string) bool {
	switch class {
	case DefectBrief, DefectPaths, DefectTestName, DefectRouting, DefectStore:
		return true
	}
	return false
}

// attemptAccount writes the attempt record onto a finish's field set.
func attemptAccount(set map[string]string, blame, class, finding, fix string) {
	if blame != "" {
		set[FieldBlame] = blame
	}
	if class != "" {
		set[FieldDefectClass] = class
	}
	if finding != "" {
		set["finding"] = finding
	}
	if fix != "" {
		set["fix"] = fix
	}
}

// WorkerStats is the one ok% definition. Dashboard side columns, stats, recount
// and the coordinator's defect count all read these counters. ok% is ok over
// ok+failed, and failed is only an attempt a worker ran to an end.
func WorkerStats(cards []*Card) WorkerCounters {
	var c WorkerCounters
	for _, card := range cards {
		if card == nil {
			continue
		}
		blame := card.F(FieldBlame)
		class := card.F(FieldDefectClass)
		report := card.F("report")
		failed := card.F("ok") == "no" || card.Col == DoneFailed || card.Col == DoneDefect
		if blame == "" && failed {
			blame, class, _, _ = ClassifyAttempt(report, true)
		}
		switch {
		case card.F("ok") == "yes" || card.Col == DoneOK:
			c.OK++
		case class == DefectLaunchRefused || strings.HasPrefix(strings.TrimSpace(report), cardhdr.EndLaunch) || IsStagingRefusal(report):
			c.Refused++
			if blame == BlameCoordinator {
				c.Coordinator++
			}
		case class == DefectProvider || IsProviderFailure(report) || IsNoResult(report):
			c.Provider++
		case class == DefectTakeBack || handedBack(strings.ToLower(report)) || card.F(FieldTakenBack) != "":
			c.Withdrawn++
			if blame == BlameCoordinator || card.F(FieldTakenBack) != "" {
				c.Coordinator++
			}
		case blame == BlameCoordinator:
			c.Coordinator++
		case blame == BlameProvider:
			c.Provider++
		case card.Col == Withdrawn:
			c.Withdrawn++
		default:
			if failed || card.Col == DoneFailed {
				c.Failed++
			}
		}
	}
	c.Done = c.OK + c.Failed
	c.OKPct = ComputeOKPercent(c.OK, c.Failed)
	return c
}

// RowExclusions is each fleet row's refused, withdrawn and provider counts,
// from WorkerStats over the cards placed on it. The tick stores them; where
// copies them beside ok%.
func RowExclusions(s *Snapshot) map[string]ExclusionCounts {
	if s == nil || s.Fleet == nil {
		return nil
	}
	by := map[string][]*Card{}
	for _, c := range s.Fleet.Column(Ready, Working, DoneOK, DoneFailed, DoneDefect, Withdrawn) {
		by[c.Row] = append(by[c.Row], c)
	}
	out := map[string]ExclusionCounts{}
	for row, cards := range by {
		st := WorkerStats(cards)
		if st.Refused == 0 && st.Withdrawn == 0 && st.Provider == 0 {
			continue
		}
		out[row] = ExclusionCounts{Refused: st.Refused, Withdrawn: st.Withdrawn, Provider: st.Provider}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// countsAsWorkerFailed says the card is in the ok% denominator's failed side.
func countsAsWorkerFailed(c *Card) bool {
	return WorkerStats([]*Card{c}).Failed == 1
}

// noLaneDeadline is a deadline kill of a card no lane took. The take stamp
// is read by takeStamps, the one reader of that stamp beside WorkDeadline.
func noLaneDeadline(c *Card, report string) bool {
	_, taken := takeStamps(c)
	return taken == "" && strings.Contains(strings.ToLower(report), "deadline")
}
