package swarm

// THE RUN (issue #856): the harness does the work, the model answers the steps.
//
// A shell step runs in this process's own shell, inside the wall the caller wrapped it in,
// and costs nothing. A model step is one call whose input is the card's preamble, the
// step's own text and the inputs the step NAMED -- plus the previous step's captured
// output, which is how a failing test's lines reach the step that fixes them -- and whose
// answer is ONE artifact in ONE fenced block. The harness applies it: `git apply` for a
// diff, a write for a named file, RESULT.md for the result.
//
// NOTHING IS CARRIED BETWEEN CALLS. There is no transcript, so a card's bill is the sum of
// its steps' inputs instead of context x turns (#855: 1,435M cache-read tokens in one day).
//
// WHAT FAILS, AND HOW. A step whose answer is not the demanded artifact is retried ONCE
// with the parse error appended, and then the card fails with ONE line naming the step and
// the remedy. A card that wants to explore says `MODE: explore` and gets today's harness
// loop under a turn budget instead.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Defaults the run is bounded by. Every one of them is a ceiling a card cannot raise from
// inside: a budget the work can edit is not a budget.
const (
	// DefaultStepInputBytes is the per-step input cap. A step over it is truncated in its
	// NAMED INPUTS (largest first) and says so; a step whose own text is over it is refused.
	DefaultStepInputBytes = 120_000
	// DefaultMaxSteps is the most steps one card may hold.
	DefaultMaxSteps = 24
	// DefaultExploreTurns is the turn budget a `MODE: explore` card runs under.
	DefaultExploreTurns = 12
	// pipelineRetries is how many times ONE step may be asked again with the parse error.
	pipelineRetries = 1
	// stepOutputTail is how much of a step's output the next step may be given.
	stepOutputTail = 8 << 10
)

// ShellResult is one deterministic step's exit code and its captured output.
type ShellResult struct {
	RC     int
	Output string
}

// ShellFunc runs one deterministic step. The caller injects it so the same engine runs
// walled on a bench (the wall wrapping `sh -c`) and unwalled in a test.
type ShellFunc func(ctx context.Context, dir, script string) ShellResult

// PipelineConfig is everything one pipeline run needs, handed over complete.
type PipelineConfig struct {
	Card          []byte
	JobDir        string // the card's own directory; RESULT.md is published here
	RepoDir       string // the clone; "" means <JobDir>/repo
	Label         string
	Route         Route
	HTTP          *http.Client
	Shell         ShellFunc // "" means `sh -c` in this process
	Env           []string  // the environment the default shell hands a step
	MaxInputBytes int
	MaxSteps      int
	MaxCalls      int // 0 means one call per model step plus one retry
	Log           io.Writer
	Now           func() time.Time
}

// PipelineResult is what the run spent and whether it stands.
type PipelineResult struct {
	Steps     int
	Calls     int
	TokensIn  int64
	TokensOut int64
	Cached    int64
	USD       float64
	RC        int    // the last deterministic step's exit code
	Refusal   string // "" when the card stands; ONE line naming the step otherwise
	Rows      []UsageRow
}

// RunPipeline runs one card as a pipeline.
func RunPipeline(ctx context.Context, cfg PipelineConfig) PipelineResult {
	run := &pipelineRun{cfg: cfg, started: cfg.now()}
	card, err := ParsePipelineCard(cfg.Card)
	if err != nil {
		return run.refuseCard(Step{}, "card-shape", err.Error())
	}
	if max := cfg.maxSteps(); len(card.Steps) > max {
		return run.refuseCard(Step{}, "step-budget",
			fmt.Sprintf("the card holds %d steps and the budget is %d", len(card.Steps), max))
	}
	run.card = card
	run.maxCalls = cfg.MaxCalls
	if run.maxCalls <= 0 {
		run.maxCalls = card.ModelSteps() + pipelineRetries
	}
	prev := ""
	for _, step := range card.Steps {
		run.res.Steps++
		if step.Kind == StepShell {
			prev = run.runShell(ctx, step)
			continue
		}
		out, refusal := run.runModel(ctx, step, prev)
		if refusal != nil {
			run.res.Refusal = *refusal
			run.line(run.res.Refusal)
			run.finish()
			return run.res
		}
		prev = out
	}
	run.line(fmt.Sprintf("PIPELINE OK label=%s steps=%d calls=%d in=%d out=%d cached=%d usd=%s",
		field(cfg.Label), run.res.Steps, run.res.Calls, run.res.TokensIn, run.res.TokensOut, run.res.Cached,
		strconv.FormatFloat(run.res.USD, 'f', 4, 64)))
	run.finish()
	return run.res
}

type pipelineRun struct {
	cfg      PipelineConfig
	card     PipelineCard
	res      PipelineResult
	rows     []UsageRow
	maxCalls int
	started  time.Time
}

func (c PipelineConfig) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c PipelineConfig) maxSteps() int {
	if c.MaxSteps > 0 {
		return c.MaxSteps
	}
	return DefaultMaxSteps
}

func (c PipelineConfig) inputCap() int {
	if c.MaxInputBytes > 0 {
		return c.MaxInputBytes
	}
	return DefaultStepInputBytes
}

// repoDir is the clone the card works in.
func (c PipelineConfig) repoDir() string {
	if c.RepoDir != "" {
		return c.RepoDir
	}
	return filepath.Join(c.JobDir, "repo")
}

// stepDir is where a deterministic step runs: the clone once it exists, the job directory
// before it does. A card's STEP 1 clones into ./repo and `cd`s into it, and every step
// after it is written as if that cwd had stuck -- which in an agent loop it did. Each step
// here is its own shell, so the cwd is decided by the machinery instead of by a `cd` that
// died with the step that ran it.
func (r *pipelineRun) stepDir() string {
	repo := r.cfg.repoDir()
	if fi, err := os.Stat(repo); err == nil && fi.IsDir() {
		return repo
	}
	return r.cfg.JobDir
}

// runShell runs one deterministic step and returns the tail of its output for the next step.
func (r *pipelineRun) runShell(ctx context.Context, step Step) string {
	dir := r.stepDir()
	sh := r.cfg.Shell
	if sh == nil {
		sh = defaultShell(r.cfg.Env)
	}
	out := sh(ctx, dir, step.Text)
	r.res.RC = out.RC
	r.line(fmt.Sprintf("PIPELINE STEP n=%d label=%s kind=shell rc=%d out=%dB",
		step.Index, field(step.Label), out.RC, len(out.Output)))
	return tailOf(out.Output, stepOutputTail)
}

// runModel makes one call (and at most one retry) and applies the artifact it demanded.
func (r *pipelineRun) runModel(ctx context.Context, step Step, prev string) (string, *string) {
	system := artifactDemand(step, r.card.Contract)
	user, over := r.buildInput(step, prev)
	if over != "" {
		refusal := r.refusal(step, "input-budget", over)
		return "", &refusal
	}
	parseErr := ""
	for attempt := 1; attempt <= pipelineRetries+1; attempt++ {
		if r.res.Calls >= r.maxCalls {
			refusal := r.refusal(step, "call-budget",
				fmt.Sprintf("this card's %d model steps are its whole budget and call %d was asked for", r.card.ModelSteps(), r.res.Calls+1))
			return "", &refusal
		}
		ask := user
		if parseErr != "" {
			ask = user + "\n\n--- your previous answer was refused ---\n" + parseErr +
				"\nAnswer again with the artifact alone, in one fenced block, and nothing else.\n"
		}
		start := r.cfg.now()
		r.res.Calls++
		call, err := r.cfg.Route.Complete(ctx, r.cfg.HTTP, system, ask)
		end := r.cfg.now()
		if err != nil {
			// A route that did not answer is not a model that answered wrongly: it is
			// named as itself, once, and never retried into a second bill.
			refusal := r.refusal(step, "route", oneLine(err.Error()))
			return "", &refusal
		}
		r.record(step, attempt, call, len(system)+len(ask), start, end)
		applied, perr := r.applyArtifact(ctx, step, call.Text)
		if perr == "" {
			return applied, nil
		}
		parseErr = perr
		if attempt == pipelineRetries+1 {
			refusal := r.refusal(step, "artifact", perr)
			return "", &refusal
		}
	}
	refusal := r.refusal(step, "artifact", "the step was asked twice and answered with no artifact")
	return "", &refusal
}

// applyArtifact takes the ONE artifact out of the answer and does what it says. The second
// return is the parse error a retry carries, and the empty string when the step stands.
func (r *pipelineRun) applyArtifact(ctx context.Context, step Step, answer string) (string, string) {
	block, ok := fencedBlock(answer)
	if !ok {
		return "", fmt.Sprintf("the answer carries no fenced block; it must be exactly one ``` block holding the %s and nothing else", step.Artifact)
	}
	switch step.Artifact {
	case ArtifactResult:
		return r.writeResult(step, block)
	case ArtifactFile:
		return r.writeFile(step, block)
	default:
		return r.applyDiff(ctx, step, block)
	}
}

// applyDiff checks the diff's paths BEFORE git sees them and then applies it in the clone.
func (r *pipelineRun) applyDiff(ctx context.Context, step Step, diff string) (string, string) {
	if strings.TrimSpace(diff) == "" {
		return "", "the fenced block is empty; it must hold a unified diff"
	}
	if !strings.Contains(diff, "@@") && !strings.Contains(diff, "new file mode") {
		return "", "the fenced block holds no unified diff (no @@ hunk header)"
	}
	// CONTAINMENT IS THE MACHINERY'S, NOT THE PROMPT'S. A path leaving ./repo is refused
	// here, before `git apply` is started, whatever the card told the model.
	if bad, outside := DiffOutside([]byte(diff)); outside {
		return "", fmt.Sprintf("the diff touches %s, which is outside ./repo; every path in the diff is relative and inside the repository", oneLine(bad))
	}
	patch := filepath.Join(r.cfg.JobDir, ".pipeline", fmt.Sprintf("step-%d.patch", step.Index))
	if err := os.MkdirAll(filepath.Dir(patch), 0o755); err != nil {
		return "", fmt.Sprintf("the patch directory could not be made: %s", oneLine(err.Error()))
	}
	if !strings.HasSuffix(diff, "\n") {
		diff += "\n"
	}
	if err := os.WriteFile(patch, []byte(diff), 0o644); err != nil {
		return "", fmt.Sprintf("the patch could not be written: %s", oneLine(err.Error()))
	}
	sh := r.cfg.Shell
	if sh == nil {
		sh = defaultShell(r.cfg.Env)
	}
	dir := r.stepDir()
	var last ShellResult
	// -p1 is the spelling a `diff --git a/x b/x` answer carries; -p0 is the spelling a
	// plain `--- x` answer carries. Both are tried before the answer is called wrong.
	for _, strip := range []string{"-p1", "-p0"} {
		last = sh(ctx, dir, fmt.Sprintf("git apply %s --whitespace=nowarn -- %s", strip, shellQuote(patch)))
		if last.RC == 0 {
			paths := DiffPaths([]byte(diff))
			return fmt.Sprintf("applied %d path(s): %s", len(paths), strings.Join(paths, " ")), ""
		}
	}
	return "", fmt.Sprintf("the diff did not apply in %s: %s", filepath.Base(dir), oneLine(tailOf(last.Output, 400)))
}

// writeFile writes the body of the one file the step named.
func (r *pipelineRun) writeFile(step Step, body string) (string, string) {
	if step.OutPath == "" {
		return "", "the step demands a file body but names no path (write `OUT: file <path>`)"
	}
	if outsideRelative(step.OutPath) {
		return "", fmt.Sprintf("the step names %s, which is outside ./repo", oneLine(step.OutPath))
	}
	dst := filepath.Join(r.stepDir(), filepath.FromSlash(step.OutPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Sprintf("the directory for %s could not be made: %s", oneLine(step.OutPath), oneLine(err.Error()))
	}
	if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
		return "", fmt.Sprintf("%s could not be written: %s", oneLine(step.OutPath), oneLine(err.Error()))
	}
	return fmt.Sprintf("wrote %s (%dB)", step.OutPath, len(body)), ""
}

// writeResult publishes RESULT.md at the job root. Line 1 IS the contract line: a result
// whose line 1 is not the card's is a different card's result and the gather refuses it, so
// the harness refuses it here, while a retry is still cheap.
func (r *pipelineRun) writeResult(step Step, body string) (string, string) {
	line1 := body
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		line1 = body[:i]
	}
	if strings.TrimSpace(line1) != strings.TrimSpace(r.card.Contract) {
		return "", fmt.Sprintf("line 1 of the result is %q; it must be exactly the card's contract line", oneLine(head(line1, 120)))
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	dst := filepath.Join(r.cfg.JobDir, ResultFile)
	if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
		return "", fmt.Sprintf("%s could not be written: %s", ResultFile, oneLine(err.Error()))
	}
	return fmt.Sprintf("wrote %s (%dB)", ResultFile, len(body)), ""
}

// buildInput assembles ONE step's whole input under the byte cap: the contract line, the
// preamble, the step's own text, the previous step's captured output, then the inputs the
// step NAMED, in order, each truncated to what is left. The second return is the refusal
// when the step's own text will not fit, which is a card defect, not a model's.
func (r *pipelineRun) buildInput(step Step, prev string) (string, string) {
	limit := r.cfg.inputCap()
	var b strings.Builder
	b.WriteString(r.card.Contract)
	b.WriteString("\n")
	if r.card.Preamble != "" {
		b.WriteString(r.card.Preamble)
		b.WriteString("\n")
	}
	b.WriteString("\n--- your step ---\nSTEP ")
	b.WriteString(step.Label)
	b.WriteString(". ")
	b.WriteString(step.Text)
	b.WriteString("\n")
	if b.Len() > limit {
		return "", fmt.Sprintf("the step's own text and the card's preamble are %dB, over the %dB per-step cap", b.Len(), limit)
	}
	left := limit - b.Len()
	if prev = tailOf(prev, min(left/3, stepOutputTail)); strings.TrimSpace(prev) != "" {
		b.WriteString("\n--- the previous step's output ---\n")
		b.WriteString(prev)
		b.WriteString("\n")
		left = limit - b.Len()
	}
	for _, in := range step.Inputs {
		if left <= 0 {
			break
		}
		body, name, ok := r.readNamedInput(in)
		if !ok {
			continue
		}
		kept := body
		note := ""
		if len(kept) > left {
			kept = kept[:left]
			note = fmt.Sprintf(" (truncated to %dB by the per-step cap)", left)
		}
		fmt.Fprintf(&b, "\n--- %s%s ---\n%s\n", name, note, kept)
		left = limit - b.Len()
	}
	return b.String(), ""
}

// readNamedInput reads one `path` or `path:from-to` the step named, out of the clone or the
// job directory and nowhere else: a step reads what it names, and names only what is here.
func (r *pipelineRun) readNamedInput(token string) (body, name string, ok bool) {
	path, from, to := splitRange(token)
	if outsideRelative(path) {
		return "", "", false
	}
	for _, base := range []string{r.stepDir(), r.cfg.JobDir, r.cfg.repoDir()} {
		full := filepath.Join(base, filepath.FromSlash(path))
		raw, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		if from <= 0 {
			return string(raw), path, true
		}
		lines := strings.Split(string(raw), "\n")
		if from > len(lines) {
			return "", "", false
		}
		if to <= 0 || to > len(lines) {
			to = len(lines)
		}
		return strings.Join(lines[from-1:to], "\n"), fmt.Sprintf("%s:%d-%d", path, from, to), true
	}
	return "", "", false
}

// splitRange reads the `path:from-to` spelling a step names a line range with.
func splitRange(token string) (path string, from, to int) {
	path = token
	idx := strings.LastIndexByte(token, ':')
	if idx <= 0 {
		return path, 0, 0
	}
	rest := token[idx+1:]
	a, b, hasDash := strings.Cut(rest, "-")
	n, err := strconv.Atoi(a)
	if err != nil || n <= 0 {
		return path, 0, 0
	}
	from = n
	if hasDash {
		if m, err := strconv.Atoi(b); err == nil {
			to = m
		}
	} else {
		to = n
	}
	return token[:idx], from, to
}

// artifactDemand is the system message: what this call is, what it must answer with, and
// that it has no tools and no memory. It is the same for every step of its kind, which is
// what makes the calls independent.
func artifactDemand(step Step, contract string) string {
	var b strings.Builder
	b.WriteString("You are ONE STEP of a pipeline. You have no tools, no shell and no memory of any other step. ")
	b.WriteString("Everything you are given is below; do not ask for more and do not describe what you would do.\n")
	switch step.Artifact {
	case ArtifactResult:
		b.WriteString("Answer with exactly ONE fenced block holding the whole text of RESULT.md and nothing outside it.\n")
		b.WriteString("Line 1 of that text is exactly:\n")
		b.WriteString(contract)
		b.WriteString("\n")
	case ArtifactFile:
		fmt.Fprintf(&b, "Answer with exactly ONE fenced block holding the whole body of %s and nothing outside it.\n", step.OutPath)
	default:
		b.WriteString("Answer with exactly ONE fenced block holding a unified diff that applies with `git apply` at the root of the repository, and nothing outside it.\n")
		b.WriteString("Every path in the diff is relative and inside the repository; a path with `..` or a leading `/` is refused by the harness.\n")
	}
	return b.String()
}

// fencedBlock is the ONE artifact in an answer: the contents of the first ``` block. An
// answer with no fence at all is not an artifact, and one with prose around the fence is
// still read -- the fence is the contract, not the absence of chatter.
func fencedBlock(answer string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(answer, "\r\n", "\n"), "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "```" {
			return strings.Join(lines[start+1:i], "\n") + "\n", true
		}
	}
	// A fence the answer never closed is still the artifact: the model stopped, and what
	// it wrote is what there is. The step's own check (a hunk header, a line 1) decides.
	if start+1 < len(lines) {
		return strings.Join(lines[start+1:], "\n"), true
	}
	return "", false
}

// record folds one call into the run's totals and writes its own usage row.
func (r *pipelineRun) record(step Step, attempt int, call CallResult, inBytes int, start, end time.Time) {
	r.res.TokensIn += call.PromptIn
	r.res.TokensOut += call.Completion
	r.res.Cached += call.Cached
	r.res.USD += call.USD
	row := UsageRow{
		"job":      fmt.Sprintf("%s#step%d", r.cfg.Label, step.Index),
		"attempt":  strconv.Itoa(attempt),
		"started":  start.UTC().Format(time.RFC3339),
		"ended":    end.UTC().Format(time.RFC3339),
		"rc":       "0",
		"provider": r.cfg.Route.Provider,
		"model":    r.cfg.Route.Model,
	}
	for _, c := range TokenColumns {
		row[c] = Dash
	}
	if call.Reported["tokens_in"] {
		row["tokens_in"] = strconv.FormatInt(call.PromptIn, 10)
	}
	if call.Reported["tokens_out"] {
		row["tokens_out"] = strconv.FormatInt(call.Completion, 10)
	}
	if call.Reported["cache_read"] {
		row["cache_read"] = strconv.FormatInt(call.Cached, 10)
	}
	row["usd"] = Dash
	if call.Reported["usd"] {
		row["usd"] = strconv.FormatFloat(call.USD, 'f', 4, 64)
	}
	r.rows = append(r.rows, row)
	r.line(fmt.Sprintf("PIPELINE STEP n=%d label=%s kind=model artifact=%s attempt=%d in=%dB in_tokens=%s out_tokens=%s cached=%s",
		step.Index, field(step.Label), step.Artifact, attempt, inBytes,
		row["tokens_in"], row["tokens_out"], row["cache_read"]))
}

// finish writes the card's usage.tsv: the CARD's own total row first -- which is the row
// every reader of a usage.tsv has always read, line 2 (internal/swarm/usage.go) -- and then
// one row per model call beneath it, so the per-step bill is on the record without moving
// the row the batch folds.
func (r *pipelineRun) finish() {
	if r.cfg.JobDir == "" {
		return
	}
	total := UsageRow{
		"job":      r.cfg.Label,
		"attempt":  "1",
		"started":  r.started.UTC().Format(time.RFC3339),
		"ended":    r.cfg.now().UTC().Format(time.RFC3339),
		"rc":       strconv.Itoa(r.res.RC),
		"provider": r.cfg.Route.Provider,
		"model":    r.cfg.Route.Model,
	}
	for _, c := range TokenColumns {
		total[c] = Dash
	}
	total["tokens_in"] = strconv.FormatInt(r.res.TokensIn, 10)
	total["tokens_out"] = strconv.FormatInt(r.res.TokensOut, 10)
	total["cache_read"] = strconv.FormatInt(r.res.Cached, 10)
	total["usd"] = strconv.FormatFloat(r.res.USD, 'f', 4, 64)
	r.res.Rows = append([]UsageRow{total}, r.rows...)
	_ = WriteCardUsageRows(filepath.Join(r.cfg.JobDir, "usage.tsv"), r.res.Rows)
}

// refuseCard is the refusal a card's own shape earns, before any call is made.
func (r *pipelineRun) refuseCard(step Step, reason, detail string) PipelineResult {
	r.res.Refusal = r.refusal(step, reason, detail)
	r.line(r.res.Refusal)
	r.finish()
	return r.res
}

// refusal is THE ONE LINE a failed pipeline prints: the label, the step, the reason, what
// happened, and the remedy. One line, always, because a coordinator reads the line and not
// the job (rule: bounded output).
func (r *pipelineRun) refusal(step Step, reason, detail string) string {
	where := ""
	if step.Index > 0 {
		where = fmt.Sprintf(" step=%d step_label=%s", step.Index, field(step.Label))
	}
	return fmt.Sprintf("PIPELINE REFUSED label=%s%s reason=%s: %s; %s",
		field(r.cfg.Label), where, reason, oneLine(detail), pipelineRemedy)
}

// pipelineRemedy is the one remedy every pipeline refusal carries.
const pipelineRemedy = "a pipeline step answers with one artifact in one fenced block and calls nothing; " +
	"put `MODE: explore` on the card for today's harness loop under --max-turns, or split the step so each one names its own inputs"

func (r *pipelineRun) line(s string) {
	if r.cfg.Log == nil {
		return
	}
	fmt.Fprintln(r.cfg.Log, s)
}

// defaultShell runs one step with `sh -c`, capturing stdout and stderr together.
func defaultShell(env []string) ShellFunc {
	return func(ctx context.Context, dir, script string) ShellResult {
		cmd := exec.CommandContext(ctx, "sh", "-c", script)
		cmd.Dir = dir
		if len(env) > 0 {
			cmd.Env = env
		}
		out, err := cmd.CombinedOutput()
		rc := 0
		if err != nil {
			rc = 1
			if ee, ok := err.(*exec.ExitError); ok {
				rc = ee.ExitCode()
			}
		}
		return ShellResult{RC: rc, Output: string(out)}
	}
}

// WriteCardUsageRows writes one header line and every row beneath it, atomically, on the
// same tab- and newline-scrubbing law as one row (WriteCardUsage): row 1 is the card's, and
// the rows under it are its steps'.
func WriteCardUsageRows(path string, rows []UsageRow) error {
	var b strings.Builder
	b.WriteString(strings.Join(CardUsageColumns, "\t"))
	b.WriteString("\n")
	scrub := strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")
	for _, row := range rows {
		var values []string
		for _, c := range CardUsageColumns {
			v := strings.TrimSpace(row[c])
			if v == "" {
				v = Dash
			}
			values = append(values, scrub.Replace(v))
		}
		b.WriteString(strings.Join(values, "\t"))
		b.WriteString("\n")
	}
	return writeAtomic(path, []byte(b.String()), 0o644)
}

// tailOf keeps the last n bytes of a capture, on a line boundary.
func tailOf(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i+1 < len(s) {
		s = s[i+1:]
	}
	return s
}

// field is ONE printed token, under the one law every tool in this repo prints fields by
// (internal/oneline): a space, an `=` or a control character in a value would break the
// line a reader parses by field, so it is escaped rather than quoted.
func field(s string) string {
	if s == "" {
		return Dash
	}
	return oneline.Field(s)
}

// shellQuote is one argument, safe inside `sh -c`.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
