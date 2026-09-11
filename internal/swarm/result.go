package swarm

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
)

// THE RESULT HAS ONE SHAPE, so the fold is mechanical and a person reads counts.
//
// This parser is the whole of rule 15, and its hardest property is what it does NOT do: a
// report that does not parse yields NO findings, ever. A parser that salvaged the lines it
// liked would be a parser with an opinion, and the one path from a malformed report to a
// person is `result --id <job>`, which prints the file verbatim to the person who asked for
// it by id.
//
// The classes, and rule 8's law that completion is evidence SEPARATE from the count:
//
//	ok         a head with findings: <n>, n > 0
//	clean      a head with findings: 0 -- a bounded review that finished and found nothing,
//	           or a probe-row that finished `not done` with its reason. NEVER a failure: the
//	           tool must not pay a worker for finding something.
//	plan-only  a RESULT.md with NO head, whatever else it holds
//	no-result  no RESULT.md at all
//	malformed  a head without findings: <n>, a fourth state word, a table that does not
//	           parse -- quarantined, never folded, counted in no other column

// The classes.
const (
	ClassOK        = "ok"
	ClassClean     = "clean"
	ClassPlanOnly  = "plan-only"
	ClassNoResult  = "no-result"
	ClassMalformed = "malformed"
)

// The three state words, and there are exactly three: a fourth word is a report a
// coordinator has to interpret, and a coordinator reading forty reports interprets nothing.
var itemStates = map[string]bool{"red": true, "green": true, "not done": true}

var gateResults = map[string]bool{"pass": true, "fail": true, "not run": true}

// Finding is one appended finding line.
type Finding struct {
	Line     int
	Text     string
	Dup      bool
	Rule     string // the rule quoted verbatim, between backticks
	File     string // normalized: relative, no leading ./, backslashes read as /
	FileLine string
	Job      string
}

// Key is the part of rule 15's de-duplication key this finding carries; the report supplies
// repo and rev. Two findings merge only when both reports carry both AND they are equal.
func (f Finding) Key(repo, rev string) (string, bool) {
	if repo == "" || rev == "" || f.File == "" || f.Rule == "" {
		return "", false
	}
	return strings.Join([]string{repo, rev, f.File, f.FileLine, f.Rule}, "\x00"), true
}

// Quoted reports whether this finding carries its rule verbatim with a file:line beside it.
// A finding with no quote is counted `unquoted` and is not counted `accurate` by anybody:
// 5 of 67 findings in batch 1 were wrong, each one a rule paraphrased from memory.
func (f Finding) Quoted() bool { return f.Rule != "" && f.File != "" }

// Item is one row of the Per item table.
type Item struct {
	Text     string
	State    string
	Evidence string
}

// Gate is one row of the Gates table.
type Gate struct {
	Name    string
	Result  string
	Seconds string
}

// Report is one parsed RESULT.md.
type Report struct {
	Class         string
	MalformedLine int
	Heading       string
	HasHead       bool
	Findings      int // the head's own number, the worker's count
	NotesRead     int
	HasNotesRead  bool
	Repo          string
	Rev           string
	Paragraph     string
	Items         []Item
	Gates         []Gate
	LeftOwed      []string
	OneLine       string
	FindingLines  []Finding
	Hash          string
	Bytes         int
}

// Red, Green and NotDone count the item states.
func (r Report) Red() int     { return r.countState("red") }
func (r Report) Green() int   { return r.countState("green") }
func (r Report) NotDone() int { return r.countState("not done") }

func (r Report) countState(state string) int {
	n := 0
	for _, it := range r.Items {
		if it.State == state {
			n++
		}
	}
	return n
}

// HashBytes is a revision's identity: the SHA-256 of its bytes. There is no mtime anywhere
// in this tool -- two revisions with one mtime are two hashes, and an mtime a filesystem
// rounds is not an identity (rule 16).
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Short is the twelve characters of a hash that an event line carries.
func Short(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

// ParseReport reads one RESULT.md into a Report. It never returns a finding from a report
// it classifies malformed.
func ParseReport(data []byte) Report {
	r := Report{Hash: HashBytes(data), Bytes: len(data), Class: ClassPlanOnly}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	section := ""
	inHead := false
	headSeen := false
	headLine := 0
	for i, raw := range lines {
		n := i + 1
		line := strings.TrimRight(raw, " \t")
		switch {
		case strings.HasPrefix(line, "# "):
			if r.Heading == "" {
				r.Heading = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			}
			section, inHead = "", false
			continue
		case strings.HasPrefix(line, "## "):
			section = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			inHead = section == "head"
			if inHead {
				r.HasHead, headLine, headSeen = true, n, false
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if inHead {
			// RULE 8'S FIRST LINE (SPEC-SWARM.md:117): the evidence of completion is the
			// report's `## Head`, "whose first line is `findings: <n>`". FIRST. A head
			// that opened with `notes read:`, `repo:` or `rev:` was read here as a
			// complete report, so the one shape a coordinator classifies on was not the
			// shape the parser required, and completion evidence could sit anywhere in
			// the head. A shape the parser bends is not a shape.
			//
			// A BLANK LINE IS A LINE. Skipping whitespace to find the count gave "first"
			// a second reading, under which `## Head` / `` / `findings: 0` was a complete
			// report (read 4, F7). The first line of the head is the first line.
			if !headSeen {
				headSeen = true
				if !strings.HasPrefix(trimmed, "findings:") {
					return malformed(r, n)
				}
			}
			switch {
			case trimmed == "":
			case strings.HasPrefix(trimmed, "findings:"):
				if r.Class == ClassMalformed {
					break
				}
				count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "findings:")))
				if err != nil || count < 0 {
					return malformed(r, n)
				}
				r.Findings = count
				r.Class = ClassOK
				if count == 0 {
					r.Class = ClassClean
				}
			case strings.HasPrefix(trimmed, "notes read:"):
				count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "notes read:")))
				if err != nil {
					return malformed(r, n)
				}
				r.NotesRead, r.HasNotesRead = count, true
			case strings.HasPrefix(trimmed, "repo:"):
				r.Repo = strings.TrimSpace(strings.TrimPrefix(trimmed, "repo:"))
			case strings.HasPrefix(trimmed, "rev:"):
				r.Rev = strings.TrimSpace(strings.TrimPrefix(trimmed, "rev:"))
			default:
				// The head's paragraph: what was asked, what the state is now, and the
				// single most important fact. The FIRST line of the head has to be
				// findings: <n>, which is the completion evidence.
				if r.Class == ClassPlanOnly {
					return malformed(r, n)
				}
				if r.Paragraph == "" {
					r.Paragraph = trimmed
				} else {
					r.Paragraph += " " + trimmed
				}
			}
			continue
		}
		switch section {
		case "per item":
			cells, ok := tableRow(trimmed)
			if !ok || isTableRule(cells) {
				continue
			}
			if len(cells) < 3 {
				return malformed(r, n)
			}
			if isHeaderRow(cells, "item", "state", "evidence") {
				continue
			}
			state := strings.ToLower(strings.TrimSpace(cells[1]))
			if !itemStates[state] {
				return malformed(r, n)
			}
			r.Items = append(r.Items, Item{Text: cells[0], State: state, Evidence: cells[2]})
		case "gates":
			cells, ok := tableRow(trimmed)
			if !ok || isTableRule(cells) {
				continue
			}
			if len(cells) < 3 {
				return malformed(r, n)
			}
			if isHeaderRow(cells, "name", "result", "seconds") {
				continue
			}
			result := strings.ToLower(strings.TrimSpace(cells[1]))
			if !gateResults[result] {
				return malformed(r, n)
			}
			r.Gates = append(r.Gates, Gate{Name: cells[0], Result: result, Seconds: cells[2]})
		case "left owed":
			if strings.HasPrefix(trimmed, "- ") {
				r.LeftOwed = append(r.LeftOwed, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			}
		case "one line":
			if trimmed != "" && r.OneLine == "" {
				r.OneLine = trimmed
			}
		case "findings":
			switch {
			case strings.HasPrefix(trimmed, "- "):
				r.FindingLines = append(r.FindingLines, parseFinding(n, strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
			case trimmed != "" && len(r.FindingLines) > 0:
				// RULE 2'S "OR THE NEXT" (SPEC-SWARM.md:82-84): "A finding line carries
				// the rule it rests on, quoted word for word, with `file:line`, on the
				// same line or the next." The parser read ONE line per finding, so a
				// finding that wrapped its quote onto the following line had no rule and
				// no file: it was counted `unquoted`, it got no de-duplication key
				// (rule 15), and it was folded into nobody's page. The parser discarded
				// the exact evidence rule 2 exists to demand.
				//
				// The NEXT line, and no further; and only for a finding that did not
				// already carry both, so a complete claim is never re-read from the line
				// below it. This is the whole of the widening: the parser gains no
				// opinion, it reads the second line rule 2 always allowed.
				last := &r.FindingLines[len(r.FindingLines)-1]
				if last.Line == n-1 && !last.Quoted() {
					last.carry(trimmed)
				}
			}
		}
	}
	// D3 (the real run, 2026-09-11): ONE report has ONE finding count. The head's
	// `findings: <n>` is the worker's own answer and the thing rule 8 classifies on, and
	// the bullets under `## Findings` are what is FOLDED. When the head says 0, the
	// section holds no findings whatever it says in words: a complete review that wrote
	// `- none` ended `result=clean findings=1` and put "none" in a coordinator's page.
	if r.HasHead && r.Findings == 0 {
		r.FindingLines = nil
	}
	if r.HasHead && r.Class == ClassPlanOnly {
		// A head with no findings: line at all. The head is the completion evidence, and
		// evidence that does not say what it evidences is not evidence.
		return malformed(r, headLine)
	}
	return r
}

// malformed drops every finding the parser had collected. This is the quarantine, and it is
// one line of code because it is one rule: a malformed report yields nothing at all.
func malformed(r Report, line int) Report {
	r.Class, r.MalformedLine = ClassMalformed, line
	r.FindingLines, r.Items, r.Gates, r.LeftOwed = nil, nil, nil, nil
	return r
}

func tableRow(line string) ([]string, bool) {
	if !strings.HasPrefix(line, "|") {
		return nil, false
	}
	parts := strings.Split(strings.Trim(line, "|"), "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out, true
}

func isTableRule(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

func isHeaderRow(cells []string, want ...string) bool {
	if len(cells) < len(want) {
		return false
	}
	for i, w := range want {
		if strings.ToLower(strings.TrimSpace(cells[i])) != w {
			return false
		}
	}
	return true
}

// parseFinding reads one appended finding line. The shape is the prompt's:
//
//   - <what was found> `<the rule, quoted verbatim>` <file>:<line>
//
// and a line beginning `dup:` is a finding already on the owed list, which is not a new
// finding (rule 1: 25 of 67 findings in batch 1 were duplicates of the owed list).
func parseFinding(line int, text string) Finding {
	f := Finding{Line: line, Text: text}
	if rest, ok := cutPrefixFold(text, "dup:"); ok {
		f.Dup, f.Text = true, strings.TrimSpace(rest)
	}
	f.Rule = lastBacktickSpan(f.Text)
	f.File, f.FileLine = fileAndLine(f.Text)
	return f
}

// carry folds rule 2's "or the next" line into the finding above it: the quote and the
// `file:line` that did not fit on the bullet. It fills only what is EMPTY, and it keeps the
// continuation in the finding's text so a coordinator's page shows the evidence beside the
// claim it rests on.
func (f *Finding) carry(text string) {
	if f.Rule == "" {
		f.Rule = lastBacktickSpan(text)
	}
	if f.File == "" {
		f.File, f.FileLine = fileAndLine(text)
	}
	f.Text += " " + text
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// lastBacktickSpan returns the text of the last `...` span, which is where the rule quoted
// verbatim sits. The LAST rather than the first because a finding's own prose may quote an
// identifier before it quotes the rule it rests on.
func lastBacktickSpan(s string) string {
	end := strings.LastIndex(s, "`")
	if end <= 0 {
		return ""
	}
	start := strings.LastIndex(s[:end], "`")
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(s[start+1 : end])
}

// fileAndLine finds the <path>:<line> a finding rests on and NORMALIZES the path -- a
// leading ./ stripped, \ read as / -- so that ./internal/x.go:10 and internal\x.go:10 under
// one rev are one finding rather than two (rule 15).
func fileAndLine(s string) (string, string) {
	for _, token := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '`' || r == ',' || r == '(' || r == ')' || r == '"'
	}) {
		path, num, ok := strings.Cut(strings.TrimRight(token, ".:;"), ":")
		if !ok || path == "" || num == "" {
			continue
		}
		if _, err := strconv.Atoi(num); err != nil {
			continue
		}
		return NormalizePath(path), num
	}
	return "", ""
}

// NormalizePath is rule 15's normalization, in one place.
func NormalizePath(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	for strings.HasPrefix(path, "./") {
		path = path[2:]
	}
	return strings.TrimPrefix(path, "/")
}

// OwedMatch reports whether a finding matches an item on a pull request's owed list. The
// match is the owed item's own text, compared without case and without punctuation, because
// a worker retyping an owed line is not expected to retype its commas.
func OwedMatch(f Finding, owed []string) bool {
	needle := foldForMatch(f.Text)
	if needle == "" {
		return false
	}
	for _, item := range owed {
		hay := foldForMatch(item)
		if hay == "" {
			continue
		}
		if strings.Contains(needle, hay) || strings.Contains(hay, needle) {
			return true
		}
	}
	return false
}

func foldForMatch(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '/', r == '.', r == ':':
			b.WriteRune(r)
			space = false
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// FindingCount is the ONE number a report has: the head's own, where there is a head, and
// the finding lines where there is none. A report killed before it wrote its head still
// carries what it found (rule 3, and demanded test 8's headless report with one appended
// line).
func (r Report) FindingCount() int {
	if r.HasHead {
		return r.Findings
	}
	return len(r.FindingLines)
}

// ReadOwed reads an owed list: the items a pull request already knows it owes, one per
// line, `- ` bullets and blank lines alike. It is a FILE because an owed list is a
// paragraph a person wrote, and putting it in an argument would put it in the process
// table (the same reason a task is a file).
func ReadOwed(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		item := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}
