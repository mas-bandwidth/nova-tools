package pulse

// A RULE IS A ROW, NOT A SENTENCE. Pit stop 3, class M (#828).
//
// POLICY.md is fifty-nine lines of prose, and every one of them is a rule only a model can
// execute: "on MAIN-RED write queue/STOP", "never `gh run rerun --failed`", "a PR whose
// mergeStateStatus is BEHIND is not merged". A rule in that shape costs a model's whole
// window to obey and cannot be tested at all -- which is why the same four of them were
// broken again today by a coordinator that had read them that morning.
//
// The rule that retires the class: a rule lives in pulse.toml, in RULES.tsv, or in a test.
// POLICY.md keeps the RECORD -- who said what, and when -- and never the rule.
//
// This file is RULES.tsv: five columns, one row per rule.
//
//	kind        one of RuleKinds: what the rule is about, which is how a pulse finds its rows
//	condition   when the rule fires, in the words of whoever wrote it
//	verdict     what happens then: the action, not the reasoning
//	since       the day the row entered the table
//	source      where the row came from: the POLICY.md line's own date, or a person
//
// `rules --seed-from <POLICY.md>` is the migration: every dated line of the prose becomes a
// row, kind guessed from its words, and a line that records what happened rather than
// ruling what happens becomes a row of kind `record`. The seed is not the end of the class
// -- a row whose verdict a verb can execute belongs in the verb -- but it is the end of
// rules nobody can enumerate.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// RulesFile is the table, in the queue directory beside pulse.toml.
const RulesFile = "RULES.tsv"

// rulesHeader is the file's first line, so a person opening it sees the columns.
const rulesHeader = "# kind\tcondition\tverdict\tsince\tsource\tstate"

// RuleKinds are the kinds a row may carry, in the order the guess tries them. The first
// nine are the subjects the bench's rules are actually about; `rule` is an obligation about
// none of them, and `record` is a line that records rather than rules.
var RuleKinds = []string{
	"stop", "revert", "hold", "route", "slots", "read", "bypass", "admission", "requeue",
	"rule", "record",
}

// kindWords is how a kind is guessed: the words a rule of that kind is written with. The
// order is RuleKinds' order, and the first kind with a word on the line wins -- so a line
// about stopping the queue on a red main is `stop` even though it also says revert.
var kindWords = map[string][]string{
	"stop":      {"stop", "red means stop", "main-red", "shut"},
	"revert":    {"revert", "roll back", "rolled back"},
	"hold":      {"hold", "held", "holds"},
	"route":     {"route", "model", "provider", "flash", "pro "},
	"slots":     {"slot", "width", "headroom", "capacity", "parallel"},
	"read":      {"read", "review", "verdict", "approve"},
	"bypass":    {"bypass", "admin", "workaround", "override"},
	"admission": {"admission", "admit", "launch", "card number", "cut"},
	"requeue":   {"requeue", "retry", "relaunch", "rerun", "second dispatch"},
}

// obligationWords are what makes a line a RULE rather than a RECORD. A line that tells the
// bench what must, may not or never happens is a rule; a line that says what landed, merged
// or was retired is the record POLICY.md is for.
var obligationWords = []string{
	"never", "always", "must", "only", "every", "no ", "not ", "nothing", "until",
	"refuse", "refused", "stays", "cannot", "do not", "is required", "shall",
}

// RuleRow is one row of RULES.tsv. State is the triage's marker (`pending` until a person
// has read a row a NEW verdict wrote); a row a person wrote carries none.
type RuleRow struct {
	Kind      string
	Condition string
	Verdict   string
	Since     string
	Source    string
	State     string
}

// ParseRuleRow reads one row's fields. Three shapes are accepted, because the table
// outlived two of them: three columns (kind, condition, verdict), four (the triage's
// kind, condition, verdict, state), and five or six (kind, condition, verdict, since,
// source, and the state when it is there). Fewer than three fields is not a row.
func ParseRuleRow(f []string) (RuleRow, bool) {
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	switch {
	case len(f) < 3:
		return RuleRow{}, false
	case len(f) == 3:
		return RuleRow{Kind: f[0], Condition: f[1], Verdict: f[2]}, true
	case len(f) == 4:
		return RuleRow{Kind: f[0], Condition: f[1], Verdict: f[2], State: f[3]}, true
	case len(f) == 5:
		return RuleRow{Kind: f[0], Condition: f[1], Verdict: f[2], Since: f[3], Source: f[4]}, true
	default:
		return RuleRow{Kind: f[0], Condition: f[1], Verdict: f[2], Since: f[3], Source: f[4], State: f[5]}, true
	}
}

// LoadRules reads <dir>/RULES.tsv. A missing file is no rows and no error: a bench that has
// not seeded its table yet still runs.
func LoadRules(dir string) ([]RuleRow, error) {
	raw, err := os.ReadFile(filepath.Join(dir, RulesFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %s", filepath.Join(dir, RulesFile), oneline.Err(err))
	}
	var rows []RuleRow
	for n, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimRight(line, "\r")
		if strings.TrimSpace(t) == "" || strings.HasPrefix(strings.TrimSpace(t), "#") {
			continue
		}
		row, ok := ParseRuleRow(strings.Split(t, "\t"))
		if !ok {
			return nil, fmt.Errorf("%s line %d has %d fields, want kind, condition, verdict, since, source (tab separated)",
				filepath.Join(dir, RulesFile), n+1, len(strings.Split(t, "\t")))
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// WriteRules writes the table whole, header first, through a temp file and a rename.
func WriteRules(dir string, rows []RuleRow) error {
	var b strings.Builder
	b.WriteString(rulesHeader + "\n")
	for _, r := range rows {
		// Escape and not Field: the columns are tab separated, so a space inside a cell is
		// text and not a second column, and a rule a person has to read must read as one.
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n",
			oneline.Escape(r.Kind), oneline.Escape(r.Condition), oneline.Escape(r.Verdict),
			oneline.Escape(dashIfEmpty(r.Since)), oneline.Escape(dashIfEmpty(r.Source)), oneline.Escape(dashIfEmpty(r.State)))
	}
	path := filepath.Join(dir, RulesFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %s", path, oneline.Err(err))
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("cannot write %s: %s", path, oneline.Err(err))
	}
	return nil
}

// RulesOfKind is every row of one kind, which is how a brief or a triage packet takes the
// rows it may decide without loading the table into a window.
func RulesOfKind(rows []RuleRow, kinds ...string) []RuleRow {
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	var out []RuleRow
	for _, r := range rows {
		if want[r.Kind] {
			out = append(out, r)
		}
	}
	return out
}

// RulesInput is the rules verb.
type RulesInput struct {
	Queue    string
	SeedFrom string // a POLICY.md to seed the table from
	Check    bool
	Max      int
	Now      func() time.Time
	Stdout   io.Writer
	Stderr   io.Writer
}

// Rules prints the table in one line, checks it, or seeds it. It returns 0 when it did, 2
// when it refused.
func Rules(in RulesInput) int {
	if strings.TrimSpace(in.Queue) == "" {
		fmt.Fprintf(in.Stderr, "RULES REFUSED: --queue is required (pass the queue directory %s lives in)\n", RulesFile)
		return 2
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	if in.SeedFrom != "" {
		raw, err := os.ReadFile(in.SeedFrom)
		if err != nil {
			fmt.Fprintf(in.Stderr, "RULES REFUSED: --seed-from %s: %s (pass the prose the rules live in today)\n",
				oneline.Field(in.SeedFrom), oneline.Err(err))
			return 2
		}
		rows := SeedRules(string(raw), now().UTC().Format("2006-01-02"))
		if len(rows) == 0 {
			fmt.Fprintf(in.Stderr, "RULES REFUSED: %s carries no dated line (a seeded row takes its source from the line's own date, such as 2026-09-16 or (added 02:55Z))\n",
				oneline.Field(in.SeedFrom))
			return 2
		}
		if err := WriteRules(in.Queue, rows); err != nil {
			fmt.Fprintf(in.Stderr, "RULES REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		fmt.Fprintf(in.Stdout, "RULES SEED file=%s rows=%d %s from=%s\n",
			oneline.Field(filepath.Join(in.Queue, RulesFile)), len(rows), kindCounts(rows), oneline.Field(in.SeedFrom))
		return 0
	}

	rows, err := LoadRules(in.Queue)
	if err != nil {
		fmt.Fprintf(in.Stderr, "RULES REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	if in.Check {
		if problems := checkRules(rows); len(problems) > 0 {
			list := bounded.Capped(in.Stderr, in.Max, "RULES", "problem", "(fix the rows in "+filepath.Join(in.Queue, RulesFile)+")")
			for _, p := range problems {
				list.Line("RULES PROBLEM " + p)
			}
			list.More()
			fmt.Fprintf(in.Stderr, "RULES REFUSED: %d of %d rows are not usable\n", len(problems), len(rows))
			return 2
		}
		fmt.Fprintf(in.Stdout, "RULES CHECK OK file=%s rows=%d %s\n",
			oneline.Field(filepath.Join(in.Queue, RulesFile)), len(rows), kindCounts(rows))
		return 0
	}
	fmt.Fprintf(in.Stdout, "RULES file=%s rows=%d %s\n",
		oneline.Field(filepath.Join(in.Queue, RulesFile)), len(rows), kindCounts(rows))
	return 0
}

// checkRules is what --check answers: a row with a kind nobody knows, or with nothing in a
// column, is a row no verb can execute.
func checkRules(rows []RuleRow) []string {
	var out []string
	for i, r := range rows {
		switch {
		case !slicesContains(RuleKinds, r.Kind):
			out = append(out, fmt.Sprintf("row %d: kind %s is not one of %s", i+1, oneline.Field(r.Kind), strings.Join(RuleKinds, "|")))
		case strings.TrimSpace(r.Condition) == "":
			out = append(out, fmt.Sprintf("row %d: no condition (say when the rule fires)", i+1))
		case strings.TrimSpace(r.Verdict) == "":
			out = append(out, fmt.Sprintf("row %d: no verdict (say what happens then)", i+1))
		case strings.TrimSpace(r.Source) == "" || r.Source == "-":
			out = append(out, fmt.Sprintf("row %d: no source (name the line or the person the rule came from)", i+1))
		}
	}
	return out
}

// kindCounts is the kinds of a table as one field: `kinds=stop:2,read:1`, counts and never
// a list, because the table is the file and this is its one line.
func kindCounts(rows []RuleRow) string {
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Kind]++
	}
	var kinds []string
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if counts[kinds[i]] != counts[kinds[j]] {
			return counts[kinds[i]] > counts[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	var cells []string
	for _, k := range kinds {
		cells = append(cells, fmt.Sprintf("%s:%d", k, counts[k]))
	}
	return "kinds=" + oneline.Field(listOrDash(cells))
}

// dateMarks are the three ways this bench's prose dates a line: the day (with an optional
// time), the "(added HH:MMZ)" of a rule appended to an older paragraph, and the bare
// "(HH:MMZ)" of a line written the same day. A line dated none of these ways is prose.
var dateMarks = []*regexp.Regexp{
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}(?:[ T]\d{2}:\d{2}Z?)?`),
	regexp.MustCompile(`\(added [^)]{1,40}\)`),
	regexp.MustCompile(`\(\d{1,2}:\d{2}Z?\)`),
}

// SeedRules turns prose into rows: one row per dated line, kind guessed from its words,
// `since` the day of the seed and `source` the line's own date.
func SeedRules(prose, since string) []RuleRow {
	var rows []RuleRow
	for _, raw := range strings.Split(prose, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "- "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		source := lineDate(line)
		if source == "" {
			continue
		}
		condition, verdict := splitRule(line)
		rows = append(rows, RuleRow{
			Kind:      GuessRuleKind(line),
			Condition: condition,
			Verdict:   verdict,
			Since:     since,
			Source:    source,
		})
	}
	return rows
}

// lineDate is the line's own date, or empty when it carries none.
func lineDate(line string) string {
	raw := lineDateRaw(line)
	if raw == "" {
		return ""
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "()"))
}

// lineDateRaw is the date exactly as the line writes it, parentheses and all, so the split
// can take the WHOLE mark out rather than leaving "( )" behind where a date was.
func lineDateRaw(line string) string {
	for _, re := range dateMarks {
		if m := re.FindString(line); m != "" {
			return m
		}
	}
	return ""
}

// attributions are how this bench's prose names who said a rule. A head that is only an
// attribution is not a condition -- "Glenn: Studio card slots are 64" is a rule about
// slots, not a rule about Glenn -- so the split steps past it to the next colon.
var attributions = []string{"glenn", "stella", "johnny", "rowan", "freddy", "emma", "added", "note", "from"}

// GuessRuleKind is the kind a line's words say it is. A line with no obligation word is a
// `record`: it says what happened, not what happens. A line that obliges but is about none
// of the nine subjects is a `rule`, which is a row somebody still has to place.
func GuessRuleKind(line string) string {
	low := strings.ToLower(line)
	if !hasAny(low, obligationWords) {
		return "record"
	}
	for _, kind := range RuleKinds {
		if hasAny(low, kindWords[kind]) {
			return kind
		}
	}
	return "rule"
}

// splitRule cuts a prose line into its condition and its verdict at the first colon after
// the date, which is how this bench writes them: "<when>: <what happens>".
func splitRule(line string) (string, string) {
	body := line
	if d := lineDateRaw(line); d != "" {
		if i := strings.Index(line, d); i >= 0 {
			body = strings.TrimSpace(line[:i] + " " + line[i+len(d):])
		}
	}
	body = strings.TrimSpace(strings.Trim(body, "-:() "))
	if before, after, ok := strings.Cut(body, ": "); ok && strings.TrimSpace(after) != "" {
		// A head that is only who said it is not a condition: step past it to the next
		// colon, and when there is none the whole tail is the verdict.
		if isAttribution(before) {
			if next, tail, ok := strings.Cut(strings.TrimSpace(after), ": "); ok && strings.TrimSpace(tail) != "" {
				return cap200(next), cap200(tail)
			}
			return cap200(strings.TrimSpace(after)), "(the line is one sentence; split it when it becomes a verb)"
		}
		return cap200(before), cap200(after)
	}
	// No colon: the first clause is when, the rest is what. A line with neither is its own
	// condition and its verdict says so, rather than a row with an empty column.
	if before, after, ok := strings.Cut(body, ", "); ok && strings.TrimSpace(after) != "" {
		return cap200(before), cap200(after)
	}
	return cap200(body), "(the line says no action; place it or retire it)"
}

// cap200 keeps a cell to a line: 200 bytes is what a brief may print, and a cell longer
// than that is prose that has not been made into a rule yet.
func cap200(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 200 {
		return s
	}
	return strings.TrimSpace(s[:197]) + "..."
}

// isAttribution says whether a head is only who said the rule.
func isAttribution(head string) bool {
	fields := strings.Fields(strings.ToLower(strings.Trim(head, `-,"' `)))
	if len(fields) == 0 || len(fields) > 4 {
		return false
	}
	first := strings.Trim(fields[0], `,;:"'`)
	return slicesContains(attributions, first)
}

func hasAny(low string, words []string) bool {
	for _, w := range words {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}
