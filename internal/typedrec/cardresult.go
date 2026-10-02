package typedrec

import (
	"bufio"
	"regexp"
	"strings"
)

// CardResult is a sprint card's RESULT.md in the card contract's shape
// (docs/SPEC-CARD-CONTRACT.md section 3): six `key: value` lines, an optional
// pull request title, and the text under `## Body`. It lives here with the
// other typed records (the one-typed-parser rule, #2506).
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
	if r.Head == "-" {
		r.Head = ""
	}
	return r
}
