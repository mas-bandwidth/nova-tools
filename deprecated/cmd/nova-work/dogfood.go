package main

// The dogfood verb is the link-mode measurement of nova-tools#2089, the numbers
// the absorb decision reads. Glenn, 2026-09-20: "I think it's safe to operate
// for a while in link mode and dogfood, before we decide on absorb mode for
// internally logged issues" -- and the dogfood is only useful for that decision
// if it produces numbers from day one, so this slice is the machinery that
// writes them, not a person keeping a spreadsheet.
//
// TWO LEDGERS, both written by machinery, neither estimated, both files under
// the one --ledger directory the caller names (kept in a repository, so the
// record outlives the bench that made it):
//
//	questions.jsonl  one row per question answered, tagged by its kind and by
//	                 the side that answered it -- link (nova-work in link
//	                 mode) or gh (today's way: gh issue list/view output read
//	                 into a model's context) -- with the tokens and the wall
//	                 clock the answer cost, as measured.
//	drift.jsonl      one row per difference found between the two sides: what
//	                 differed, which side was right, how it arose, how it was
//	                 found, the tokens and the minutes the repair cost, and
//	                 whether a decision was made on the stale copy before it
//	                 was caught.
//
// `dogfood report` prints the two tables -- one cost row per question kind,
// link against gh, and the drift log -- with the signals the issue named in
// advance so nobody argues after the fact: a steady trickle that reconcile
// repairs unattended is fine; drift that needs a person, or any decision made
// on a stale copy, is sync pain; a per-question saving that is large and
// obvious across most kinds is "radical". The round trips that stay on GitHub
// regardless (PRs, reviews, CI) are printed once on every table, because absorb
// cannot remove them and they cap the possible saving.
//
// Every path comes from a flag. There is no default ledger and no discovery:
// a missing flag is a refusal, never a guess. The question kinds, the sides,
// the drift causes and the finders are closed lists, the issue's own, so the
// replay set and the report cannot drift apart on naming.
import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The flag hints, one const each because a refusal that says what the input
// WANTS is the onboarding standard's own shape, and a hint is a claim about one
// flag that outlives the call site that prints it.
const (
	dogfoodLedgerHint = `--ledger <dir> is the dogfood ledger directory: questions.jsonl and drift.jsonl live in it, created on the first row; keep it in a repository so the record outlives the bench`
	dogfoodKindHint   = `--kind <kind> is the question's kind, one of: uid (lookup by uid), number (lookup by issue number or URL), subtree (what is open under this feature), since (what changed since an instant), blocks (what blocks X), percent (what % of the sprint is done)`
	dogfoodModeHint   = `--mode <mode> is the side that answered: link (nova-work in link mode) or gh (today's way, gh issue list/view output read into a model's context)`
	dogfoodTokensHint = `--tokens <n> is the token count the answer cost, as measured -- never estimated`
	dogfoodWallHint   = `--wall <duration> is the wall clock the answer took, as measured, such as 312ms or 4m10s`
	dogfoodWhatHint   = `--what <text> is what differed between the two sides, in one line`
	dogfoodRightHint  = `--right <side> is which side was right: link or gh`
	dogfoodAroseHint  = `--arose <cause> is how the drift arose: edit, issue-transfer, repo-rename, missed-capture, failed-back-pointer or human-edit (a person edited a back-pointer)`
	dogfoodFoundHint  = `--found <how> is how the drift was found: reconcile, person or wrong-answer`
	dogfoodRepairHint = `--repair-tokens <n> and --repair-minutes <n> are what the repair cost, as measured -- the inputs the absorb decision reads`
	dogfoodNowHint    = `--now <stamp> is the instant this row is recorded at, RFC3339; the default is this run's clock and an unparsable one is a refusal rather than a silent fall back to it`
	dogfoodFromHint   = `--from <stamp> is the first instant a row is counted from, RFC3339; the default is the whole ledger`
	dogfoodToHint     = `--to <stamp> is the last instant a row is counted to, RFC3339; the default is the whole ledger`
)

// The closed lists, the issue's own (#2089). The report reads rows against
// them, and a row that names none of them is a row nobody wrote with this
// machinery, which the loader refuses rather than quietly counting.
var (
	// dogfoodQuestionKinds is every kind of question the dogfood measures, both
	// ways: the lookups, the subtree, the history, the blockers and the sprint
	// percent (#2068).
	dogfoodQuestionKinds = []string{"uid", "number", "subtree", "since", "blocks", "percent"}
	// dogfoodSides is the two sides of every comparison: the link-mode session
	// and today's way.
	dogfoodSides = []string{"link", "gh"}
	// dogfoodCauses is how a drift arose, the issue's own list.
	dogfoodCauses = []string{"edit", "issue-transfer", "repo-rename", "missed-capture", "failed-back-pointer", "human-edit"}
	// dogfoodFoundBy is how a drift was found: by reconcile, by a person, or by
	// a wrong answer.
	dogfoodFoundBy = []string{"reconcile", "person", "wrong-answer"}
)

// The ledger files, fixed names under the --ledger directory so a report and a
// record cannot disagree about where a row lives.
const (
	dogfoodQuestionsFile = "questions.jsonl"
	dogfoodDriftFile     = "drift.jsonl"
)

// cmdDogfood is the verb's own switch: question records one answered question,
// drift records one difference with its repair cost, report prints the tables.
func cmdDogfood(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " dogfood", "no sub-verb given; the three are question, drift and report")
	}
	switch args[0] {
	case "question":
		return cmdDogfoodQuestion(args[1:], stdout, stderr)
	case "drift":
		return cmdDogfoodDrift(args[1:], stdout, stderr)
	case "report":
		return cmdDogfoodReport(args[1:], stdout, stderr)
	default:
		return refuse(stderr, " dogfood", fmt.Sprintf("unknown sub-verb %q; the three are question, drift and report", args[0]))
	}
}

// dogfoodQuestion is one row of questions.jsonl: one question, answered one
// way, and what it cost. Written by the record verb, read by the report, and
// never estimated.
type dogfoodQuestion struct {
	Kind   string `json:"kind"`
	Mode   string `json:"mode"`
	Tokens int64  `json:"tokens"`
	WallMS int64  `json:"wall_ms"`
	At     string `json:"at"`
}

// dogfoodDrift is one row of drift.jsonl: one difference between the two sides,
// and what repairing it cost.
type dogfoodDrift struct {
	What            string `json:"what"`
	Right           string `json:"right"`
	Arose           string `json:"arose"`
	Found           string `json:"found"`
	RepairTokens    int64  `json:"repair_tokens"`
	RepairMinutes   int64  `json:"repair_minutes"`
	DecisionOnStale bool   `json:"decision_on_stale"`
	At              string `json:"at"`
}

// cmdDogfoodQuestion records one answered question: the kind, the side that
// answered, the tokens and the wall clock. Both come from the measurement
// itself -- usage.tsv and session timestamps on a real bench -- so a run that
// cannot name them is a run that measured nothing and refuses rather than
// recording a guess.
func cmdDogfoodQuestion(args []string, stdout, stderr io.Writer) int {
	f := newFlags("dogfood question")
	ledger := f.fs.String("ledger", "", dogfoodLedgerHint)
	kind := f.fs.String("kind", "", dogfoodKindHint)
	mode := f.fs.String("mode", "", dogfoodModeHint)
	tokens := f.fs.Int64("tokens", -1, dogfoodTokensHint)
	wall := f.fs.Duration("wall", 0, dogfoodWallHint)
	now := f.fs.String("now", "", dogfoodNowHint)
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*ledger, "ledger", dogfoodLedgerHint)
	f.want(*kind, "kind", dogfoodKindHint)
	f.want(*mode, "mode", dogfoodModeHint)
	// A token count of zero is a real measurement -- a question answered from
	// what the session already held -- so the floor is zero and the refusal is
	// for the count that was never given or went negative.
	if *tokens < 0 {
		f.add("--tokens is required; it wants the measured token count of the answer, 0 or more; a negative count is a typo")
	}
	// The ledger stores whole milliseconds (wall_ms), so the floor is the
	// stored resolution: a positive duration under 1ms would be written as 0
	// and read back as a zero-cost measurement. Refuse it rather than record a
	// measurement the ledger cannot hold (stella's hold on #2762).
	if *wall <= 0 {
		f.add("--wall is required; it wants the measured wall clock of the answer, a positive duration such as 312ms or 4m10s; a zero or negative duration is a measurement with no time in it")
	} else if *wall < time.Millisecond {
		f.add(fmt.Sprintf("--wall %s is under the ledger's 1ms resolution; the ledger stores whole milliseconds, so it would be recorded as 0; give 1ms or more", oneline.Field(wall.String())))
	}
	if *kind != "" && !dogfoodKnown(*kind, dogfoodQuestionKinds) {
		f.add(fmt.Sprintf("--kind is one of %s, got %q", oneline.Escape(orList(dogfoodQuestionKinds)), *kind))
	}
	if *mode != "" && !dogfoodKnown(*mode, dogfoodSides) {
		f.add(fmt.Sprintf("--mode is one of %s, got %q", oneline.Escape(orList(dogfoodSides)), *mode))
	}
	at, ok := askNow(*now)
	if !ok {
		f.add(fmt.Sprintf("--now %q is not an RFC3339 instant", *now))
	}
	if f.refused(stderr) {
		return 2
	}
	if err := dogfoodLedgerDir(*ledger); err != nil {
		return refuse(stderr, " dogfood question", oneline.Err(err))
	}
	row := dogfoodQuestion{Kind: *kind, Mode: *mode, Tokens: *tokens, WallMS: wall.Milliseconds(), At: at.UTC().Format(time.RFC3339)}
	if err := dogfoodAppend(dogfoodLedgerPath(*ledger, dogfoodQuestionsFile), row); err != nil {
		return refuse(stderr, " dogfood question", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "DOGFOOD QUESTION OK kind=%s mode=%s tokens=%d wall=%s at=%s\n",
		oneline.Field(*kind), oneline.Field(*mode), *tokens,
		oneline.Field(wall.String()), oneline.Field(at.UTC().Format(time.RFC3339)))
	return 0
}

// cmdDogfoodDrift records one difference found between the two sides, with its
// repair cost. The repair cost is required, not defaulted: the absorb decision
// reads exactly these numbers, and a drift recorded without what it cost to
// repair is a row that says something happened and nothing about what keeping
// two sides charges for it.
func cmdDogfoodDrift(args []string, stdout, stderr io.Writer) int {
	f := newFlags("dogfood drift")
	ledger := f.fs.String("ledger", "", dogfoodLedgerHint)
	what := f.fs.String("what", "", dogfoodWhatHint)
	right := f.fs.String("right", "", dogfoodRightHint)
	arose := f.fs.String("arose", "", dogfoodAroseHint)
	found := f.fs.String("found", "", dogfoodFoundHint)
	repairTokens := f.fs.Int64("repair-tokens", -1, dogfoodRepairHint)
	repairMinutes := f.fs.Int64("repair-minutes", -1, dogfoodRepairHint)
	decisionOnStale := f.fs.Bool("decision-on-stale", false, "a decision was made on the stale copy before the drift was caught")
	now := f.fs.String("now", "", dogfoodNowHint)
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*ledger, "ledger", dogfoodLedgerHint)
	f.want(*what, "what", dogfoodWhatHint)
	f.want(*right, "right", dogfoodRightHint)
	f.want(*arose, "arose", dogfoodAroseHint)
	f.want(*found, "found", dogfoodFoundHint)
	if *repairTokens < 0 {
		f.add("--repair-tokens is required; it wants the measured token count of the repair, 0 or more; the absorb decision reads this number")
	}
	if *repairMinutes < 0 {
		f.add("--repair-minutes is required; it wants the measured minutes the repair took, 0 or more; the absorb decision reads this number")
	}
	if *right != "" && !dogfoodKnown(*right, dogfoodSides) {
		f.add(fmt.Sprintf("--right is one of %s, got %q", oneline.Escape(orList(dogfoodSides)), *right))
	}
	if *arose != "" && !dogfoodKnown(*arose, dogfoodCauses) {
		f.add(fmt.Sprintf("--arose is one of %s, got %q", oneline.Escape(orList(dogfoodCauses)), *arose))
	}
	if *found != "" && !dogfoodKnown(*found, dogfoodFoundBy) {
		f.add(fmt.Sprintf("--found is one of %s, got %q", oneline.Escape(orList(dogfoodFoundBy)), *found))
	}
	at, ok := askNow(*now)
	if !ok {
		f.add(fmt.Sprintf("--now %q is not an RFC3339 instant", *now))
	}
	if f.refused(stderr) {
		return 2
	}
	if err := dogfoodLedgerDir(*ledger); err != nil {
		return refuse(stderr, " dogfood drift", oneline.Err(err))
	}
	row := dogfoodDrift{What: *what, Right: *right, Arose: *arose, Found: *found,
		RepairTokens: *repairTokens, RepairMinutes: *repairMinutes,
		DecisionOnStale: *decisionOnStale, At: at.UTC().Format(time.RFC3339)}
	if err := dogfoodAppend(dogfoodLedgerPath(*ledger, dogfoodDriftFile), row); err != nil {
		return refuse(stderr, " dogfood drift", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "DOGFOOD DRIFT OK what=%s right=%s arose=%s found=%s repair-tokens=%d repair-minutes=%d decision-on-stale=%t at=%s\n",
		oneline.Field(*what), oneline.Field(*right), oneline.Field(*arose), oneline.Field(*found),
		*repairTokens, *repairMinutes, *decisionOnStale, oneline.Field(at.UTC().Format(time.RFC3339)))
	return 0
}

// cmdDogfoodReport prints the two tables and the signals: the cost of a
// question per kind (link against gh), the drift log with every row's repair
// cost, and the three signals the issue named in advance. --from and --to bound
// the rows the table counts, which is how one table a month is made; --max caps
// the drift rows behind one MORE line, the cap-and-count rule every listing
// here keeps.
func cmdDogfoodReport(args []string, stdout, stderr io.Writer) int {
	f := newFlags("dogfood report")
	ledger := f.fs.String("ledger", "", dogfoodLedgerHint)
	max := f.fs.Int("max", bounded.Default, "drift rows before one MORE line; 0 is every row")
	from := f.fs.String("from", "", dogfoodFromHint)
	to := f.fs.String("to", "", dogfoodToHint)
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*ledger, "ledger", dogfoodLedgerHint)
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already prints every row", *max))
	}
	var fromT, toT time.Time
	if *from != "" {
		t, err := time.Parse(time.RFC3339, *from)
		if err != nil {
			f.add(fmt.Sprintf("--from %q is not an RFC3339 instant", *from))
		} else {
			fromT = t.UTC()
		}
	}
	if *to != "" {
		t, err := time.Parse(time.RFC3339, *to)
		if err != nil {
			f.add(fmt.Sprintf("--to %q is not an RFC3339 instant", *to))
		} else {
			toT = t.UTC()
		}
	}
	if !fromT.IsZero() && !toT.IsZero() && fromT.After(toT) {
		f.add("--from is after --to; a window that ends before it starts is a typo with two readings")
	}
	if f.refused(stderr) {
		return 2
	}
	if info, err := os.Stat(*ledger); err != nil || !info.IsDir() {
		return refuse(stderr, " dogfood report", fmt.Sprintf("there is no ledger directory at %s; the record verbs create it on the first row; refusing to guess", oneline.Field(*ledger)))
	}
	questions, err := dogfoodLoadQuestions(dogfoodLedgerPath(*ledger, dogfoodQuestionsFile))
	if err != nil {
		return refuse(stderr, " dogfood report", oneline.Err(err))
	}
	drifts, err := dogfoodLoadDrift(dogfoodLedgerPath(*ledger, dogfoodDriftFile))
	if err != nil {
		return refuse(stderr, " dogfood report", oneline.Err(err))
	}
	questions = dogfoodQuestionsIn(questions, fromT, toT)
	drifts = dogfoodDriftsIn(drifts, fromT, toT)
	costs := dogfoodCosts(questions)

	fmt.Fprintf(stdout, "DOGFOOD REPORT questions=%d drifts=%d\n", len(questions), len(drifts))

	// The cost table, one row per kind that has runs, in the closed list's own
	// order. A side with no runs is a dash, never a zero: a side that was never
	// measured is not a side that measured zero, and the means are what the
	// runs actually recorded.
	for _, c := range costs {
		linkTokens, ghTokens, linkWall, ghWall, savingTokens, savingWall := "-", "-", "-", "-", "-", "-"
		if c.linkRuns > 0 {
			linkTokens = fmt.Sprintf("%d", dogfoodMean(c.linkTokens, c.linkRuns))
			linkWall = (time.Duration(dogfoodMean(c.linkMS, c.linkRuns)) * time.Millisecond).String()
		}
		if c.ghRuns > 0 {
			ghTokens = fmt.Sprintf("%d", dogfoodMean(c.ghTokens, c.ghRuns))
			ghWall = (time.Duration(dogfoodMean(c.ghMS, c.ghRuns)) * time.Millisecond).String()
		}
		if c.linkRuns > 0 && c.ghRuns > 0 {
			if s, ok := dogfoodSaving(dogfoodMean(c.linkTokens, c.linkRuns), dogfoodMean(c.ghTokens, c.ghRuns)); ok {
				savingTokens = fmt.Sprintf("%d", s)
			}
			if s, ok := dogfoodSaving(dogfoodMean(c.linkMS, c.linkRuns), dogfoodMean(c.ghMS, c.ghRuns)); ok {
				savingWall = fmt.Sprintf("%d", s)
			}
		}
		fmt.Fprintf(stdout, "DOGFOOD COST kind=%s link-runs=%d gh-runs=%d link-tokens=%s gh-tokens=%s link-wall=%s gh-wall=%s saving-tokens=%s saving-wall=%s\n",
			oneline.Field(c.kind), c.linkRuns, c.ghRuns,
			oneline.Field(linkTokens), oneline.Field(ghTokens),
			oneline.Field(linkWall), oneline.Field(ghWall),
			oneline.Field(savingTokens), oneline.Field(savingWall))
	}

	// The drift log, one row per difference found, in the order they were
	// recorded, capped behind one MORE line.
	list := bounded.Capped(stdout, *max, "DOGFOOD", "drift", "widen --max or pass --max 0")
	for i, d := range drifts {
		list.Line(fmt.Sprintf("DOGFOOD DRIFT row=%d what=%s right=%s arose=%s found=%s repair-tokens=%d repair-minutes=%d decision-on-stale=%t",
			i+1, oneline.Field(d.What), oneline.Field(d.Right), oneline.Field(d.Arose), oneline.Field(d.Found),
			d.RepairTokens, d.RepairMinutes, d.DecisionOnStale))
	}
	list.More()

	// The signals, named in advance (#2089) so nobody argues after the fact.
	needingPerson, onStale := 0, 0
	for _, d := range drifts {
		if d.Found == "person" || d.Found == "wrong-answer" {
			needingPerson++
		}
		if d.DecisionOnStale {
			onStale++
		}
	}
	bothWays, largeObvious := 0, 0
	for _, c := range costs {
		if c.linkRuns == 0 || c.ghRuns == 0 {
			continue
		}
		bothWays++
		st, okT := dogfoodSaving(dogfoodMean(c.linkTokens, c.linkRuns), dogfoodMean(c.ghTokens, c.ghRuns))
		sw, okW := dogfoodSaving(dogfoodMean(c.linkMS, c.linkRuns), dogfoodMean(c.ghMS, c.ghRuns))
		// Large and obvious is at least half the tokens and half the wall saved
		// on one kind; "across most kinds" is more than half of the kinds
		// measured both ways, and at least two of them, because one kind
		// measured is a data point and not a pattern.
		if okT && okW && st >= 50 && sw >= 50 {
			largeObvious++
		}
	}
	trickle, stale := "fine", "fine"
	if needingPerson > 0 {
		trickle = "sync-pain"
	}
	if onStale > 0 {
		stale = "sync-pain"
	}
	saving := "not-yet"
	if bothWays >= 2 && largeObvious*2 > bothWays {
		saving = "radical"
	}
	fmt.Fprintf(stdout, "DOGFOOD SIGNAL name=drift-repairs-unattended state=%s drifts=%d needing-a-person=%d\n",
		oneline.Field(trickle), len(drifts), needingPerson)
	fmt.Fprintf(stdout, "DOGFOOD SIGNAL name=decision-on-a-stale-copy state=%s made=%d\n",
		oneline.Field(stale), onStale)
	fmt.Fprintf(stdout, "DOGFOOD SIGNAL name=per-question-saving state=%s kinds-both-ways=%d large-and-obvious=%d\n",
		oneline.Field(saving), bothWays, largeObvious)

	// What stays on GitHub regardless, recorded once on every table: absorb
	// cannot remove these round trips, and they cap the possible saving.
	fmt.Fprintln(stdout, "DOGFOOD CAP stays-on-github=prs,reviews,ci caps=the-possible-saving")

	if err := list.Err(); err != nil {
		return refuse(stderr, " dogfood report", oneline.Err(err))
	}
	return 0
}

// dogfoodKnown says whether v is one of the closed list, the reader every flag
// and every loaded row is checked against.
func dogfoodKnown(v string, list []string) bool {
	for _, one := range list {
		if v == one {
			return true
		}
	}
	return false
}

// dogfoodLedgerPath is one ledger file under the directory the caller named.
func dogfoodLedgerPath(dir, name string) string {
	return dir + "/" + name
}

// dogfoodLedgerDir makes the ledger directory when it is not there yet, and
// refuses when the path names a file: a ledger is a directory of two files,
// never one file with two kinds of row in it.
func dogfoodLedgerDir(dir string) error {
	if info, err := os.Stat(dir); err == nil {
		if info.IsDir() {
			return nil
		}
		return fmt.Errorf("the ledger path %s is a file; a ledger is a directory holding questions.jsonl and drift.jsonl", dir)
	}
	return os.MkdirAll(dir, 0o755)
}

// dogfoodAppend writes one row onto a ledger file. It never reads and rewrites
// the file: the row is marshalled first and written as ONE write on a file
// opened O_APPEND, so the kernel places each row at the end of whatever is
// there at the moment of the write. Two coordinator or lane calls recording at
// the same instant each land their row; neither can overwrite the other's, as
// a read-then-rewrite would (stella's hold on #2762).
func dogfoodAppend(path string, row any) error {
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	// fmt.Fprintf formats into its own buffer and hands the file one Write.
	// oneline.Escape leaves json.Marshal's output byte-identical except for a
	// raw bidi override in a string, which it renders as the \uXXXX escape the
	// JSON decoder reads back as the same rune.
	if _, err := fmt.Fprintf(f, "%s\n", oneline.Escape(string(b))); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// dogfoodLoadQuestions reads questions.jsonl whole: one JSON row per line, a
// line nobody can parse is an error naming it, and a row whose kind, mode or
// instant this machinery never writes is an error too, because a ledger the
// report quietly skipped rows of is a table that lies.
func dogfoodLoadQuestions(path string) ([]dogfoodQuestion, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []dogfoodQuestion
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r dogfoodQuestion
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s: line %d is not a question row: %w", path, i+1, err)
		}
		if !dogfoodKnown(r.Kind, dogfoodQuestionKinds) || !dogfoodKnown(r.Mode, dogfoodSides) {
			return nil, fmt.Errorf("%s: line %d holds a kind or a mode this machinery never wrote", path, i+1)
		}
		if _, ok := dogfoodStamp(r.At); !ok {
			return nil, fmt.Errorf("%s: line %d holds an at that is not an RFC3339 instant", path, i+1)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// dogfoodLoadDrift reads drift.jsonl whole, on the same terms as the question
// ledger: every line parses, every closed-list field is one this machinery
// writes, and the instant is an instant.
func dogfoodLoadDrift(path string) ([]dogfoodDrift, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []dogfoodDrift
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r dogfoodDrift
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s: line %d is not a drift row: %w", path, i+1, err)
		}
		if !dogfoodKnown(r.Right, dogfoodSides) || !dogfoodKnown(r.Arose, dogfoodCauses) || !dogfoodKnown(r.Found, dogfoodFoundBy) {
			return nil, fmt.Errorf("%s: line %d holds a right, an arose or a found this machinery never wrote", path, i+1)
		}
		if _, ok := dogfoodStamp(r.At); !ok {
			return nil, fmt.Errorf("%s: line %d holds an at that is not an RFC3339 instant", path, i+1)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// dogfoodStamp parses one row's recorded instant.
func dogfoodStamp(at string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// dogfoodQuestionsIn keeps the question rows recorded inside the window. The
// loader already refused a row whose instant it could not parse, so every row
// lands on one side of the window or the other.
func dogfoodQuestionsIn(rows []dogfoodQuestion, from, to time.Time) []dogfoodQuestion {
	var out []dogfoodQuestion
	for _, r := range rows {
		if dogfoodRowIn(r.At, from, to) {
			out = append(out, r)
		}
	}
	return out
}

// dogfoodDriftsIn keeps the drift rows recorded inside the window, on the same
// terms.
func dogfoodDriftsIn(rows []dogfoodDrift, from, to time.Time) []dogfoodDrift {
	var out []dogfoodDrift
	for _, r := range rows {
		if dogfoodRowIn(r.At, from, to) {
			out = append(out, r)
		}
	}
	return out
}

// dogfoodRowIn says whether one recorded instant falls inside the window; a
// zero bound on either side is no bound, which is how a report with no --from
// and no --to reads the whole ledger.
func dogfoodRowIn(at string, from, to time.Time) bool {
	t, ok := dogfoodStamp(at)
	if !ok {
		return false
	}
	if !from.IsZero() && t.Before(from) {
		return false
	}
	if !to.IsZero() && t.After(to) {
		return false
	}
	return true
}

// dogfoodCost is one kind's cost, both sides, aggregated over the runs the
// table counts.
type dogfoodCost struct {
	kind       string
	linkRuns   int
	ghRuns     int
	linkTokens int64
	ghTokens   int64
	linkMS     int64
	ghMS       int64
}

// dogfoodCosts aggregates the question rows per kind, in the closed list's own
// order, keeping only the kinds that have runs.
func dogfoodCosts(rows []dogfoodQuestion) []dogfoodCost {
	var costs []dogfoodCost
	for _, kind := range dogfoodQuestionKinds {
		var c dogfoodCost
		for _, r := range rows {
			if r.Kind != kind {
				continue
			}
			switch r.Mode {
			case "link":
				c.linkRuns++
				c.linkTokens += r.Tokens
				c.linkMS += r.WallMS
			case "gh":
				c.ghRuns++
				c.ghTokens += r.Tokens
				c.ghMS += r.WallMS
			}
		}
		if c.linkRuns == 0 && c.ghRuns == 0 {
			continue
		}
		c.kind = kind
		costs = append(costs, c)
	}
	return costs
}

// dogfoodMean is the integer mean of the runs: the tables a person reads, not
// a claim of precision the measurements never had.
func dogfoodMean(sum int64, runs int) int64 {
	if runs == 0 {
		return 0
	}
	return sum / int64(runs)
}

// dogfoodSaving is what the link side saved against the gh side, in whole
// percent, and false when there is no gh side to save against. A negative
// saving is a saving the link side owed, and prints as one.
func dogfoodSaving(link, gh int64) (int64, bool) {
	if gh <= 0 {
		return 0, false
	}
	return (gh - link) * 100 / gh, true
}
