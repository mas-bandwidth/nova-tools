package sprint

import (
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Blame identifies who is responsible for an attempt's failure or outcome.
const (
	BlameCoordinator = "coordinator"
	BlameWorker      = "worker"
	BlameProvider    = "provider"
	BlameNone        = "none"
)

// DefectClass categorizes defects for introspection and reporting.
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

// Attempt fields.
const (
	FieldBlame       = "blame"
	FieldDefectClass = "class"
	FieldAttemptsRan = "attempts_ran"
)

// AttemptRecord carries accounting and blame metadata for one attempt.
type AttemptRecord struct {
	When    time.Time `json:"when"`
	Card    string    `json:"card"`
	Worker  string    `json:"worker"`
	Blame   string    `json:"blame"`
	Class   string    `json:"class"`
	Finding string    `json:"finding"`
	Fix     string    `json:"fix,omitempty"`
	Verdict string    `json:"verdict"`
	Route   string    `json:"route,omitempty"`
	Stream  string    `json:"stream,omitempty"`
}

// WorkerCounters holds the standardized work counts for a worker (friend, fleet member, route, stream).
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

// ComputeOKPercent computes the ok% from landed-or-ok over attempts a worker actually ran to an end.
// Denominator is ok + failed (where failed only counts attempts a worker ran to an end with blame=worker).
func ComputeOKPercent(ok, failed int) float64 {
	denom := ok + failed
	if denom == 0 {
		return 0.0
	}
	return float64(ok) / float64(denom) * 100.0
}

var fixRegex = regexp.MustCompile(`(?i)(?:fix|fix-card|fix_card)\s*[:=]\s*([a-zA-Z0-9_\-\.~]+)`)

// ExtractFix extracts a fix card name from report text, if present.
func ExtractFix(report string) string {
	m := fixRegex.FindStringSubmatch(report)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// ExtractFindingFirstLine extracts the first line or sentence of a finding from report text.
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
	lines := strings.Split(report, "\n")
	return firstSentence(lines[0])
}

// ClassifyAttempt classifies a report's finding into blame, class, finding first line, and fix card.
func ClassifyAttempt(report string, failed bool) (blame, class, finding, fix string) {
	finding = ExtractFindingFirstLine(report)
	fix = ExtractFix(report)

	if !failed {
		return BlameNone, "", "", fix
	}

	lower := strings.ToLower(report)
	trimmed := strings.TrimSpace(report)

	// The member puts a typed end at the start of a refusal. Do not infer an
	// infrastructure failure from those words appearing in a worker's finding.
	if strings.HasPrefix(trimmed, cardhdr.EndLaunch) || IsStagingRefusal(trimmed) {
		return BlameCoordinator, DefectLaunchRefused, finding, fix
	}

	// Provider blame likewise comes only from the member's typed end. A worker
	// report may legitimately mention HTTP status codes or provider failures it
	// diagnosed; that text alone cannot move blame away from the worker.
	if IsProviderFailure(trimmed) || IsNoResult(trimmed) {
		return BlameProvider, DefectProvider, finding, fix
	}

	// Check take-back / handed-back / not started:
	if strings.Contains(lower, "not started") ||
		strings.Contains(lower, "handed back") ||
		strings.Contains(lower, "take-back") ||
		strings.Contains(lower, "take back") ||
		strings.Contains(lower, "coordinator take") {
		return BlameCoordinator, DefectTakeBack, finding, fix
	}

	// Check paths defect:
	if strings.Contains(lower, "outside paths") ||
		strings.Contains(lower, "outside its paths") ||
		strings.Contains(lower, "paths-proposed") ||
		strings.Contains(lower, "paths that do not hold") ||
		strings.Contains(lower, "not in paths") ||
		strings.Contains(lower, "paths does not exist") ||
		strings.Contains(lower, "paths does not hold") {
		return BlameCoordinator, DefectPaths, finding, fix
	}

	// Check test-name defect:
	if strings.Contains(lower, "test name does not exist") ||
		strings.Contains(lower, "test does not exist") ||
		strings.Contains(lower, "no such test") ||
		strings.Contains(lower, "no test named") ||
		strings.Contains(lower, "test not found") {
		return BlameCoordinator, DefectTestName, finding, fix
	}

	// Check brief defect:
	if strings.Contains(lower, "missing file") ||
		strings.Contains(lower, "stop contradicts") ||
		strings.Contains(lower, "contradicts the tree") ||
		strings.Contains(lower, "brief defect") ||
		strings.Contains(lower, "step 1 defect already fixed") ||
		strings.Contains(lower, "defect already fixed") ||
		strings.Contains(lower, "wrong brief") {
		return BlameCoordinator, DefectBrief, finding, fix
	}

	// Check routing defect:
	if strings.Contains(lower, "misrouted") ||
		strings.Contains(lower, "wrong route") ||
		strings.Contains(lower, "tier mismatch") {
		return BlameCoordinator, DefectRouting, finding, fix
	}

	// Check store defect:
	if strings.Contains(lower, "store fault") ||
		strings.Contains(lower, "bench fault") ||
		strings.Contains(lower, "redis error") ||
		strings.Contains(lower, "store error") {
		return BlameCoordinator, DefectStore, finding, fix
	}

	return BlameWorker, DefectWork, finding, fix
}

// WorkerStats calculates WorkerCounters for a collection of cards.
// This is the single definition called by dashboard, stats, and coordinator view.
func WorkerStats(cards []*Card) WorkerCounters {
	var c WorkerCounters
	for _, card := range cards {
		blame := card.F(FieldBlame)
		class := card.F(FieldDefectClass)
		report := card.F("report")
		if blame == "" && (card.F("ok") == "no" || card.Col == DoneFailed) {
			blame, class, _, _ = ClassifyAttempt(report, true)
		}

		switch {
		case card.F("ok") == "yes" || card.Col == DoneOK:
			c.OK++
		case class == DefectLaunchRefused || strings.HasPrefix(strings.TrimSpace(report), cardhdr.EndLaunch):
			c.Refused++
			if blame == BlameCoordinator {
				c.Coordinator++
			}
		case class == DefectProvider || IsProviderFailure(report) || IsNoResult(report):
			c.Provider++
		case class == DefectTakeBack || strings.Contains(strings.ToLower(report), "not started") || strings.Contains(strings.ToLower(report), "handed back"):
			c.Withdrawn++
			if blame == BlameCoordinator {
				c.Coordinator++
			}
		case blame == BlameCoordinator:
			c.Coordinator++
		case blame == BlameProvider:
			c.Provider++
		case IsWithdrawn(card.Col):
			c.Withdrawn++
		default:
			c.Failed++
		}
	}
	c.Done = c.OK + c.Failed
	c.OKPct = ComputeOKPercent(c.OK, c.Failed)
	return c
}

// WorkerStatsBy is WorkerStats grouped by a field of the work cards: one
// WorkerCounters per value of the field, in name order. Empty values are left
// out. The fleet table's work cards are the attempt records, so a group's
// counters are the same definition the friends and fleet rows use.
func WorkerStatsBy(cards []*Card, field string) map[string]WorkerCounters {
	by := map[string][]*Card{}
	for _, c := range cards {
		if v := c.F(field); v != "" {
			by[v] = append(by[v], c)
		}
	}
	out := make(map[string]WorkerCounters, len(by))
	for name, cs := range by {
		out[name] = WorkerStats(cs)
	}
	return out
}
