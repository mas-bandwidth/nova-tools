// Package disposition is the one strict parser for typed review lines
// (nova-tools #3092 rev 7): `DISPOSITION who=<f> head=<sha40>
// verdict=<APPROVE|HOLD> score=<k>` and `REPAIR who=<f> head=<sha40>
// ready=<true|false>`, each with an optional `:` inline tail. `hold ingest`
// and the hold router share it, and so does `task done --body-file` on a
// review task. Records are keyed by the #3139 unit contract
// (s:<S>:u:<unit>, resolved through s:<S>:prunit:<repo>:<n>); the retired PR
// record s:<S>:pr:* is never written (nova-tools #3491, SPEC-NOTE on #3139
// comment 5814524499). s:<S>:disp:<repo>:<n> keeps its one writer,
// ns_ingest_disposition (#3092 rev 7 key table), for the readers #3491 has
// not yet moved to s:<S>:read:<unit>:<friend>.
package disposition

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Type is the typed-line token.
type Type string

const (
	TypeDisposition Type = "DISPOSITION"
	TypeRepair      Type = "REPAIR"
)

// Outcome says what a parsed comment makes.
type Outcome string

const (
	// Record is a line that makes a record (Lua still resolves who and self).
	Record Outcome = "RECORD"
	// NoRecord is prose, a non-record verdict or ready=false; exit 0.
	NoRecord Outcome = "NORECORD"
	// Refused is a malformed typed line; exit 2 and the reader retypes.
	Refused Outcome = "REFUSED"
)

// Line is one parsed typed line.
type Line struct {
	Type    Type
	Who     string
	Head    string
	Verdict string // APPROVE | HOLD (DISPOSITION)
	Score   int    // 1-10 (DISPOSITION)
	Kind    string // explicit kind= (DISPOSITION, optional)
	Scope   string // scope= (DISPOSITION, optional)
	Ready   bool   // REPAIR
	Tail    string // the inline tail after the first bare value ending in ':'
	Reason  string // tail + "\n" + the body after the typed line, trimmed
}

// Result is the parse of one comment body.
type Result struct {
	Outcome Outcome
	Why     string // NORECORD / REFUSED reason
	Line    Line
}

var (
	sha40      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	keyToken   = regexp.MustCompile(`^[a-z_]+$`)
	dispKeys   = map[string]bool{"who": true, "head": true, "verdict": true, "score": true, "kind": true, "scope": true}
	repairKeys = map[string]bool{"who": true, "head": true, "ready": true}
	holdKinds  = map[string]bool{"substance": true, "scope": true, "control": true, "ci": true, "order": true}
)

// Parse reads the first non-empty line of body after dropping fenced blocks
// and `>` quotes (merge.StripQuotedAndCode). Only a line starting exactly with
// `DISPOSITION ` or `REPAIR ` is typed; anything else is NORECORD prose.
func Parse(body string) Result {
	clean := merge.StripQuotedAndCode(body)
	lines := strings.Split(clean, "\n")
	first := -1
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			first = i
			break
		}
	}
	if first < 0 {
		return Result{Outcome: NoRecord, Why: "prose"}
	}
	typed := strings.TrimRight(lines[first], " \t\r")
	var typ Type
	switch {
	case strings.HasPrefix(typed, "DISPOSITION "):
		typ = TypeDisposition
	case strings.HasPrefix(typed, "REPAIR "):
		typ = TypeRepair
	default:
		return Result{Outcome: NoRecord, Why: "prose"}
	}
	fields, tail, why := splitFields(strings.TrimPrefix(typed, string(typ)+" "))
	if why != "" {
		return Result{Outcome: Refused, Why: why}
	}
	rest := strings.Join(lines[first+1:], "\n")
	// The reason keeps fences and quotes as text: take the raw body after the
	// typed line, not the stripped one.
	if idx := strings.Index(body, typed); idx >= 0 {
		rest = body[idx+len(typed):]
	}
	reason := strings.TrimSpace(strings.TrimSpace(tail) + "\n" + strings.TrimSpace(rest))
	ln := Line{Type: typ, Tail: strings.TrimSpace(tail), Reason: reason}
	allowed := dispKeys
	if typ == TypeRepair {
		allowed = repairKeys
	}
	for k := range fields {
		if !allowed[k] {
			return Result{Outcome: Refused, Why: "unknown-key " + k}
		}
	}
	ln.Who = fields["who"]
	if ln.Who == "" {
		return Result{Outcome: Refused, Why: "missing who"}
	}
	head, ok := fields["head"]
	if !ok {
		return Result{Outcome: Refused, Why: "missing head"}
	}
	head = strings.ToLower(head)
	if !sha40.MatchString(head) {
		return Result{Outcome: Refused, Why: "head not 40 hex"}
	}
	ln.Head = head
	if typ == TypeRepair {
		ready, ok := fields["ready"]
		if !ok {
			return Result{Outcome: Refused, Why: "missing ready"}
		}
		switch ready {
		case "true":
			ln.Ready = true
		case "false":
			return Result{Outcome: NoRecord, Why: "ready=false", Line: ln}
		default:
			return Result{Outcome: Refused, Why: "ready not true|false"}
		}
		return Result{Outcome: Record, Line: ln}
	}
	verdict, ok := fields["verdict"]
	if !ok {
		return Result{Outcome: Refused, Why: "missing verdict"}
	}
	scoreText, ok := fields["score"]
	if !ok {
		return Result{Outcome: Refused, Why: "missing score"}
	}
	scoreText = strings.TrimSuffix(scoreText, "/10")
	score, err := strconv.Atoi(scoreText)
	if err != nil || score < 1 || score > 10 {
		return Result{Outcome: Refused, Why: "score not 1-10"}
	}
	ln.Verdict, ln.Score = verdict, score
	if k, ok := fields["kind"]; ok {
		if !holdKinds[k] {
			return Result{Outcome: Refused, Why: "unknown kind " + k}
		}
		ln.Kind = k
	}
	ln.Scope = fields["scope"]
	if verdict != "APPROVE" && verdict != "HOLD" {
		return Result{Outcome: NoRecord, Why: "verdict=" + verdict, Line: ln}
	}
	return Result{Outcome: Record, Line: ln}
}

// splitFields reads the key=value run. It ends at end of line or at the first
// bare value ending in ':' (the ':' is stripped; the rest is the tail).
func splitFields(s string) (map[string]string, string, string) {
	fields := map[string]string{}
	i := 0
	for {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) {
			return fields, "", ""
		}
		eq := strings.IndexByte(s[i:], '=')
		sp := strings.IndexByte(s[i:], ' ')
		if eq < 0 || (sp >= 0 && sp < eq) {
			return nil, "", "bare token " + firstWord(s[i:])
		}
		key := s[i : i+eq]
		if !keyToken.MatchString(key) {
			return nil, "", "bad key " + key
		}
		if _, dup := fields[key]; dup {
			return nil, "", "repeated key " + key
		}
		i += eq + 1
		var value string
		if i < len(s) && s[i] == '"' {
			end := strings.IndexByte(s[i+1:], '"')
			if end < 0 {
				return nil, "", "unterminated quote in " + key
			}
			value = s[i+1 : i+1+end]
			i += end + 2
			fields[key] = value
			if i < len(s) && s[i] == ':' {
				return fields, s[i+1:], ""
			}
			if i < len(s) && s[i] != ' ' {
				return nil, "", "text after quote in " + key
			}
			continue
		}
		j := strings.IndexByte(s[i:], ' ')
		if j < 0 {
			value = s[i:]
			i = len(s)
		} else {
			value = s[i : i+j]
			i += j
		}
		if strings.HasSuffix(value, ":") {
			fields[key] = strings.TrimSuffix(value, ":")
			return fields, s[i:], ""
		}
		fields[key] = value
	}
}

func firstWord(s string) string {
	if j := strings.IndexByte(s, ' '); j >= 0 {
		return s[:j]
	}
	return s
}

var (
	fileLine = regexp.MustCompile(`[A-Za-z0-9_./-]+\.[A-Za-z0-9]+:\d+`)
	ciWord   = regexp.MustCompile(`(?i)\b(red|pending|cancel+ed|terminated|shards?)\b`)
	pullRef  = regexp.MustCompile(`(?i)(?:#|pull/|\bPR\s+#?)(\d+)\b`)
	sentence = regexp.MustCompile(`[.!?](\s+|$)|\n+`)
)

// ciVocab is every word a CI-state-only clause may use: the CI state words,
// the leg and shard names, and the glue of a status sentence. A clause with
// any other word (a cause, a defect, a component: "because the parser
// accepts invalid certificates") is not CI-only, whatever CI word it has.
var ciVocab = map[string]bool{
	"ci": true, "ci-ok": true, "check": true, "checks": true, "check-run": true, "check-runs": true,
	"job": true, "jobs": true, "run": true, "runs": true, "rerun": true, "re-run": true,
	"reruns": true, "rerunning": true, "leg": true, "legs": true, "shard": true, "shards": true,
	"red": true, "green": true, "pending": true, "queued": true, "cancelled": true, "canceled": true,
	"cancel": true, "terminated": true, "failed": true, "failing": true, "timed": true, "timeout": true,
	"out": true, "signal": true, "bench": true, "kill": true, "killed": true, "flaky": true,
	"test": true, "tests": true, "lint": true, "e2e": true, "studio": true, "space": true,
	"linux": true, "macos": true, "hosted": true, "self-hosted": true, "is": true, "are": true,
	"was": true, "were": true, "still": true, "now": true, "on": true, "at": true, "in": true,
	"of": true, "for": true, "the": true, "a": true, "an": true, "this": true, "head": true,
	"and": true, "or": true, "only": true, "waiting": true, "awaiting": true, "needs": true,
	"need": true, "not": true, "yet": true, "all": true, "every": true, "one": true, "two": true,
}

var ciToken = regexp.MustCompile(`[A-Za-z0-9/_-]+`)

// ciStateOnly: the clause names CI state and nothing else. It has at least
// one CI word, and every token is CI vocabulary, a number or a leg label
// such as 1/4 or sha-like hex.
func ciStateOnly(s string) bool {
	if !ciWord.MatchString(s) {
		return false
	}
	for _, w := range ciToken.FindAllString(s, -1) {
		lw := strings.ToLower(strings.Trim(w, "-_/"))
		if lw == "" || ciVocab[lw] || ciLegLabel.MatchString(lw) {
			continue
		}
		return false
	}
	return true
}

var ciLegLabel = regexp.MustCompile(`^([0-9]+(/[0-9]+)?|[0-9a-f]{7,40})$`)

// Classify derives the kind of a HOLD with no explicit kind= from its
// reason (rules 2-4; rule 1, self, is decided in Lua against the unit's
// author): every sentence names only CI state and there is no file:line ->
// ci; the only sentence names another pull -> order; else substance.
func Classify(reason string, pr int) string {
	if fileLine.MatchString(reason) {
		return "substance"
	}
	var sentences []string
	for _, s := range sentence.Split(reason, -1) {
		if t := strings.TrimSpace(s); t != "" {
			sentences = append(sentences, t)
		}
	}
	if len(sentences) == 0 {
		return "substance"
	}
	allCI := true
	for _, s := range sentences {
		if !ciStateOnly(s) {
			allCI = false
			break
		}
	}
	if allCI {
		return "ci"
	}
	if len(sentences) == 1 {
		for _, m := range pullRef.FindAllStringSubmatch(sentences[0], -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && n != pr {
				return "order"
			}
		}
	}
	return "substance"
}

// ShortRepo is the repo name the unit keys use (`mas-bandwidth/nova-tools`
// -> `nova-tools`).
func ShortRepo(repo string) string {
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// String renders a result as the one line the verb prints.
func (r Result) String() string {
	if r.Why == "" {
		return string(r.Outcome)
	}
	return fmt.Sprintf("%s %s", r.Outcome, r.Why)
}
