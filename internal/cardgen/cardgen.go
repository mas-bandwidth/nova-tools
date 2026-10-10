// Package cardgen is nova-card's planner: it turns a structured source (a ratchet
// ledger, a findings TSV, a tool's rendered help) into a directory of briefs that
// `nova-sprint add --brief-dir` admits as they are. Everything here is a pure
// function over text (docs/SPEC-CARD-CONTRACT.md section 6, generated cards): the row parsers, the
// PATHS computation, the wave assignment and the rendering take bytes and return
// bytes, so the unit tests run on fixture ledgers with no repository, no clock and
// no store. cmd/nova-card is the transport: it reads the worktree, resolves the
// base sha, runs the lint over the output and writes the directory.
package cardgen

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// Row is one entry of a source: the file (or package directory) the work lives in,
// the key that names the entry inside that file, and the rest of the row as its
// source wrote it. Line is the row's line in the source file, 1-based.
type Row struct {
	File string
	Key  string
	Rest string
	Line int
}

// Ledger describes one ratchet ledger of internal/ci: where it lives, the class test
// that holds it, how a row reads, the tier its cards default to, and the task text a
// card gets with its rows substituted.
type Ledger struct {
	Name string // the --ledger name
	File string // repository-relative path of the ledger file
	Test string // the TEST: line's package and test name
	Tier string // flash or pro
	// Generated says the ledger is one land regenerates (docs/SPEC-SPRINT.md section
	// 7, the generality family): cards on it need no dependency on one another.
	Generated bool
	// Counted says a row carries a count (`pkg N`, dead_code) rather than naming one
	// site: the card's STOP reads the count, not the number of rows.
	Counted bool
	// Task is the card's task paragraph. {{file}}, {{rows}}, {{ledger}}, {{test}} and
	// {{count}} are substituted; everything else is written as it stands.
	Task string
	// Parse reads one non-comment row. ok is false for a row the ledger does not
	// recognise, which the planner reports and skips.
	Parse func(line string) (r Row, ok bool)
}

var (
	// `path:Name rest` (serial-tests, slowwaits)
	colonRowRE = regexp.MustCompile(`^(\S+?):([A-Za-z_][A-Za-z0-9_]*)\s*(.*)$`)
	// `file:line kind date reason` (fixed-waits)
	lineRowRE = regexp.MustCompile(`^(\S+?):(\d+)\s+(.*)$`)
	// `pkg N` (dead_code)
	countRowRE = regexp.MustCompile(`^(\S+)\s+(\d+)$`)
	// `<name> <reason>` (namedpaths, generality fixtures)
	firstWordRE = regexp.MustCompile(`^(\S+)\s*(.*)$`)
	// `<tool>  # reason` (transcripts)
	transcriptRowRE = regexp.MustCompile(`^(\S+)\s*(#.*)?$`)
)

const ceilingNote = " Do not edit the `# ceiling:` line of the ledger: a ceiling above the count is accepted, and one closing card lowers it when the stream lands."

const draftRule = " Write the draft early and commit it before any extra probe."

// Ledgers is every ledger nova-card generates from, by name. The names are the
// words a coordinator uses for them; the files are where internal/ci keeps them.
var Ledgers = map[string]Ledger{
	"serial-tests": {
		Name: "serial-tests", File: "internal/ci/testdata/serial-tests_allowlist.txt", Test: "internal/ci TestEveryTestOpensWithTParallel", Tier: "flash",
		Task: "The ledger {{ledger}} lists {{count}} test(s) of {{file}} that do not open with t.Parallel(): {{rows}}. Each row's reason names what makes the test unsafe beside another (t.Setenv, t.Chdir, os.Setenv, os.Chdir, a swapped package variable). Delete those rows from the ledger first, so {{test}} goes red naming each test; then make every one of those tests open with t.Parallel() as its first statement by giving it a per-test seam in place of the process-wide call: cmd.Env on the command it runs instead of t.Setenv, a directory argument or t.TempDir instead of t.Chdir, an injected value instead of a package-level swap. The test's assertions stay as they are; only the seam changes. A test that cannot be made safe this way is reported, with its row left in place, as not done." + ceilingNote + draftRule,
		Parse: func(line string) (Row, bool) {
			m := colonRowRE.FindStringSubmatch(line)
			if m == nil {
				return Row{}, false
			}
			return Row{File: m[1], Key: m[2], Rest: m[3]}, true
		},
	},
	"slowwaits": {
		Name: "slowwaits", File: "internal/ci/testdata/slowwaits_allowlist.txt", Test: "internal/ci TestNoTestSleepsOverASecondOrWaitsOutADeadlineOverFive", Tier: "flash",
		Task: "The ledger {{ledger}} lists {{count}} test(s) of {{file}} that still write a sleep over one second or a deadline between five and thirty seconds: {{rows}}. Delete those rows from the ledger first, so {{test}} goes red naming each test; then give each test a bound under the rule's limit, or route its wait through the package's clock seam, keeping the assertion it makes. A test that waits a deadline out belongs behind the slow tag, and that move is reported, not made here." + draftRule,
		Parse: func(line string) (Row, bool) {
			m := colonRowRE.FindStringSubmatch(line)
			if m == nil {
				return Row{}, false
			}
			return Row{File: m[1], Key: m[2], Rest: m[3]}, true
		},
	},
	"sleeps-skips": {
		Name: "sleeps-skips", File: "internal/ci/sleeps-skips_allowlist.txt", Test: "internal/ci TestNoUnitTestWaitsOnTheWallClock", Tier: "flash",
		Task: "The SLEEPS ledger {{ledger}} lists {{count}} unit test(s) of the package {{file}} that wait on the wall clock, or skip themselves with t.Skip(\"SLEEPS: ...\"): {{rows}}. Delete those rows from the ledger first, so {{test}} goes red naming each one; then make each test wait on no real time: an injected clock, testing/synctest, or a signal from the code under test in place of time.Sleep, time.After and a wall-clock deadline (docs/TESTING.md: unit tests use no real time). A test that needs real time is a functional test and moves behind the functional tag, which is reported." + draftRule,
		Parse: func(line string) (Row, bool) {
			f := strings.Split(line, "\t")
			if len(f) < 2 || f[0] == "" || f[1] == "" {
				return Row{}, false
			}
			return Row{File: f[0], Key: f[1], Rest: strings.Join(f[2:], " ")}, true
		},
	},
	"fixed-waits": {
		Name: "fixed-waits", File: "internal/ci/testdata/fixed-waits-allowlist.txt", Test: "internal/ci TestNoFixedWaitsOnTheCIPath", Tier: "flash",
		Task: "The ledger {{ledger}} lists {{count}} fixed wait(s) in {{file}}: {{rows}} (file:line, the kind of wait, the date, the reason). Delete those rows from the ledger first, so {{test}} goes red naming each site; then replace each wait: a fixed sleep becomes a poll on the condition it waited for, a short bound becomes one the code under test is handed, an elapsed-time assertion becomes an assertion on what happened. The allowlist only shrinks: a row that names no offender is refused by the test." + draftRule,
		Parse: func(line string) (Row, bool) {
			m := lineRowRE.FindStringSubmatch(line)
			if m == nil {
				return Row{}, false
			}
			return Row{File: m[1], Key: m[2], Rest: m[3]}, true
		},
	},
	"dead-code": {
		Name: "dead-code", File: "internal/ci/testdata/dead_code_allowlist.txt", Test: "internal/ci TestDeadCode", Tier: "pro", Counted: true,
		Task: "The dead code ledger {{ledger}} carries the row `{{rows}}`: the package {{file}} has that many functions unreachable from any cmd/ root (the union over GOOS linux, darwin and windows). Run the class test {{test}} to read the names the deadcode tool reports for the package; delete each unreachable function with its tests and any helper only it used, then lower the package's count on its ledger row (to zero deletes the row) so {{test}} is green with fewer rows. A function that is reachable on one GOOS only is not dead and stays." + ceilingNote + draftRule,
		Parse: func(line string) (Row, bool) {
			m := countRowRE.FindStringSubmatch(line)
			if m == nil {
				return Row{}, false
			}
			return Row{File: m[1], Key: m[2], Rest: m[2]}, true
		},
	},
	"namedpaths": {
		Name: "namedpaths", File: "internal/ci/testdata/namedpaths_allowlist.txt", Test: "internal/ci TestEveryNamedRepoPathExists", Tier: "pro",
		Task: "The ledger {{ledger}} carries the row `{{rows}}`: the name {{file}} looks like a path into this repository and is not one, and the row says why. Find what writes the name (grep the tree for it), then either make the name true (create the file the sentence promises, or point the sentence at the file that exists) or reword the sentence so it names no path; then delete the row, so {{test}} is green with one row fewer. The list is checked in both directions: a stale row is as red as a missing one." + draftRule,
		Parse: func(line string) (Row, bool) {
			m := firstWordRE.FindStringSubmatch(line)
			if m == nil {
				return Row{}, false
			}
			return Row{File: m[1], Key: m[1], Rest: m[2]}, true
		},
	},
	"transcripts": {
		Name: "transcripts", File: "internal/ci/testdata/transcripts_allowlist.txt", Test: "internal/ci TestEveryTranscriptIsExecutedLineForLine", Tier: "pro",
		Task: "The ledger {{ledger}} carries the row `{{rows}}`: the `## {{file}}` section of docs/TESTS.md is executed by no test line for line through onboarding.CompareTranscript (docs/SPEC-TOOLWORK.md rule 3). Delete the row first, so {{test}} goes red naming the section; then write the test in cmd/{{file}} that runs the section's transcript line by line through onboarding.CompareTranscript, correcting the transcript where the tool's output has moved on, so the test is green. The list only shrinks." + draftRule,
		Parse: func(line string) (Row, bool) {
			m := transcriptRowRE.FindStringSubmatch(line)
			if m == nil || m[1] == "" {
				return Row{}, false
			}
			return Row{File: m[1], Key: m[1], Rest: strings.TrimSpace(m[2])}, true
		},
	},
	"generality-fixtures": {
		Name: "generality-fixtures", File: "internal/ci/testdata/generality_text_fixtures_allowlist.txt", Test: "internal/ci TestGeneralityText", Tier: "pro", Generated: true,
		Task: "The ledger {{ledger}} carries the row `{{rows}}`: the whole file {{file}} is excepted from the generality scan because it carries fleet, person or host names as recorded data. Decide whether it still must: a synthetic fixture is rewritten with generic names and its row deleted; a true recording keeps its row and this card reports not-done with the reason. Run {{test}} with NOVA_CI_UPDATE=1 once after the change and commit what it writes; this ledger is a generated one land regenerates on a conflict." + draftRule,
		Parse: func(line string) (Row, bool) {
			m := firstWordRE.FindStringSubmatch(line)
			if m == nil {
				return Row{}, false
			}
			return Row{File: m[1], Key: m[1], Rest: m[2]}, true
		},
	},
}

// LedgerNames is every ledger name, sorted, for the help and a refusal.
func LedgerNames() []string {
	names := make([]string, 0, len(Ledgers))
	for n := range Ledgers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ParseLedger reads a ledger file's rows in file order, skipping comments and blank
// lines. A row the ledger does not recognise is returned in skipped with its line.
func ParseLedger(l Ledger, text string) (rows []Row, skipped []string) {
	for i, line := range strings.Split(text, "\n") {
		t := strings.TrimRight(line, "\r")
		if strings.TrimSpace(t) == "" || strings.HasPrefix(strings.TrimSpace(t), "#") {
			continue
		}
		r, ok := l.Parse(t)
		if !ok {
			skipped = append(skipped, fmt.Sprintf("%s:%d: %s", l.File, i+1, t))
			continue
		}
		r.Line = i + 1
		rows = append(rows, r)
	}
	return rows, skipped
}

// Card is one planned brief before it is rendered: its id, the rows it covers, the
// paths it may touch, its test, tier, wave and dependencies (card ids).
type Card struct {
	ID    string
	File  string
	Rows  []Row
	Paths []string
	Test  string
	Tier  string
	Wave  int
	Deps  []string
	Task  string
	// Kind is the KIND: line: swarm.LedgerKind for a card cut from a ledger, fix-red for
	// every other generated card.
	Kind string
	// Ledger, Count and Counted are a ledger card's: the ledger file, what its entry for
	// File reads at the base (the number of rows for File, or, Counted, the count a
	// `pkg N` row carries), so the STOP line says the entry shrinks from Count to 0 with
	// the class test green.
	Ledger  string
	Count   int
	Counted bool
	// New is the NEW: line, the files the card creates (none for most): a test file
	// in a package that has none yet (NewTestFile).
	New []string
}

// Plan is a directory's worth of cards with what the planner had to leave out.
type Plan struct {
	Cards   []Card
	Waves   int
	Tier    string
	Skipped []string
	// Shared is true when two cards of the plan name one file in their PATHS and
	// neither needs the other: the add needs --allow-shared-paths.
	Shared bool
}

// MaxPaths is the PATHS ceiling: entries past it are merged into their directory's glob.
const MaxPaths = 8

// PlanLedger groups a ledger's rows by file (one card per file, in ledger order),
// computes each card's PATHS and TEST, and plans one wave with no dependency for
// every ledger: the lander resolves a ledger conflict as the union of removals, so
// adjacent deletions of one file no longer conflict (docs/SPEC-CARD-CONTRACT.md
// section 6, generated cards). Every card shares the ledger's path and none needs
// another, so Shared tells the add it wants --allow-shared-paths; a generated
// ledger (docs/SPEC-SPRINT.md section 7) plans the same. prefix opens every id;
// tier "" takes the ledger's own; max keeps the first max cards (0 is all), cut
// before the plan is rendered so no kept card needs a cut one.
func PlanLedger(l Ledger, rows []Row, prefix, tier string, max int) Plan {
	if tier == "" {
		tier = l.Tier
	}
	if prefix == "" {
		prefix = l.Name
	}
	var cards []Card
	index := map[string]int{}
	for _, r := range rows {
		i, ok := index[r.File]
		if !ok {
			index[r.File] = len(cards)
			cards = append(cards, Card{File: r.File, Test: l.Test, Tier: tier, Kind: swarm.LedgerKind, Ledger: l.File})
			i = len(cards) - 1
		}
		cards[i].Rows = append(cards[i].Rows, r)
		n := 1
		if v, err := strconv.Atoi(r.Key); l.Counted && err == nil {
			n = v // the row is `pkg N`, its Key N
		}
		cards[i].Count += n
		cards[i].Counted = l.Counted
	}
	cards = first(cards, max)
	seen := map[string]int{}
	for i := range cards {
		c := &cards[i]
		c.ID = uniqueID(prefix+"-"+Slug(c.File), seen)
		c.Paths = ledgerPaths(c.File, l.File)
		c.Task = ledgerTask(l, *c)
	}
	p := Plan{Cards: cards, Tier: tier, Waves: 1}
	for i := range p.Cards {
		p.Cards[i].Wave = 1
	}
	// every card is wave 1 and names the ledger in PATHS, and none needs another:
	// two cards make the add want --allow-shared-paths
	p.Shared = len(cards) > 1
	return p
}

// ledgerPaths is the PATHS of a card on file (a Go file, a package directory or a
// bare name) that also edits ledger: the file, its package's test files, the ledger.
// A name with no slash (a transcripts tool, a namedpaths word) adds no package glob.
func ledgerPaths(file, ledger string) []string {
	var paths []string
	switch {
	case strings.HasSuffix(file, ".go"):
		paths = append(paths, file, path.Dir(file)+"/*_test.go")
	case strings.Contains(file, "/"):
		paths = append(paths, file+"/*.go")
	}
	if file != ledger {
		paths = append(paths, ledger)
	}
	return MergePaths(paths)
}

// NewTestFile marks the test file a card creates: when its package's *_test.go glob
// names nothing at the base (exists reports it), the package has no test yet and the
// card writes <dir>/<file>_test.go, named on its NEW: line. The glob stays in PATHS:
// the land holds the diff to PATHS (internal/diffcheck), and the new file matches it.
func NewTestFile(c *Card, exists func(glob string) bool) {
	for _, p := range c.Paths {
		if strings.HasSuffix(p, "/*_test.go") && !exists(p) {
			c.New = append(c.New, path.Dir(p)+"/"+strings.TrimSuffix(path.Base(c.File), ".go")+"_test.go")
		}
	}
}

// MergePaths drops duplicates and an entry a sibling glob already covers, then, past
// MaxPaths, folds files into their directory's glob until the list fits.
func MergePaths(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		p = path.Clean(p)
		if p == "." || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	for len(out) > MaxPaths {
		// fold the first plain file into its directory's glob
		folded := false
		for i, p := range out {
			if strings.ContainsAny(p, "*?") {
				continue
			}
			glob := path.Dir(p) + "/*"
			out = append(out[:i], out[i+1:]...)
			if !seen[glob] {
				seen[glob] = true
				out = append(out, glob)
			}
			folded = true
			break
		}
		if !folded {
			break
		}
	}
	// an entry its own directory's glob covers is noise
	var final []string
	for _, p := range out {
		covered := false
		for _, q := range out {
			if q != p && strings.HasSuffix(q, "/*") && path.Dir(p) == strings.TrimSuffix(q, "/*") {
				covered = true
			}
		}
		if !covered {
			final = append(final, p)
		}
	}
	return final
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// Slug is a file's name as an id segment: lower case, runs of other characters as
// one hyphen, a trailing _test.go or .go dropped.
func Slug(file string) string {
	s := strings.ToLower(file)
	s = strings.TrimSuffix(s, "_test.go")
	s = strings.TrimSuffix(s, ".go")
	s = strings.Trim(slugRE.ReplaceAllString(s, "-"), "-")
	if s == "" {
		return "card"
	}
	return s
}

// DuplicateID is the first id two cards of a plan share, "" when every id is its own:
// a card is written as <id>.md, so a second card of one id would overwrite the first
// while the manifest listed both. The ledger and findings planners make ids unique
// (uniqueID); a tool named twice to --from help does not.
func DuplicateID(cards []Card) string {
	seen := map[string]bool{}
	for _, c := range cards {
		if seen[c.ID] {
			return c.ID
		}
		seen[c.ID] = true
	}
	return ""
}

// first is the first max cards of a plan in source order, every card when max is 0.
func first(cards []Card, max int) []Card {
	if max > 0 && len(cards) > max {
		return cards[:max]
	}
	return cards
}

func uniqueID(id string, seen map[string]int) string {
	n := seen[id]
	seen[id] = n + 1
	if n == 0 {
		return id
	}
	return fmt.Sprintf("%s-%d", id, n+1)
}

func ledgerTask(l Ledger, c Card) string {
	var rows []string
	for _, r := range c.Rows {
		rows = append(rows, strings.TrimSpace(strings.TrimSpace(r.Key+" "+r.Rest)))
	}
	rep := strings.NewReplacer(
		"{{file}}", c.File,
		"{{rows}}", strings.Join(rows, "; "),
		"{{ledger}}", l.File,
		"{{test}}", testName(l.Test),
		"{{count}}", fmt.Sprint(len(c.Rows)),
	)
	return rep.Replace(l.Task)
}

// stopCondition is what ends a card's task. A ledger card's class test is green at the
// base and stays green, so it ends when the ledger's entry for its file shrinks to 0;
// every other card's test is red before the change and green after it.
func stopCondition(c Card) string {
	if c.Kind != swarm.LedgerKind {
		return "the test " + testName(c.Test) + " is red before the change and green after it"
	}
	entry := "the ledger rows for %s in %s shrink"
	switch {
	case c.Counted:
		entry = "the count on the ledger row for %s in %s shrinks"
	case c.Count == 1:
		entry = "the ledger row for %s in %s shrinks"
	}
	return fmt.Sprintf(entry+" from %d to 0 and the class test %s stays green", c.File, c.Ledger, c.Count, testName(c.Test))
}

// testName is the TestX of a `pkg TestX` TEST line.
func testName(test string) string {
	f := strings.Fields(test)
	if len(f) == 0 {
		return "the class test"
	}
	return f[len(f)-1]
}

// testPackage is the pkg of a `pkg TestX` TEST line, "" for TEST: none.
func testPackage(test string) string {
	f := strings.Fields(test)
	if len(f) < 2 || f[0] == "none" {
		return ""
	}
	return f[0]
}

// Finding is one row of a findings TSV: file:line, what was found, the remedy, and
// the test (`pkg TestName`, or empty for none) that pins the fix.
type Finding struct {
	File   string
	Line   string
	What   string
	Remedy string
	Test   string
	Row    int
}

// ParseFindings reads a findings TSV: four tab-separated columns, file:line, finding,
// remedy, test; a header row whose first cell is `file` is skipped, as are comments.
func ParseFindings(text string) (findings []Finding, skipped []string) {
	for i, line := range strings.Split(text, "\n") {
		t := strings.TrimRight(line, "\r")
		if strings.TrimSpace(t) == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Split(t, "\t")
		if len(f) < 3 || (i == 0 && strings.EqualFold(strings.TrimSpace(f[0]), "file")) {
			if len(f) < 3 {
				skipped = append(skipped, fmt.Sprintf("findings:%d: %s", i+1, t))
			}
			continue
		}
		file, ln, _ := strings.Cut(strings.TrimSpace(f[0]), ":")
		fd := Finding{File: file, Line: ln, What: strings.TrimSpace(f[1]), Remedy: strings.TrimSpace(f[2]), Row: i + 1}
		if len(f) > 3 {
			fd.Test = strings.TrimSpace(f[3])
		}
		findings = append(findings, fd)
	}
	return findings, skipped
}

// PlanFindings is one card per file of a findings TSV, in first-seen order, every
// card wave 1 with no dependency (each touches its own file). The TEST is the first
// finding's test that names one; a card with none says `TEST: none ...` and is a
// read card is not: a finding is a fix, so the kind stays fix-red and a missing
// test is the card's first job, said in the task. max keeps the first max cards, 0 is all.
func PlanFindings(findings []Finding, prefix, tier string, max int) Plan {
	if tier == "" {
		tier = "pro"
	}
	if prefix == "" {
		prefix = "finding"
	}
	var cards []Card
	index := map[string]int{}
	seen := map[string]int{}
	count := map[string]int{}
	for _, f := range findings {
		i, ok := index[f.File]
		if !ok {
			index[f.File] = len(cards)
			cards = append(cards, Card{File: f.File, Tier: tier, Wave: 1, Kind: "fix-red", ID: uniqueID(prefix+"-"+Slug(f.File), seen)})
			i = len(cards) - 1
		}
		count[f.File]++
		c := &cards[i]
		if c.Test == "" && f.Test != "" {
			c.Test = f.Test
		}
		at := f.File
		if f.Line != "" {
			at += ":" + f.Line
		}
		c.Task += fmt.Sprintf(" At %s: %s. Remedy: %s.", at, strings.TrimSuffix(f.What, "."), strings.TrimSuffix(f.Remedy, "."))
	}
	cards = first(cards, max)
	for i := range cards {
		c := &cards[i]
		paths := []string{c.File}
		if strings.HasSuffix(c.File, ".go") {
			paths = append(paths, path.Dir(c.File)+"/*_test.go")
		}
		if pkg := testPackage(c.Test); pkg != "" {
			paths = append(paths, pkg+"/*_test.go")
		}
		c.Paths = MergePaths(paths)
		head := fmt.Sprintf("A reader's findings on %s, %d of them, each with its remedy.", c.File, count[c.File])
		if c.Test == "" {
			// no test named: the card names the one it must write, in the file's package
			c.Test = path.Dir(c.File) + " " + findingTestName(c.File)
			c.Task = head + c.Task + " No test pins these yet: write the red test first, the one the TEST line names, in the package's own _test.go, and it is the test this card lands with." + draftRule
		} else {
			c.Task = head + c.Task + " The test named on the TEST line is red before and green after." + draftRule
		}
	}
	return Plan{Cards: cards, Tier: tier, Waves: 1}
}

// HelpLineLimit is the width a help line keeps under (docs/STANDARD.md).
const HelpLineLimit = 100

// HelpExampleTest is the test a help card writes when its tool's package has none
// that runs the example lines; four of the twenty tools have it by this name.
const HelpExampleTest = "TestHelpExampleLinesRunAsPrinted"

var (
	testFuncRE    = regexp.MustCompile(`^func (Test[A-Za-z0-9_]*)\(`)
	exampleCallRE = regexp.MustCompile(`onboarding\.ExampleLines\(`)
)

// ExampleTest is the test that holds a tool's help examples to the binary, read off
// the texts of its package's _test.go files: HelpExampleTest when a file declares
// it, else the first test function whose body calls onboarding.ExampleLines, else
// "".
func ExampleTest(texts ...string) string {
	first := ""
	for _, text := range texts {
		fn := ""
		for _, line := range strings.Split(text, "\n") {
			if m := testFuncRE.FindStringSubmatch(line); m != nil {
				fn = m[1]
				if fn == HelpExampleTest {
					return fn
				}
				continue
			}
			if first == "" && fn != "" && exampleCallRE.MatchString(line) {
				first = fn
			}
		}
	}
	return first
}

// PlanHelp is one card for a tool from its rendered help: the lines over
// HelpLineLimit are listed; the terms and the examples are the model's reading, so
// the card is pro. help is the banner `tool help` printed; a tool with no long
// line and nothing else to say still gets a card, because the examples and the
// terms are the judgment the card asks for. test is the test of cmd/<tool> that
// runs the help's example lines (ExampleTest over the package's test files), and
// "" when the package has none or no checkout was read: then the card writes
// HelpExampleTest first and its gate is the package.
func PlanHelp(tool, help, test, prefix, tier string) Card {
	if tier == "" {
		tier = "pro"
	}
	if prefix == "" {
		prefix = "help"
	}
	var long []string
	for i, line := range strings.Split(help, "\n") {
		if len(line) > HelpLineLimit {
			long = append(long, fmt.Sprintf("%d (%d chars)", i+1, len(line)))
		}
	}
	task := fmt.Sprintf("Read `%s help` as a stranger who has the binary and nothing else, and make it cold-usable: ", tool)
	if len(long) > 0 {
		task += fmt.Sprintf("%d line(s) run over %d characters (help line %s); wrap each. ", len(long), HelpLineLimit, strings.Join(long, ", "))
	} else {
		task += fmt.Sprintf("no line runs over %d characters. ", HelpLineLimit)
	}
	task += "Then: every term the help uses is defined where it first appears or is a word a stranger knows (name each undefined one and define it in place); every example line runs as printed against the built tool (run each; one that does not is corrected or dropped); the first three lines after the banner are the flow a first user needs. The help lives in cmd/" + tool + "/main.go (its usage constant); "
	if test != "" {
		task += "the test " + test + " in the same package holds the examples to the binary and is red before and green after. "
	} else {
		test = HelpExampleTest
		task += "no test in cmd/" + tool + " runs the help's example lines through onboarding.ExampleLines: write " + test + " first, in the package's own _test.go, every example line run as printed and compared by onboarding.CompareTranscript, and the card's gate is go test ./cmd/" + tool + "/. "
	}
	task += "A change to behaviour is out of scope: words only." + draftRule
	return Card{
		ID:    prefix + "-" + Slug(tool),
		File:  "cmd/" + tool + "/main.go",
		Paths: MergePaths([]string{"cmd/" + tool + "/main.go", "cmd/" + tool + "/*_test.go", "docs/CLI.md"}),
		Test:  "cmd/" + tool + " " + test,
		Tier:  tier,
		Wave:  1,
		Kind:  "fix-red",
		Task:  task,
	}
}

// ClassRed is a base red on one class of the tree's class suite, as the lander's base
// gate found it (internal/sprint land_class.go; docs/SPEC-SPRINT.md section 7, the base's
// class gate): the class, the run that went red, the test that pins it ("pkg TestX", ""
// when the class is a run with no test, gofmt and vet), the files the run's output names,
// and the finding on one line.
type ClassRed struct {
	Class   string
	Run     string
	Test    string
	Files   []string
	Finding string
}

// PlanClassRed is the fix-red card the generator stamps for a base red on one class:
// its id is fix-red-<class>-<base> (one card per class and base, so the lander's judgment
// and a hand generation name the same card), its PATHS the files the finding names with
// their package's tests, and its TEST the class test. A class that is a run with no test
// (gofmt, vet) gets the class test the card writes in internal/ci, ClassTestName: fix-red
// is a gated kind, and its red and green is a test.
func PlanClassRed(r ClassRed, base, tier string) Card {
	if tier == "" {
		tier = "pro"
	}
	var paths []string
	for _, f := range r.Files {
		paths = append(paths, f)
		if strings.HasSuffix(f, ".go") {
			paths = append(paths, path.Dir(f)+"/*_test.go")
		}
	}
	file := "internal/ci"
	if len(r.Files) > 0 {
		file = r.Files[0]
	}
	test, gate := r.Test, "the test "+testName(r.Test)+" is red before and green after"
	if test == "" {
		test = "internal/ci " + ClassTestName(r.Class)
		gate = "no class test runs `" + r.Run + "` yet: write " + ClassTestName(r.Class) + " in internal/ci first (unless internal/ci holds it already), the run over the tree, red while it fails or prints, so it is red before the fix and green after"
	}
	if pkg := testPackage(test); pkg != "" {
		paths = append(paths, pkg+"/*_test.go")
	}
	return Card{
		ID:    "fix-red-" + Slug(r.Class) + "-" + Slug(base),
		File:  file,
		Paths: MergePaths(paths),
		Test:  test,
		Tier:  tier,
		Wave:  1,
		Kind:  "fix-red",
		Task:  fmt.Sprintf("The base %s is red on its class %s, so the lander lands nothing onto it until a head that cures it lands first. The run `%s` says: %s. Fix each finding at its cause in the files it names; %s. A ledger that only shrinks is not raised to make it pass.", base, r.Class, r.Run, strings.TrimSuffix(r.Finding, "."), gate) + draftRule,
	}
}

// ClassTestName is the internal/ci test a class with no test of its own is pinned by:
// TestTheTreePasses then the class in upper camel case (gofmt: TestTheTreePassesGofmt).
func ClassTestName(class string) string {
	return "TestTheTreePasses" + strings.TrimPrefix(findingTestName(class), "TestFinding")
}

// Header is what every brief of one generation shares.
type Header struct {
	Repo    string // owner/name
	Base    string // the branch
	Sha     string // the base sha, 40 hex
	Minutes int    // the deadline; 0 takes the tier's default
}

// Attribution is the line every brief carries about its commit's By: trailer. A brief
// never names its author: the deal may hand any card, a pinned one too, to any worker, a
// friend or a fleet machine, so the worker who does the attempt names itself, and the WHO
// line stays a preference, never an author (the coordinator's rule: a commit names the
// worker who did the work, never a model and never someone who did not). A model name is
// never a By:. Only a Claude worker writes a Co-Authored-By trailer, its true one; the
// line spells no fill-in template of it, which a worker of another model completes with
// its own model's name. internal/card refuses a brief that writes By: and a configured
// name (check author-name).
const Attribution = "ATTRIBUTION: By: your own name, the worker who does this attempt, on its own line at the end of every commit message; a model name is never a By:, and this brief names no author: its WHO line, if any, is a preference for who is dealt the card, never the name to sign. Below the By: line, a Claude worker adds its true Co-Authored-By trailer (Claude, its model, the noreply@anthropic.com address); any other worker adds no Co-Authored-By.\n"

// AsARead is the brief's AS A READ section, the text a reader of the work is given
// (sprint.FriendReadBrief carries it through the next heading): a By: trailer is judged
// only for being present and true, and the scope of the change is its PATHS line as
// AlwaysInPathsRule widens it. The sentence stays out of THE TASK: add's paths-cover-named
// check would otherwise refuse every generated brief for naming tla/RUNS.tsv, tla/CASES.tsv
// and internal/docs/catalog.go.
const AsARead = "AS A READ\nA By: trailer is judged only for being present and true: it names the worker who pushed the branch under read, whoever was preferred for the card. A trailer naming another friend than a WHO line or an earlier brief expected is no finding, and attribution alone never decides a verdict; read the change against the task, its test and its PATHS.\nThe scope of this change is its PATHS line. " + AlwaysInPathsRule + "\nA head whose only defect is PATHS is a HOLD with a PATHS-PROPOSED line for the worker, not a broken finding.\n"

// Deadline is the minutes a tier gets when the header names none.
func Deadline(tier string) int {
	if tier == "pro" {
		return 60
	}
	return 45
}

// modelGate is what a card whose PATHS reach tla/ adds to its STEP 4 gate: the model is run
// in the gate, by the tree's own tool on a TLC bench, and the RUNS.tsv it writes is committed
// with the change, because the lander refuses a head that edits a model or a configuration
// without a current record for each case it touches (internal/sprint/land_records.go;
// tla/README.md, "Refreshing the records after a model edit").
const modelGate = "The PATHS reach tla/, so the gate also runs the model: on a Linux TLC bench (java, the pinned jar at /opt/tla/tla2tools.jar; never a working machine), for each group g that `go run ./tools/tlacheck groups --root . --stale` lists (the groups of the cases your edit touched), run `make tlc TLC_JAR=/opt/tla/tla2tools.jar TLC_OUT=$JOB/scratch/tlc-$g TLC_GROUP=$g`, then `go run ./tools/tlacheck merge --root . --keep tla/RUNS.tsv --out tla/RUNS.tsv $JOB/scratch/tlc-*/RUNS.tsv`, until the groups command prints []; commit tla/RUNS.tsv with the change, never a row written by hand. The lander refuses a head that edits tla/*.tla or tla/*.cfg without a current RUNS.tsv row for each case it touches, naming the case."

// touchesModels says a card's PATHS reach a model or a configuration under tla/.
func touchesModels(paths []string) bool {
	for _, f := range []string{"tla/M.tla", "tla/MCM.cfg"} {
		if slices.ContainsFunc(paths, func(g string) bool { return hygiene.MatchGlob(g, f) }) {
			return true
		}
	}
	return slices.ContainsFunc(paths, func(g string) bool {
		return path.Dir(g) == "tla" && (path.Ext(g) == ".tla" || path.Ext(g) == ".cfg")
	})
}

// Render writes one brief: the header lines nova-sprint add reads, the paragraph
// every card of the night carried, the rules verbatim from the card template, the
// ATTRIBUTION line, the task, the steps, and the AS A READ section a reader is given
// (AsARead, which carries AlwaysInPathsRule).
// It is the card template's shape with the <...> filled, so it passes the add's lint
// and nova-swarm lint --card --child-rules by construction.
func Render(h Header, c Card) string {
	minutes := h.Minutes
	if minutes == 0 {
		minutes = Deadline(c.Tier)
	}
	sha12 := h.Sha
	if len(sha12) > 12 {
		sha12 = sha12[:12]
	}
	deps := "-"
	if len(c.Deps) > 0 {
		deps = strings.Join(c.Deps, ", ")
	}
	pkg := testPackage(c.Test)
	if pkg == "" {
		pkg = path.Dir(c.File)
		if !strings.Contains(pkg, "/") {
			pkg = "internal/ci"
		}
	}
	gate := "go test -count=1 -timeout 600s ./" + pkg + "/"
	if pkg != "internal/ci" && !strings.HasPrefix(pkg, "internal/ci") {
		gate += " ./internal/ci/"
	}
	paths, model := c.Paths, ""
	if touchesModels(paths) {
		model = " " + modelGate
		if !slices.ContainsFunc(paths, func(g string) bool { return hygiene.MatchGlob(g, "tla/"+tlc.RunsFile) }) {
			paths = append(slices.Clip(paths), "tla/"+tlc.RunsFile)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT: %s sha=%s tier: %s\n", c.ID, sha12, c.Tier)
	fmt.Fprintf(&b, "REPO: %s\n", h.Repo)
	fmt.Fprintf(&b, "BASE: %s\n", h.Base)
	fmt.Fprintf(&b, "KIND: %s\n", c.Kind)
	fmt.Fprintf(&b, "DEPENDS-ON: %s\n", deps)
	fmt.Fprintf(&b, "PATHS: %s\n", strings.Join(paths, ", "))
	if len(c.New) > 0 {
		fmt.Fprintf(&b, "NEW: %s\n", strings.Join(c.New, ", "))
	}
	fmt.Fprintf(&b, "TEST: %s\n", c.Test)
	fmt.Fprintf(&b, "START: %s, %s\n", c.File, pkg)
	fmt.Fprintf(&b, "STOP: %s, and the STEP 4 gate passes\n", stopCondition(c))
	// docs/SPEC-CARD-CONTRACT.md: the deadline is a bound; past it is the coordinator's judgment.
	fmt.Fprintf(&b, "Deadline: finish within %d minutes; the judgment of a card that runs past it is the coordinator's, so report what you have with the verdict not-done rather than push past it.\n", minutes)
	fmt.Fprintf(&b, "You are a child of the coordinator: one task, one staged checkout, one branch, unattended. This card is the whole task. Read $JOB/JOB.md first. Start at the current BASE tip; admission inspected exact base %s. Verify the defect still exists before editing; if already fixed report not-done with exact evidence rather than duplicate work. One change, one test that is red before and green after.\n", h.Sha)
	b.WriteString("Libraries considered: the Go standard library and testify, already in the tree; the package's own seams and helpers; no new dependency, and no helper over thirty lines without first searching the package for one.\n\n")
	b.WriteString(swarm.ChildRulesParagraph())
	b.WriteString("\n")
	b.WriteString(Attribution + "\n")
	fmt.Fprintf(&b, "THE TASK. %s The work lives in %s; the files this card may touch are its PATHS line and no other, in the staged checkout JOB.md names, on the card's own branch, from BASE %s.\n\n", c.Task, c.File, h.Base)
	b.WriteString("STEP 1. Enter the staged checkout JOB.md names with cd $JOB/repo && git log --oneline -1, no clone; work only on its own branch. Export GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 before any go command; " + swarm.GoCacheLine + " TMPDIR and GOTMPDIR are already set by the bench runner outside the source checkout: the native worker uses its slot temp and the remote gate uses its lane temp (docs/SPEC-SPRINT.md section 18, bench lanes). Set neither by hand. Scratch belongs under $JOB/scratch.\n")
	fmt.Fprintf(&b, "STEP 2. Make it red first, as the task says, with the test %s: run %s -run %s and keep the failing line as evidence.\n", testName(c.Test), gate, testName(c.Test))
	b.WriteString("STEP 3. Make it pass in the files this card names, and only those. Commit the draft on your own branch as soon as the test is green, before any further probe; a later commit may refine it. A change any other file needs goes in the report as a proposed diff, never a commit.\n")
	fmt.Fprintf(&b, "STEP 4. Run the gate: %s and read the last line of each. Run gofmt -l on every changed Go file; it must print nothing.%s %s\n", gate, model, swarm.GateNamesWhoseFile)
	fmt.Fprintf(&b, "STEP 5. Commit on your own branch with the trailer. Nothing reaches the forge from inside the wall: in the job the git shim records a push, the pull request is the finish JOB.md names (STEP 6), and the member makes both, against %s, from outside the wall when the card finishes. The pull request body states the diff stat, what was deleted, the tests with what each pins, and what was not done.\n", h.Base)
	b.WriteString("STEP 6. End as JOB.md says (docs/SPEC-CARD-CONTRACT.md): where JOB.md ends the card with its pull request, that is the end and there is nothing else to write, the gate's lines in the pull request body; where it asks for RESULT.md, write it in JOB.md's shape (head, branch, verdict, gate, output, report). For a friend's REPORT.md (docs/FRIENDS.md), first line exactly Verdict: LAND|HOLD|FAIL, second line exactly Head: <40-hex>; for HOLD and FAIL omit Head: and leave line 2 blank.\n")
	b.WriteString("\n" + AsARead)
	return b.String()
}

// LintFinding is one line the generator refuses a brief on.
type LintFinding struct {
	ID      string
	Check   string
	Line    int
	Excerpt string
}

func (f LintFinding) String() string {
	return fmt.Sprintf("LINT DRIFT card=%s check=%s line=%d: %s", f.ID, f.Check, f.Line, f.Excerpt)
}

var placeholderRE = regexp.MustCompile(`<[a-z][a-z0-9 -]*>`)

// Lint holds one rendered brief to what nova-sprint add holds it to (cmd/nova-sprint
// verbs.go lintBriefReads): the model lines of line 1, the child rules, the typed
// header and a tree card's steps; and to the template's own placeholders, which the
// add does not read but nova-swarm lint --card does. Empty means admitted.
func Lint(id, brief string) []LintFinding {
	var out []LintFinding
	add := func(check string, line int, excerpt string) {
		out = append(out, LintFinding{ID: id, Check: check, Line: line, Excerpt: excerpt})
	}
	if _, why := cardhdr.ReadModel(brief); why != "" {
		add("model-lines", 1, why)
	}
	for _, f := range swarm.LintCardChildWith([]byte(brief), swarm.DefaultChildRules) {
		add(f.Check, f.Line, f.Excerpt)
	}
	for _, f := range swarm.LintCardHeader([]byte(brief), nil, true) {
		add(f.Check, f.Line, f.Excerpt)
	}
	for _, f := range cardtree.Lint(brief) {
		add(f.Check, f.Line, f.Excerpt)
	}
	for i, line := range strings.Split(brief, "\n") {
		if m := placeholderRE.FindString(line); m != "" {
			add("placeholder", i+1, m)
		}
	}
	return out
}

// Manifest is the manifest.tsv of a directory: id, file, test, wave, deps, one card
// per line after the header.
func Manifest(p Plan) string {
	var b strings.Builder
	b.WriteString("id\tfile\ttest\twave\tdeps\n")
	for _, c := range p.Cards {
		deps := "-"
		if len(c.Deps) > 0 {
			deps = strings.Join(c.Deps, ",")
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\t%s\n", c.ID, c.File, c.Test, c.Wave, deps)
	}
	return b.String()
}

// OKLine is the one line a generation prints.
func OKLine(dir string, p Plan) string {
	return fmt.Sprintf("CARDS OK dir=%s cards=%d waves=%d tier=%s", dir, len(p.Cards), p.Waves, p.Tier)
}

// findingTestName is the Go test name a findings card with no test named is given:
// TestFinding then the file's base name in upper camel case.
func findingTestName(file string) string {
	base := strings.TrimSuffix(path.Base(file), path.Ext(file))
	var b strings.Builder
	b.WriteString("TestFinding")
	up := true
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if up && r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
			up = false
		default:
			up = true
		}
	}
	return b.String()
}
