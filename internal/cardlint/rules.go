// Package cardlint is the admission lint for the eight brief defects of
// 2026-10-07 (docs/SPEC-CARDS.md). Lint reads the brief and the names and file
// sizes the caller passes. It returns refusal lines. It does not advise, and
// it does not open a repository.
//
// Libraries considered: the standard library, and internal/cardhdr, the one
// reader of a WHO line and a KEY: value line. A second parser would drift.
package cardlint

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The eight rules, in the order Lint reports them (docs/SPEC-CARDS.md).
const (
	RuleStepLines  = "step-lines-stated"
	RuleFinishForm = "finish-form-present"
	RuleOneLane    = "one-lanes-home-on-any-lane"
	RuleWholeFile  = "no-whole-file-read-over-100k"
	RuleTruncated  = "no-truncated-text"
	RuleExamples   = "examples-are-placeholders"
	RuleTLA        = "tla-edits-carry-tlacheck"
	RuleNames      = "no-names-outside-quotes"
)

// heavyBytes is 100 KB. A file over it is a whole-file read the lint refuses.
const heavyBytes = 100 * 1000

// heavyAtBase is the files this rule was cut against, used when the caller
// passes no sizes of its own. The figures are the byte sizes at that base.
var heavyAtBase = map[string]int{
	"docs/SPEC-SPRINT.md":      667502,
	"docs/CLI.md":              274352,
	"cmd/nova-sprint/verbs.go": 188036,
}

const tlaRecordsFile = "tla/RUNS.tsv"

// Finding is one defect. Refusal is the line a caller prints, and the brief
// is not admitted.
type Finding struct {
	Rule    string
	Line    int
	Refusal string
}

// Options are what the caller knows and the brief does not. Names are the
// people, friends and machines of the deployment. None are written in this
// tree. Sizes are file byte sizes at the brief's base; nil uses heavyAtBase.
type Options struct {
	Names []string
	Sizes map[string]int
}

// Lint refuses a brief for each of the eight rules it breaks.
func Lint(brief string, o Options) []Finding {
	var out []Finding
	out = append(out, ruleStepLines(brief)...)
	out = append(out, ruleFinishForm(brief)...)
	out = append(out, ruleOneLane(brief)...)
	out = append(out, ruleWholeFile(brief, o)...)
	out = append(out, ruleTruncated(brief)...)
	out = append(out, ruleExamples(brief)...)
	out = append(out, ruleTLA(brief)...)
	out = append(out, ruleNames(brief, o)...)
	return out
}

// ruleStepLines refuses a STEP that carries VERDICT: when the brief never
// states the step line the machine parses (docs/SPEC-CARDS.md, step-lines-stated).
func ruleStepLines(brief string) []Finding {
	if statesStepForm(brief) {
		return nil
	}
	var out []Finding
	for _, s := range stepsOf(brief) {
		if !strings.Contains(s.text, "VERDICT:") {
			continue
		}
		out = append(out, Finding{
			Rule:    RuleStepLines,
			Line:    s.line,
			Refusal: fmt.Sprintf("CARD REFUSED: step %d carries VERDICT: but the brief never states the step line the machine parses; run: add THE FINISH FORM with the step-line sentence", s.n),
		})
	}
	return out
}

// statesStepForm says the brief states the result line
// step <n>: <ok|broken|not-done|skipped> <40-hex sha or -> <one line>.
func statesStepForm(brief string) bool {
	low := strings.ToLower(brief)
	if !strings.Contains(low, "step <n>:") {
		return false
	}
	if !strings.Contains(low, "ok|broken|not-done|skipped") {
		return false
	}
	if !strings.Contains(low, "40-hex") && !strings.Contains(low, "40 hex") {
		return false
	}
	return strings.Contains(brief, "->")
}

// ruleFinishForm refuses a brief that does not carry THE FINISH FORM
// (docs/SPEC-CARDS.md, finish-form-present).
func ruleFinishForm(brief string) []Finding {
	if strings.Contains(brief, "THE FINISH FORM") &&
		verdictFormRE.MatchString(brief) &&
		headFormRE.MatchString(brief) {
		return nil
	}
	return []Finding{{
		Rule:    RuleFinishForm,
		Line:    1,
		Refusal: "CARD REFUSED: no FINISH FORM: the machine reads the report's first two lines before anything else; run: add the FINISH FORM paragraph",
	}}
}

var (
	verdictFormRE = regexp.MustCompile(`Verdict:\s*LAND\|HOLD\|FAIL`)
	headFormRE    = regexp.MustCompile(`Head:\s*(<40-hex[^>\n]*>|-)`)
)

// ruleOneLane refuses one lane's home on a card any lane can take, and a pin
// that also carries the fleet start beside a clone-and-push finish
// (docs/SPEC-CARDS.md, one-lanes-home-on-any-lane).
func ruleOneLane(brief string) []Finding {
	who, _ := cardhdr.ReadWho(brief)
	var out []Finding
	seen := map[string]bool{}
	add := func(line int, path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, Finding{
			Rule:    RuleOneLane,
			Line:    line,
			Refusal: fmt.Sprintf("CARD REFUSED: the start and the finish name one lane (%s) but the card is for whoever the dealer gives it to; run: use the lane-neutral STEP 1 and END", path),
		})
	}
	lines := strings.Split(brief, "\n")
	for i, line := range lines {
		for _, m := range laneHomeRE.FindAllStringSubmatch(line, -1) {
			if who.Name != "" && strings.EqualFold(m[1], who.Name) {
				continue
			}
			// A sentence period after the path is not part of the path.
			add(i+1, strings.TrimRight(m[0], ".,;:"))
		}
		if setJobRE.MatchString(line) {
			add(i+1, setJobRE.FindString(line))
		}
	}
	if who.Name == "" && pushCmdRE.MatchString(brief) {
		add(1, "git push")
	}
	if who.Name != "" && strings.Contains(brief, "do not clone") && pushCmdRE.MatchString(brief) {
		add(1, "git push")
	}
	return out
}

var (
	// laneHomeRE is one lane's home. Group 1 is the lane's name. The prefix is
	// optional, so ~/name-working/jobs/x and name-working/outbox/x both match.
	laneHomeRE = regexp.MustCompile(`(?:~/|/)?([A-Za-z0-9_-]+)-working/(?:jobs|outbox|inbox)/[^\s)'"<>]*`)
	setJobRE   = regexp.MustCompile(`Set JOB=\S*\s+from BRIEF\.md`)
	pushCmdRE  = regexp.MustCompile(`(?m)^[ \t]*\$?[ \t]*git push[ \t]*$`)
)

// ruleWholeFile refuses a step that reads a file over 100 KB whole
// (docs/SPEC-CARDS.md, no-whole-file-read-over-100k).
func ruleWholeFile(brief string, o Options) []Finding {
	paths := headerEntries(brief, "PATHS")
	var out []Finding
	seen := map[string]bool{}
	for _, s := range stepsOf(brief) {
		if namesSection(s.text) {
			continue
		}
		var files []string
		if readDocsRE.MatchString(s.text) {
			files = append(files, paths...)
		}
		for _, m := range readFileRE.FindAllStringSubmatch(s.text, -1) {
			files = append(files, m[1])
		}
		for _, file := range files {
			if seen[file] {
				continue
			}
			sz, ok := sizeOf(o, file)
			if !ok || sz <= heavyBytes {
				continue
			}
			seen[file] = true
			out = append(out, Finding{
				Rule:    RuleWholeFile,
				Line:    s.line,
				Refusal: fmt.Sprintf("CARD REFUSED: STEP %d reads %s whole (%d KB at BASE) against a 400,000-token budget; run: name the section (grep -n '^#' %s, then sed -n '<from>,<to>p')", s.n, file, sz/1000, file),
			})
		}
	}
	return out
}

func namesSection(text string) bool {
	low := strings.ToLower(text)
	return strings.Contains(low, "section") || strings.Contains(low, "grep -n") || strings.Contains(low, "sed -n")
}

func sizeOf(o Options, file string) (int, bool) {
	if o.Sizes != nil {
		n, ok := o.Sizes[file]
		return n, ok
	}
	n, ok := heavyAtBase[file]
	return n, ok
}

var (
	readDocsRE = regexp.MustCompile(`(?i)\bread\b[^\n]{0,80}(?:docs\s+PATHS\s+names|docs\s+named\s+in\s+PATHS|AGENTS\.md)`)
	readFileRE = regexp.MustCompile(`(?i)\bread\s+(?:the\s+(?:file|whole)\s+)?([A-Za-z0-9_./-]+\.(?:md|go))\b`)
)

// ruleTruncated refuses a prose paragraph that ends mid-sentence or holds an
// unclosed backtick (docs/SPEC-CARDS.md, no-truncated-text).
func ruleTruncated(brief string) []Finding {
	var out []Finding
	n := 0
	for _, p := range proseBlocks(brief) {
		n++
		text := strings.TrimSpace(p.text)
		if text == "" {
			continue
		}
		if endsClean(text) && !unclosedTick(text) && quoteHasSentence(p) {
			continue
		}
		out = append(out, Finding{
			Rule:    RuleTruncated,
			Line:    p.line,
			Refusal: fmt.Sprintf("CARD REFUSED: paragraph %d ends mid-sentence (%q); run: paste the whole finding, or state it whole in fewer words", n, lastRunes(text, 20)),
		})
	}
	return out
}

// quoteHasSentence says a blockquote's last 80 characters hold a sentence end.
// A block that is not a quotation is not held to that window.
func quoteHasSentence(p block) bool {
	if !p.quote {
		return true
	}
	tail := lastRunes(strings.TrimSpace(p.text), 80)
	return strings.ContainsAny(tail, ".!?")
}

func endsClean(s string) bool {
	r := []rune(s)
	if len(r) == 0 {
		return true
	}
	switch r[len(r)-1] {
	case '.', ':', ')', '?', '!', '>', '`':
		return true
	default:
		return false
	}
}

func unclosedTick(s string) bool {
	return strings.Count(s, "`")%2 == 1
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// ruleExamples refuses a FORM example row that is a concrete value, and an
// example command that names another tool (docs/SPEC-CARDS.md, examples-are-placeholders).
func ruleExamples(brief string) []Finding {
	var out []Finding
	for _, row := range formDataRows(brief) {
		cell := concreteCell(row)
		if cell == "" {
			continue
		}
		out = append(out, Finding{
			Rule:    RuleExamples,
			Line:    1,
			Refusal: fmt.Sprintf("CARD REFUSED: the FORM's example row is concrete (%s): it is copied and read false; run: make it the shape only, <the verb ...> <file>:<line> ...", cell),
		})
	}
	cardTool := ""
	if m := cardToolRE.FindStringSubmatch(brief); m != nil {
		cardTool = m[1]
	}
	if cardTool == "" {
		return out
	}
	seen := map[string]bool{}
	inFence := false
	for i, line := range strings.Split(brief, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !isExampleLine(line, inFence) {
			continue
		}
		for _, m := range novaToolRE.FindAllStringSubmatch(line, -1) {
			other := m[1]
			if other == cardTool || seen[other] {
				continue
			}
			seen[other] = true
			out = append(out, Finding{
				Rule:    RuleExamples,
				Line:    i + 1,
				Refusal: fmt.Sprintf("CARD REFUSED: the example names %s in a card about %s", other, cardTool),
			})
		}
	}
	return out
}

func isExampleLine(line string, inFence bool) bool {
	t := strings.TrimSpace(line)
	if strings.Contains(strings.ToLower(t), "example:") {
		return true
	}
	if inFence && (strings.HasPrefix(t, "nova-") || strings.HasPrefix(t, "$")) {
		return true
	}
	return strings.HasPrefix(t, "$ nova-") || strings.HasPrefix(t, "$nova-")
}

var (
	formHeadRE = regexp.MustCompile(`(?i)^(?:#{1,6}[ \t]+)?FORM\b`)
	cardToolRE = regexp.MustCompile(`cmd/(nova-[a-z0-9-]+)/`)
	novaToolRE = regexp.MustCompile(`\b(nova-[a-z0-9-]+)\b`)
)

// formDataRows is the FORM table's data rows: the header and the separator are not data.
func formDataRows(brief string) []string {
	var rows []string
	in := false
	for _, line := range strings.Split(brief, "\n") {
		t := strings.TrimSpace(line)
		if !in {
			if formHeadRE.MatchString(t) {
				in = true
			}
			continue
		}
		if t == "" || strings.HasPrefix(t, "STEP ") || strings.HasPrefix(t, "## ") || strings.HasPrefix(strings.ToLower(t), "example:") {
			break
		}
		if strings.HasPrefix(t, "|") || strings.Contains(line, "\t") {
			rows = append(rows, t)
		}
	}
	var data []string
	header := true
	for _, row := range rows {
		if strings.Contains(row, "---") {
			continue
		}
		if header {
			header = false
			continue
		}
		data = append(data, row)
	}
	return data
}

// concreteCell is the first cell of a row that is not a placeholder. A
// placeholder cell holds an angle bracket. "" means the row is shape only.
func concreteCell(row string) string {
	var cells []string
	if strings.Contains(row, "|") {
		for _, c := range strings.Split(row, "|") {
			c = strings.TrimSpace(c)
			if c != "" {
				cells = append(cells, c)
			}
		}
	} else {
		for _, c := range strings.Split(row, "\t") {
			c = strings.TrimSpace(c)
			if c != "" {
				cells = append(cells, c)
			}
		}
	}
	for _, c := range cells {
		if strings.Contains(c, "<") {
			continue
		}
		return c
	}
	return ""
}

// ruleTLA refuses a model edit that does not carry the record step. The
// finding is a refusal, never a note (docs/SPEC-CARDS.md, tla-edits-carry-tlacheck).
func ruleTLA(brief string) []Finding {
	paths := append(headerEntries(brief, "PATHS"), headerEntries(brief, "NEW")...)
	if !listNamesModel(paths) {
		return nil
	}
	records := listCovers(headerEntries(brief, "PATHS"), tlaRecordsFile)
	shared := listCovers(headerEntries(brief, "SHARED"), tlaRecordsFile)
	merged := false
	for _, s := range stepsOf(brief) {
		if stepMergesRecords(s.text) {
			merged = true
			break
		}
	}
	test, ok := headerValue(brief, "TEST")
	realTest := ok && goTestRE.MatchString(test)
	if records && shared && merged && realTest {
		return nil
	}
	return []Finding{{
		Rule:    RuleTLA,
		Line:    1,
		Refusal: "CARD REFUSED: PATHS name tla/ but no STEP runs tlacheck merge --keep (or tla/RUNS.tsv is not SHARED); run: add the TLC record step",
	}}
}

func stepMergesRecords(text string) bool {
	low := strings.ToLower(text)
	if !strings.Contains(low, "tlacheck merge") || !strings.Contains(text, "--keep") || !strings.Contains(text, "--root") || !strings.Contains(text, "--out") {
		return false
	}
	if !strings.Contains(text, tlaRecordsFile) {
		return false
	}
	return strings.Contains(text, "TLC records owed") || strings.Contains(low, "record machine")
}

var goTestRE = regexp.MustCompile(`\bTest[A-Za-z0-9_]+\b`)

// ruleNames refuses a configured name outside double quotes, on every line,
// not only the first paragraph. A WHO: line is the pin and is not a name.
// A lane home is that rule's own finding (docs/SPEC-CARDS.md, no-names-outside-quotes).
func ruleNames(brief string, o Options) []Finding {
	var names []string
	seen := map[string]bool{}
	for _, n := range o.Names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	if len(names) == 0 {
		return nil
	}
	var out []Finding
	for i, line := range strings.Split(brief, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "WHO:") {
			continue
		}
		scan := strings.ToLower(maskHomes(unquoted(line)))
		for _, n := range names {
			if hasWord(scan, n) {
				out = append(out, Finding{
					Rule:    RuleNames,
					Line:    i + 1,
					Refusal: fmt.Sprintf("CARD REFUSED: line %d names %s outside a quotation; run: say \"a Linux bench\", \"the coordinator's machine\", \"the owner\", \"a friend\", or By: <your own name>", i+1, n),
				})
			}
		}
	}
	return out
}

var quotedRE = regexp.MustCompile(`"[^"]*"|“[^”]*”`)

func unquoted(line string) string {
	return quotedRE.ReplaceAllStringFunc(line, func(q string) string {
		return strings.Repeat(" ", len(q))
	})
}

func maskHomes(line string) string {
	return laneHomeRE.ReplaceAllStringFunc(line, func(m string) string {
		return strings.Repeat(" ", len(m))
	})
}

func hasWord(s, w string) bool {
	if w == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(s[from:], w)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(w)
		if (start == 0 || !isWordByte(s[start-1])) && (end >= len(s) || !isWordByte(s[end])) {
			return true
		}
		from = start + 1
		if from > len(s) {
			return false
		}
	}
}

func isWordByte(b byte) bool {
	r := rune(b)
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

type step struct {
	n    int
	line int
	text string
}

var stepHeadRE = regexp.MustCompile(`(?i)^STEP[ \t]+([0-9]+)\b`)

func stepsOf(brief string) []step {
	var out []step
	for i, line := range strings.Split(brief, "\n") {
		if m := stepHeadRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			n, _ := strconv.Atoi(m[1])
			out = append(out, step{n: n, line: i + 1, text: line + "\n"})
			continue
		}
		if len(out) == 0 {
			continue
		}
		out[len(out)-1].text += line + "\n"
	}
	return out
}

type block struct {
	line  int
	text  string
	quote bool
}

// proseBlocks is the brief's prose, one block per blank-line paragraph, fences
// left out. A block of only header lines, STEP lines, headings and table rows
// is not prose. A block whose lines are all quotations is a quoted block.
func proseBlocks(brief string) []block {
	var out []block
	var cur []string
	start := 1
	quote := true
	inFence := false
	flush := func() {
		if proseText(cur) != "" {
			out = append(out, block{line: start, text: proseText(cur), quote: quote && len(cur) > 0})
		}
		cur = nil
		quote = true
	}
	for i, line := range strings.Split(brief, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			if inFence {
				inFence = false
			} else {
				flush()
				inFence = true
			}
			continue
		}
		if inFence {
			continue
		}
		if t == "" {
			flush()
			start = i + 2
			continue
		}
		if len(cur) == 0 {
			start = i + 1
		}
		if !strings.HasPrefix(t, ">") {
			quote = false
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

func proseText(lines []string) string {
	var kept []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || skipProseLine(t) {
			continue
		}
		t = strings.TrimPrefix(t, ">")
		kept = append(kept, strings.TrimSpace(t))
	}
	return strings.Join(kept, " ")
}

var headerKeyRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*:`)

func skipProseLine(t string) bool {
	switch {
	case isHeading(t):
		return true
	case strings.HasPrefix(t, "STEP ") || strings.HasPrefix(t, "STEP\t"):
		return true
	case strings.HasPrefix(t, "#"):
		return true
	case strings.HasPrefix(t, "|"):
		return true
	case headerKeyRE.MatchString(t):
		return true
	default:
		return false
	}
}

// isHeading says t is a title of capital letters, not a sentence. A pasted
// finding cut mid-word is not a title.
func isHeading(t string) bool {
	letters := false
	for _, r := range t {
		if unicode.IsLetter(r) {
			letters = true
			if !unicode.IsUpper(r) {
				return false
			}
		}
	}
	return letters
}

func headerValue(brief, key string) (string, bool) {
	for _, line := range strings.Split(brief, "\n") {
		k, v, ok := cardhdr.KeyValue(strings.TrimRight(line, "\r"))
		if ok && strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

func headerEntries(brief, key string) []string {
	v, ok := headerValue(brief, key)
	if !ok {
		return nil
	}
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if p != "none" && p != "-" {
			out = append(out, p)
		}
	}
	return out
}

func entryCovers(entry, file string) bool {
	e := strings.TrimSuffix(strings.TrimPrefix(entry, "./"), "/")
	if e == file || strings.HasPrefix(file, e+"/") {
		return true
	}
	for _, tree := range []string{"/**", "/..."} {
		if dir, ok := strings.CutSuffix(e, tree); ok && (file == dir || strings.HasPrefix(file, dir+"/")) {
			return true
		}
	}
	ok, _ := path.Match(e, file)
	return ok
}

func listCovers(entries []string, file string) bool {
	for _, e := range entries {
		if entryCovers(e, file) {
			return true
		}
	}
	return false
}

func listNamesModel(entries []string) bool {
	for _, e := range entries {
		t := strings.TrimSuffix(strings.TrimPrefix(e, "./"), "/")
		if strings.HasPrefix(t, "tla/") && strings.HasSuffix(t, ".tla") {
			return true
		}
		if entryCovers(e, "tla/Model.tla") {
			return true
		}
	}
	return false
}
