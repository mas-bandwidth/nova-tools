package typedrec

import (
	"bufio"
	"regexp"
	"strings"
)

// CardResult is a sprint card's RESULT.md in the card contract's shape
// (docs/SPEC-CARD-CONTRACT.md section 3): six `key: value` lines, an optional
// pull request title, and the text under `## Body`. It lives here with the
// other typed records, giving each record shape one parser.
type CardResult struct {
	Shaped  bool // the six keys are present and head, verdict and report read
	Head    string
	Branch  string
	Verdict string // a work card's ok, not-done or nothing; a read's ok or broken
	Gate    string
	Output  string
	Report  string
	Title   string
	Body    string
	// ReportDerived is whether Report was taken from the body's first prose line because the
	// file had no `report:` line (a result whose every other key reads: head, branch,
	// verdict, gate and output, with a body). Measured on a worker's own RESULT.md
	// (fixed-cpp-appends-and-deprecations-read-clean.w1.g9.e20): the report line is the
	// ledger's one-line summary and guards nothing; the head and the verdict still do.
	ReportDerived bool
	// Fault, on a result that is not Shaped, says what is wrong with it in words the
	// next attempt can act on: the keys missing, a head that is no commit, a verdict
	// that is none of the four. "" when the shape holds, when the file is empty (no result
	// at all), and when the file is native's own (a card it ended wrote none).
	Fault string
}

// CardResultKeys are the six keys a shaped card result carries.
var CardResultKeys = []string{"head", "branch", "verdict", "gate", "output", "report"}

// CardVerdicts are the verdicts a card result may give.
var CardVerdicts = []string{"ok", "not-done", "nothing", "broken"}

// shaRE is a commit as git names it, abbreviated or whole: the one sha
// predicate of the card contract (IsSha).
var shaRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// IsSha is a commit id, abbreviated (7 or more) or whole, lowercase hex.
func IsSha(s string) bool { return shaRE.MatchString(s) }

// IsFullSha is a whole commit id: forty hex digits (sha1) or sixty-four (sha256).
func IsFullSha(s string) bool { return IsSha(s) && (len(s) == 40 || len(s) == 64) }

// ParseCardResult reads a card result: the first `key: value` of each key,
// and the text under a `## Body` line as the body. A head of `-` is none.
func ParseCardResult(b []byte) CardResult {
	var r CardResult
	seen := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var body []string
	inBody := false
	for sc.Scan() {
		line := sc.Text()
		if inBody {
			body = append(body, line)
			continue
		}
		t := strings.TrimSpace(line)
		if strings.EqualFold(t, "## Body") {
			inBody = true
			continue
		}
		k, v, ok := strings.Cut(t, ":")
		k = strings.ToLower(strings.TrimSpace(k))
		if !ok || strings.ContainsAny(k, " \t") {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = strings.TrimSpace(v)
		}
	}
	r.Head, r.Branch = strings.ToLower(seen["head"]), seen["branch"]
	r.Verdict = strings.ToLower(seen["verdict"])
	r.Gate, r.Output, r.Report, r.Title = seen["gate"], seen["output"], seen["report"], seen["title"]
	r.Body = strings.TrimSpace(strings.Join(body, "\n"))
	_, hasReport := seen["report"]
	if !hasReport && r.Body != "" {
		// no report line, but the body says what was done: its first prose line is the report
		if line := firstProse(r.Body); line != "" {
			r.Report, r.ReportDerived = line, true
			seen["report"] = line
		}
	}
	r.Shaped = r.Report != "" && (r.Head == "-" || IsSha(r.Head))
	for _, k := range CardResultKeys {
		if _, ok := seen[k]; !ok {
			r.Shaped = false
		}
	}
	known := false
	for _, v := range CardVerdicts {
		known = known || r.Verdict == v
	}
	r.Shaped = r.Shaped && known
	if !r.Shaped {
		r.ReportDerived = false
		if strings.TrimSpace(string(b)) != "" && !NativeWritten(b) {
			r.Fault = shapeFault(seen, r.Head, r.Verdict, known)
		}
	}
	if r.Head == "-" {
		r.Head = ""
	}
	return r
}

// nativeMark opens the line native writes into a RESULT.md for a card it ended itself
// (an idle reap, a deadline, a stage timeout): that file is native's word, not the
// worker's, and has no shape to fault.
const nativeMark = "written-by: nova-swarm native"

// NativeWritten is whether a RESULT.md is native's own, written for a card that
// published none.
func NativeWritten(b []byte) bool {
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), nativeMark) {
			return true
		}
	}
	return false
}

// maxDerivedReport bounds a report taken from a body, the length a report line keeps.
const maxDerivedReport = 300

// firstProse is the first line of a body that is prose: not blank, not a heading, not a
// fence, cut to maxDerivedReport.
func firstProse(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") {
			continue
		}
		if len(t) > maxDerivedReport {
			t = t[:maxDerivedReport]
		}
		return t
	}
	return ""
}

// shapeFault is what keeps a result from being shaped, in the card contract's own words
// (docs/SPEC-CARD-CONTRACT.md section 3). The first fault a worker should fix comes first.
func shapeFault(seen map[string]string, head, verdict string, knownVerdict bool) string {
	var missing []string
	for _, k := range CardResultKeys {
		if _, ok := seen[k]; !ok {
			missing = append(missing, k+":")
		}
	}
	var faults []string
	if len(missing) == len(CardResultKeys) {
		faults = append(faults, "none of the six `key: value` lines is present; the file must open with `head: <full sha>` on line 1, then branch:, verdict:, gate:, output:, report: (not a `RESULT:` line, not DONE or BLOCKED words)")
	} else if len(missing) > 0 {
		faults = append(faults, "no "+strings.Join(missing, ", ")+" line")
	}
	if _, ok := seen["head"]; ok && head != "-" && !IsSha(head) {
		faults = append(faults, "head is `"+clip(head)+"`, not a commit sha (7 or more lowercase hex digits) or -")
	}
	if _, ok := seen["verdict"]; ok && !knownVerdict {
		faults = append(faults, "verdict is `"+clip(verdict)+"`, not one of "+strings.Join(CardVerdicts, ", "))
	}
	if v, ok := seen["report"]; ok && v == "" {
		faults = append(faults, "report: is empty")
	}
	return strings.Join(faults, "; ")
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}
