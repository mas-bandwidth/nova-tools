package swarm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// TRIAGE: ONE PAGE, AND THE TERMINAL IS AN INDEX TO IT.
//
// The coordinator's window never holds a worker's transcript, never a raw RESULT.md unless
// it asks for one by id, and never the runner's log: on 2026-09-11 the window read twenty
// reports of forty lines each and wrote twenty prompts of thirty lines each, and that cost
// is the coordinator's tokens and the tool's to remove.
//
// A REPORT IS READ AS ONE REVISION. The tool reads RESULT.md into memory, hashes the bytes,
// parses that buffer, and hashes the file again before recording anything; a second hash
// that differs is TRIAGE SKIPPED, nothing is recorded as consumed, and the next run takes
// it whole. RESULT.md.tmp is never opened. There is no mtime anywhere in this tool.

// TriageState is <pool>/triage.json: what has been folded into a page already.
type TriageState struct {
	Version   int               `json:"version"`
	Runs      int               `json:"runs"`
	LastISO   string            `json:"last_iso"`
	LastCount int               `json:"last_count"`
	Consumed  map[string]string `json:"consumed"`
}

// TriageInput is one triage run.
type TriageInput struct {
	Pool           *Pool
	Batch          string
	Dirs           []string
	Since          string
	All            bool
	NoState        bool
	Max            int
	Owed           []string
	Stdout, Stderr io.Writer
	Now            func() time.Time

	// pauseAfterFirstHash is demanded test 16's INJECTED PAUSE, and the only seam in this
	// tool: it runs between the first hash of a report and the parse of that buffer, which
	// is the window a third revision has to land in. It is unexported and set by nothing
	// but this package's own tests -- no flag, no environment variable, no production
	// caller -- because a pause a caller could ask for is a pause a pool could be left in.
	pauseAfterFirstHash func()
}

type folded struct {
	sc     Sidecar
	report Report
	from   string
}

// Triage walks the jobs, folds every revision it has not folded, and writes one page.
func Triage(in TriageInput) int {
	p := in.Pool
	out := in.Stdout

	state := TriageState{Version: 1, Consumed: map[string]string{}}
	if !in.All {
		_ = ReadJSON(p.Path(TriageStateFile), &state)
		if state.Consumed == nil {
			state.Consumed = map[string]string{}
		}
	}

	jobs, err := p.Jobs()
	if err != nil {
		fmt.Fprintf(in.Stderr, "TRIAGE REFUSED: the pool could not be walked: %s\n", oneline.Escape(redactedReason(err)))
		return 2
	}
	if in.Batch != "" {
		var kept []Sidecar
		for _, sc := range jobs {
			if sc.Batch == in.Batch {
				kept = append(kept, sc)
			}
		}
		if len(kept) == 0 {
			fmt.Fprintf(in.Stderr, "TRIAGE REFUSED: no sidecar in %s carries batch=%s; `nova-swarm status --pool %s` lists what is here\n",
				oneline.Field(p.Dir), oneline.Field(in.Batch), p.Dir)
			return 1
		}
		jobs = kept
	}
	if in.Since != "" {
		var kept []Sidecar
		for _, sc := range jobs {
			if sc.ID >= in.Since {
				kept = append(kept, sc)
			}
		}
		jobs = kept
	}

	reports := bounded.Capped(out, in.Max, "TRIAGE", "report", "nova-swarm triage --pool "+p.Dir+" --max 0")
	var kept []folded
	counts := map[string]int{}
	// `reports=` is the number of jobs that HAVE a report to read, not the number of jobs:
	// demanded test 8's eight jobs, one of them with no RESULT.md, print `reports=7
	// … no_result=1` (SPEC-SWARM.md:1253).
	skipped, malformedN, reportsN := 0, 0, 0
	for _, sc := range jobs {
		// A JOB THE MODEL COULD NOT TAKE SAYS SO HERE, FROM ITS SIDECAR (#103). Two Freddy
		// reads of whole specs died on OpenCode's input limit on 2026-09-12 and triage said
		// nothing at all about them: a job with no report is counted `no_result`, and the
		// one fact that explains it was in a log that `reclaim` then deleted. The sidecar
		// carries the provider's own sentence, so this line costs no file and no log, and
		// the report below is still folded -- a worker that appended findings before it was
		// cut off keeps them (rule 3).
		if sc.End == EndInputLimit {
			reports.Line(fmt.Sprintf("TRIAGE INPUT-LIMIT id=%s job=%s: %s",
				oneline.Field(sc.ID), oneline.Field(dashOr(sc.Label)),
				oneline.Escape(oneline.Cap(dashOr(sc.Limit), oneline.TailBytes))))
		}
		raw, from, err := p.ReportBytes(sc)
		if err != nil {
			counts[ClassNoResult]++
			continue
		}
		reportsN++
		first := HashBytes(raw)
		if in.pauseAfterFirstHash != nil {
			in.pauseAfterFirstHash()
		}
		report := ParseReport(raw)
		again, err := p.rehash(from)
		if err != nil || again != first {
			// A writer that ignored the protocol and appended in place. Not folded this
			// run, not recorded, taken whole by the next one when it holds still.
			fmt.Fprintf(out, "TRIAGE SKIPPED id=%s: changed while read\n", oneline.Field(sc.ID))
			skipped++
			continue
		}
		counts[report.Class]++
		if report.Class == ClassMalformed {
			malformedN++
			fmt.Fprintf(out, "TRIAGE QUARANTINED id=%s rev=%s line=%d: not folded; nova-swarm result --pool %s --id %s\n",
				oneline.Field(sc.ID), oneline.Field(Short(first)), report.MalformedLine, p.Dir, oneline.Field(sc.ID))
			continue
		}
		if !in.All && state.Consumed[sc.ID] == first {
			continue
		}
		kept = append(kept, folded{sc: sc, report: report, from: from})
		reports.Line(fmt.Sprintf("TRIAGE REPORT id=%s rev=%s job=%s result=%s items=%d red=%d green=%d notdone=%d: %s",
			oneline.Field(sc.ID), oneline.Field(Short(first)), oneline.Field(dashOr(sc.Label)), oneline.Field(report.Class),
			len(report.Items), report.Red(), report.Green(), report.NotDone(),
			oneline.Escape(oneline.Cap(dashOr(report.Heading), oneline.TailBytes))))
	}
	reports.More()

	// Rule 15's de-duplication, by (repo, rev, file, line, rule): two findings merge only
	// when both reports carry repo AND rev and they are equal, so equal file:line and rule
	// in two codebases, or in two revisions of one, are two findings.
	var order []*merged
	byKey := map[string]*merged{}
	// EVERY FINDING IS COUNTED EXACTLY ONCE (SPEC-SWARM.md:621, "`new` is findings not
	// marked `dup:`"). A finding is `dup` because the worker marked it, because it matches
	// an item the owed list already carries (rule 1), or because it folds into a finding
	// already seen under the same (repo, rev, file, line, rule) (rule 15) -- and never for
	// two of those at once. Before this, a marked duplicate that also folded was counted
	// twice and decremented `new`, so three identical `dup:` findings printed `new=-2`.
	findings, new_, dup, unquoted := 0, 0, 0, 0
	for _, k := range kept {
		for _, f := range k.report.FindingLines {
			findings++
			if !f.Quoted() {
				unquoted++
			}
			isDup := f.Dup || OwedMatch(f, in.Owed)
			key, ok := f.Key(k.report.Repo, k.report.Rev)
			if ok {
				if m, seen := byKey[key]; seen {
					m.jobs = append(m.jobs, k.sc.ID)
					dup++
					continue
				}
				if isDup {
					dup++
				} else {
					new_++
				}
				m := &merged{f: f, jobs: []string{k.sc.ID}, from: k.from}
				byKey[key] = m
				order = append(order, m)
				continue
			}
			if isDup {
				dup++
			} else {
				new_++
			}
			order = append(order, &merged{f: f, jobs: []string{k.sc.ID}, from: k.from})
		}
	}

	accurate, wrong, anyVerdict := 0, 0, false
	for _, sc := range jobs {
		if sc.Verdict != nil {
			anyVerdict = true
			accurate += sc.Verdict.Accurate
			wrong += sc.Verdict.Wrong
		}
	}
	verdict := func(n int) string {
		if !anyVerdict {
			return Dash
		}
		return fmt.Sprint(n)
	}
	budgetEnded := 0
	for _, sc := range jobs {
		if sc.End == EndBudget || sc.End == EndUnverifiable {
			budgetEnded++
		}
	}

	page, pageErr := in.writePage(kept, order)
	fmt.Fprintf(out, "TRIAGE BATCH batch=%s reports=%d findings=%d new=%d dup=%d unquoted=%d clean=%d plan_only=%d no_result=%d malformed=%d budget=%d accurate=%s wrong=%s\n",
		oneline.Field(dashOr(in.Batch)), reportsN, findings, new_, dup, unquoted,
		counts[ClassClean], counts[ClassPlanOnly], counts[ClassNoResult], malformedN, budgetEnded,
		verdict(accurate), verdict(wrong))

	items := bounded.Capped(out, in.Max, "TRIAGE", "finding", "--max 0")
	for _, m := range order {
		jobsField := strings.Join(m.jobs, ",")
		items.Line(fmt.Sprintf("TRIAGE FINDING jobs=%s at=%s: %s",
			oneline.Field(jobsField), oneline.Field(dashOr(m.f.File+":"+m.f.FileLine)),
			oneline.Escape(oneline.Cap(m.f.Text, oneline.TailBytes))))
	}
	if items.Elided() > 0 {
		fmt.Fprintf(out, "TRIAGE MORE kind=finding shown=%d total=%d at=%s --max 0\n", items.Shown(), items.Total(), oneline.Field(page))
	}

	red, green, notdone, itemsN := 0, 0, 0, 0
	for _, k := range kept {
		red += k.report.Red()
		green += k.report.Green()
		notdone += k.report.NotDone()
		itemsN += len(k.report.Items)
	}
	if pageErr != nil {
		fmt.Fprintf(in.Stderr, "TRIAGE REFUSED: the page could not be written: %s\n", oneline.Escape(redactedReason(pageErr)))
		return 2
	}
	// ONE TOKEN, ONE MEANING (lesson 119): `reports=` on TRIAGE BATCH counts the jobs that
	// HAVE a report, and this line counts what this run FOLDED into the page -- two shapes
	// that shared the token `reports=` three lines apart (the new-user audit, S6).
	fmt.Fprintf(out, "TRIAGE OK folded=%d template=%d malformed=%d skipped=%d items=%d red=%d green=%d notdone=%d page=%s\n",
		len(kept), len(kept), malformedN, skipped, itemsN, red, green, notdone, oneline.Field(page))

	if !in.NoState && !in.All {
		for _, k := range kept {
			state.Consumed[k.sc.ID] = k.report.Hash
		}
		state.Runs++
		state.LastISO = Stamp(in.Now())
		state.LastCount = len(kept)
		_ = WriteJSON(p.Path(TriageStateFile), state)
	}
	return 0
}

// TriageStateFile is the consumed set's file name.
const TriageStateFile = "triage.json"

// merged is one finding after rule 15's fold, and every job that reported it.
type merged struct {
	f    Finding
	jobs []string
	from string
}

// writePage is the artifact: one page per run, carrying each report's heading, its Per item
// table and its Left owed list, in job-id order, oldest first -- and then THE FINDINGS,
// FOLDED, each carrying every contributing job id.
//
// SPEC-SWARM.md:297: "the merged finding carries every contributing job id,
// `jobs=<id,id,…>`, on the page and on its triage line." The page carried each report's raw
// lines instead: two workers finding one thing were two findings on the page and one in the
// count, and the page -- the artifact that is KEPT -- was the one that disagreed.
func (in TriageInput) writePage(kept []folded, order []*merged) (string, error) {
	path := in.Pool.Path(Reports, in.Now().UTC().Format("20060102T150405Z")+".md")
	var b strings.Builder
	fmt.Fprintf(&b, "# triage %s\n\n", Stamp(in.Now()))
	sort.Slice(kept, func(i, j int) bool { return kept[i].sc.ID < kept[j].sc.ID })
	for _, k := range kept {
		fmt.Fprintf(&b, "## %s\n\n", k.sc.ID)
		fmt.Fprintf(&b, "- rev: `%s`\n- path: `%s`\n- result: %s\n\n", Short(k.report.Hash), k.from, k.report.Class)
		if k.report.Heading != "" {
			fmt.Fprintf(&b, "%s\n\n", k.report.Heading)
		}
		if len(k.report.Items) > 0 {
			b.WriteString("| item | state | evidence |\n| --- | --- | --- |\n")
			for _, it := range k.report.Items {
				fmt.Fprintf(&b, "| %s | %s | %s |\n", it.Text, it.State, it.Evidence)
			}
			b.WriteString("\n")
		}
		if len(k.report.LeftOwed) > 0 {
			b.WriteString("Left owed:\n\n")
			for _, owed := range k.report.LeftOwed {
				fmt.Fprintf(&b, "- %s\n", owed)
			}
			b.WriteString("\n")
		}
		if k.report.OneLine != "" {
			fmt.Fprintf(&b, "One line: %s\n\n", k.report.OneLine)
		}
	}
	if len(order) > 0 {
		b.WriteString("## Findings\n\n")
		for _, m := range order {
			fmt.Fprintf(&b, "- jobs=%s at=%s: %s\n", strings.Join(m.jobs, ","), dashOr(m.f.File+":"+m.f.FileLine), m.f.Text)
		}
		b.WriteString("\n")
	}
	return path, writeAtomic(path, []byte(b.String()), 0o644)
}

// Jobs is every task the pool knows about whose result triage may count, in id order.
//
// RULE 11 (SPEC-SWARM.md:156): a job whose group left a survivor "moves to `failed/` with
// `violation=background` in the sidecar, and `triage` does not count it". The result is
// QUARANTINED: a report written beside a process that was still running when it was read
// is not evidence, and folding it into a coordinator's page is the one way a quarantined
// result reaches a person as though it were a finished one. `result --id` is how it is
// read (rule 15), and it is still counted by `status` and by `cost`.
func (p *Pool) Jobs() ([]Sidecar, error) {
	var out []Sidecar
	for _, state := range []string{Running, Done, Failed} {
		list, err := p.List(state)
		if err != nil {
			return nil, err
		}
		for _, sc := range list {
			if sc.Violation != "" {
				continue
			}
			out = append(out, sc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ReportBytes reads a job's report from the RETAINED copy for a finalized job and from the
// job directory only for a running one, so the first triage after a reclaim reads the same
// bytes it would have read before it.
//
// BOTH READS ARE STEADY. Each of these paths is published by a rename -- the retained copy
// by `writeAtomic`, the live RESULT.md by the harness -- so on Windows a read landing inside
// that replace window fails for microseconds and says nothing about the job. Read as a fact
// it made a published report a `no_result` on the page and threw the worker's findings away:
// the class this branch closed for exit.json and for `finish`'s read of the same file. A
// record that is GONE still answers at once, so the ErrNotExist fall-through below keeps its
// meaning.
func (p *Pool) ReportBytes(sc Sidecar) ([]byte, string, error) {
	retained := filepath.Join(p.ReportsDir(sc.ID), CopiedResult)
	if raw, err := readFileSteady(retained); err == nil {
		return raw, retained, nil
	}
	if sc.Job == "" {
		return nil, "", os.ErrNotExist
	}
	live := ResultPath(sc.Job)
	raw, err := readFileSteady(live)
	return raw, live, err
}

// rehash is rule 16's proof that the buffer triage parsed is still what is on disk. It is
// STEADY for a reason of its own: a bare read here made a replace COLLISION indistinguishable
// from the one thing the rule exists to catch, a writer appending in place, and a report that
// had not changed by one byte printed `TRIAGE SKIPPED … changed while read`.
func (p *Pool) rehash(path string) (string, error) {
	raw, err := readFileSteady(path)
	if err != nil {
		return "", err
	}
	return HashBytes(raw), nil
}

// ResultByID is `result --id`: the one path by which a malformed report reaches a person.
// The body does NOT go through internal/oneline, because the body is the thing asked for.
func ResultByID(p *Pool, id string, stdout, stderr io.Writer) int {
	sc, ok := p.findSidecar(id)
	if !ok {
		fmt.Fprintf(stderr, "RESULT REFUSED: no task %s in %s; `nova-swarm status --pool %s` lists what is here\n",
			oneline.Field(id), oneline.Field(p.Dir), oneline.Field(p.Dir))
		return 1
	}
	raw, from, err := p.ReportBytes(sc)
	if err != nil {
		tail := ""
		if sc.Job != "" {
			tail = HarnessTail(sc.Job)
		}
		fmt.Fprintf(stderr, "RESULT REFUSED: %s published no report; the record says %s; what the harness said is beneath\n",
			oneline.Field(id), oneline.Field(MarkerNoResult))
		// LESSON 23: one escaped event line, and another tool's transcript RAW beneath it.
		if tail != "" {
			fmt.Fprintln(stderr, tail)
		}
		return 1
	}
	report := ParseReport(raw)
	fmt.Fprintf(stdout, "RESULT OK id=%s rev=%s class=%s bytes=%d from=%s\n",
		oneline.Field(id), oneline.Field(Short(report.Hash)), oneline.Field(report.Class), len(raw), oneline.Field(from))
	_, _ = stdout.Write(raw)
	return 0
}

func (p *Pool) findSidecar(id string) (Sidecar, bool) {
	for _, state := range []string{Running, Done, Failed, Pending} {
		if sc, err := p.ReadSidecar(state, id); err == nil {
			return sc, true
		}
	}
	return Sidecar{}, false
}
