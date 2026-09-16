package pulse

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// HarvestInput is everything the harvest verb needs, held apart from command-line parsing
// so a test can drive it with fake directories and a fake nova-swarm/nova-pulse on PATH.
type HarvestInput struct {
	ID           string
	Root         string
	Sources      string
	Templates    string
	MaxBodyBytes int
	Max          int
	Publish      bool   // run the publish command per BRANCH card; without it the command is printed
	Deadline     string // the deadline the relaunch carries (rule 15's launch needs one)
	Slots        string // the slot range the relaunch carries
	Stdout       io.Writer
	Stderr       io.Writer
	Now          func() time.Time
}

// harvestCard is one card this harvest folds, whoever launched it: a label, the model it ran
// on, the bench it ran on, the card's path when a launch recorded one, and the stamp it was
// launched at (zero when unknown).
type harvestCard struct {
	Label    string
	Model    string
	Bench    string
	Card     string
	Launched time.Time
}

// orphanAfter is how long after a launch a card with no job directory anywhere is an orphan
// rather than a card still being copied to its bench. It is the 23:36Z class: the batch was
// admitted, no job dir was ever made, and the coordinator read a silent root as idle.
const orphanAfter = 3 * time.Minute

func field(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return oneline.Field(s)
}

func refusal(w io.Writer, token string, err error) int {
	fmt.Fprintf(w, "%s REFUSED: %s\n", token, oneline.Err(err))
	return 2
}

// Harvest folds one pulse's cards: it pushes and opens a draft PR for every card whose
// RESULT.md line 1 equals its contract line, sends every abstain (or missing RESULT) to
// retry.tsv with the harness log's last refusal line, cuts a read card per PR, and then
// pulses again -- queue first. It never merges.
func Harvest(in HarvestInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.MaxBodyBytes <= 0 {
		in.MaxBodyBytes = 4096
	}
	started := in.Now()

	// #628: --id is optional. A harvest with one folds that pulse's rows; a harvest without
	// one folds every card in the root, whoever launched it.
	if strings.TrimSpace(in.Root) == "" {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("missing --root; refusing to guess (supply the root directory)"))
	}

	// #628: A ROOT IS FOLDED BY WHAT IS IN IT, NOT BY WHO CUT IT. launch.tsv first (this
	// tool's own record), then a cards.tsv a cut wrote, and last the job directories
	// themselves, so a root filled by `nova-swarm batch` by hand folds like any other.
	cards, source, err := harvestCards(in.Root, in.ID)
	if err != nil {
		return refusal(in.Stderr, "HARVEST", err)
	}
	if len(cards) == 0 {
		fmt.Fprintf(in.Stderr, "HARVEST REFUSED reason=no-card root=%s (no launch.tsv, no cards.tsv and no job directory under it)\n", oneline.Field(in.Root))
		return 2
	}

	var done, pushed, prs, abstain, mismatch, refused, retried, orphans, reads, noBranch int
	lines := make([]string, 0) // HARVEST PR / RETRY / ORPHAN / REFUSED per-card lines
	usdTotal := 0.0

	for _, hc := range cards {
		c := CardRow{Label: hc.Label, Slot: "-", Model: hc.Model, Card: hc.Card}
		jd := findJobDir(in.Root, hc.Label)
		if jd == "" {
			// A launched card with no job directory anywhere is an orphan, not an abstain:
			// nothing ran, so there is no harness log to quote and no reason to triage.
			if hc.Launched.IsZero() || in.Now().Sub(hc.Launched) >= orphanAfter {
				orphans++
				lines = append(lines, fmt.Sprintf("HARVEST ORPHAN label=%s bench=%s: no job directory under the root %s after launch (the batch was admitted and no card started)",
					field(hc.Label), field(hc.Bench), field(in.Root)))
				writeSeen(in.Root, c, "orphan")
				continue
			}
			jd = jobDir(in.Root, "0", hc.Label)
		}
		usdTotal += readCardUSD(filepath.Join(jd, "usage.tsv"))
		contract := cardContract(hc.Card)
		state, branch, repo, resultLines := classify(jd, c, contract)

		switch state {
		case "mismatch":
			mismatch++
			writeSeen(in.Root, c, "mismatch")
			lines = append(lines, fmt.Sprintf("HARVEST MISMATCH label=%s reason=line1: the RESULT's line 1 is not the card's contract line", field(hc.Label)))
		case "blocked":
			mismatch++
			writeSeen(in.Root, c, "blocked")
			lines = append(lines, fmt.Sprintf("HARVEST BLOCKED label=%s: the card says BLOCKED; nothing is pushed", field(hc.Label)))
		case "read":
			reads++
			verdict := readVerdict(resultLines)
			writeSeen(in.Root, c, "read")
			lines = append(lines, fmt.Sprintf("HARVEST READ label=%s pr=%d verdict=%s",
				field(hc.Label), verdict.PR, field(verdict.Say)))
		case "no-branch":
			noBranch++
			writeSeen(in.Root, c, "no-branch")
			lines = append(lines, fmt.Sprintf("HARVEST NO-BRANCH label=%s: a RESULT with a verdict and no BRANCH line; nothing to push (ask the card for its branch)", field(hc.Label)))
		case "branch-main":
			mismatch++
			writeSeen(in.Root, c, "branch-main")
			lines = append(lines, fmt.Sprintf("HARVEST MISMATCH label=%s reason=branch-main: a card may not push to main", field(hc.Label)))
		case "abstain":
			abstain++
			retried++
			// #12: the reason token the packet already scored, never the runner's NATIVE
			// line. `deadline`, `no-result`, `card-abstain` -- one token a coordinator can
			// count -- and the harness's own last refusal after it, when there is one.
			reason := cardAbstainReason(jd, resultLines)
			tail := lastRefusal(filepath.Join(jd, "harness.log"))
			writeSeen(in.Root, c, "retry")
			lines = append(lines, fmt.Sprintf("HARVEST RETRY label=%s reason=%s: %s",
				field(hc.Label), reason, oneline.Escape(tail)))
			appendRetry(in.Root, c, reason+" "+tail)
		case "refused":
			refused++
			writeSeen(in.Root, c, "refused")
			fmt.Fprintf(in.Stderr, "HARVEST REFUSED label=%s: fix card with no red: line and no test file in its diff (add the red test output before the fix)\n", field(hc.Label))
		case "done":
			done++
			cmdline := publishCommand(jd, branch, hc.Label)
			if !in.Publish {
				// The event line carries fields; the command a person copies is its own
				// line, whole, because a command line is not one token (SPEC.md's field law)
				// and a half-escaped command is not runnable.
				lines = append(lines, fmt.Sprintf("HARVEST BRANCH label=%s branch=%s job=%s",
					field(hc.Label), field(branch), oneline.Field(jd)))
				lines = append(lines, "HARVEST PUBLISH "+oneline.Escape(cmdline))
				writeSeen(in.Root, c, "branch")
				continue
			}
			url := pushURL(repo)
			if err := push(in, jd, url, branch); err != nil {
				fmt.Fprintf(in.Stderr, "HARVEST NOTE push failed label=%s: %s\n", field(hc.Label), oneline.Err(err))
				continue
			}
			pushed++
			pr, err := openPR(in, jd, url, hc.Label, branch, resultLines)
			if err != nil {
				fmt.Fprintf(in.Stderr, "HARVEST NOTE pr failed label=%s: %s\n", field(hc.Label), oneline.Err(err))
				continue
			}
			prs++
			lines = append(lines, fmt.Sprintf("HARVEST PR repo=%s pr=%d label=%s branch=%s",
				field(repo), pr, field(hc.Label), field(branch)))
			appendNext(in.Root, repo, pr, hc.Label)
		}
	}

	// #12: the spend is the cards' own usage.tsv rows, summed, and the packet's usd only
	// when no card wrote one. A harvest that lost the batch's spend printed usd=- while the
	// BATCH line said usd=0.0498.
	usd := formatUSD(usdTotal)
	if usdTotal == 0 {
		usd = readUSD(filepath.Join(in.Root, "pulses", in.ID+".packet"))
	}

	grouped := bound(in.Stdout, in.Max)
	for _, l := range lines {
		grouped.Line(l)
	}
	grouped.More()

	code := 0
	result := "OK"
	if mismatch > 0 || abstain > 0 || refused > 0 || orphans > 0 || noBranch > 0 {
		code = 1
	}
	fmt.Fprintf(in.Stdout, "HARVEST %s id=%s source=%s done=%d pushed=%d prs=%d read=%d abstain=%d mismatch=%d no-branch=%d refused=%d orphan=%d retry=%d usd=%s took=%s\n",
		result, field(in.ID), source, done, pushed, prs, reads, abstain, mismatch, noBranch, refused, orphans, retried, usd,
		in.Now().Sub(started).Round(time.Millisecond))

	// Rule 15: harvest pulses again, queue first. The PULSE line (or PULSE POOL EMPTY) is
	// harvest's own last line.
	if rc := relaunch(in); rc != 0 && code == 0 {
		code = rc
	}
	return code
}

func bound(w io.Writer, max int) *boundedList {
	return &boundedList{w: w, max: max}
}

// boundedList mirrors bounded.List's cap for the HARVEST event lines without a package cycle.
type boundedList struct {
	w     io.Writer
	max   int
	shown int
	total int
}

func (l *boundedList) Line(line string) {
	l.total++
	if l.max > 0 && l.shown >= l.max {
		return
	}
	fmt.Fprintf(l.w, "%s\n", oneline.Escape(line))
	l.shown++
}

// More prints the MORE line when lines were elided.
func (l *boundedList) More() {
	if l.max > 0 && l.total > l.shown {
		fmt.Fprintf(l.w, "HARVEST MORE kind=event shown=%d total=%d use --max 0 to show all\n", l.shown, l.total)
	}
}

// readCards reads cards.tsv (label<TAB>slot<TAB>model<TAB>card-path) into rows.
func readCards(path string) ([]CardRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s (harvest folds the cards cut by launch; run: nova-pulse cut)", path)
	}
	var out []CardRow
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 4 {
			return nil, fmt.Errorf("%s wants label<TAB>slot<TAB>model<TAB>card-path, got %d fields", path, len(p))
		}
		out = append(out, CardRow{Label: p[0], Slot: p[1], Model: p[2], Card: p[3]})
	}
	return out, nil
}

// jobDir is a card's job directory under the root: <root>/<slot>/jobs/<label>.
func jobDir(root, slot, label string) string {
	if slot == "" || slot == "-" {
		slot = "0"
	}
	return filepath.Join(root, slot, "jobs", label)
}

// cardContract is line 1 of a card's text file, the contract line its RESULT must equal.
func cardContract(cardPath string) string {
	if cardPath == "" {
		return ""
	}
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(strings.Split(string(raw), "\n")))
}

// classify reads a card's RESULT.md and returns its disposition and the push details.
// done -> pushed unless the branch is main or a pro card lacks a red: line (refused).
func classify(jobDir string, c CardRow, contract string) (state, branch, repo string, resultLines []string) {
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		return "abstain", "", "", nil
	}
	norm := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	line1 := strings.TrimSpace(firstNonEmpty(lines))
	// The contract is checked WHEN THE CARD IS ON DISK. A root folded without its cards
	// (#628: a root nova-swarm batch filled by hand) has no contract line to compare, and
	// calling every card of it a mismatch is how the fold used to lose four real outcomes;
	// such a card is disposed by its own two lines instead.
	if contract != "" && line1 != contract {
		return "mismatch", "", "", lines
	}
	line2 := ""
	if len(lines) > 1 {
		line2 = strings.TrimSpace(lines[1])
	}
	if strings.HasPrefix(line2, "ABSTAIN") {
		return "abstain", "", "", lines
	}
	if strings.HasPrefix(line2, "BLOCKED") {
		return "blocked", "", "", lines
	}
	// A READ CARD IS NOT A BRANCH CARD. Its verdict is `PR<n>: APPROVE|HOLD`, it has no
	// branch and never had one, and calling it a mismatch (which is what "no BRANCH line"
	// used to fall through to) told a coordinator its line 1 was wrong when the card had
	// done exactly what it was cut to do.
	if v := readVerdict(lines); v != nil {
		return "read", "", "", lines
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "BRANCH ") {
			branch = strings.TrimSpace(strings.TrimPrefix(t, "BRANCH "))
		}
		if strings.HasPrefix(t, "REPO ") {
			repo = strings.TrimSpace(strings.TrimPrefix(t, "REPO "))
			repo = strings.TrimPrefix(repo, "github.com/")
		}
	}
	// Three different things used to be one "mismatch": a wrong line 1, a card that named
	// no branch, and a card that named main. Each now says which it is, because the remedy
	// differs: fix the card, ask the worker for the branch, refuse the push.
	if branch == "" {
		return "no-branch", "", repo, lines
	}
	if branch == "main" || branch == "master" {
		return "branch-main", branch, repo, lines
	}
	if c.Model == "pro" && !hasRedLine(lines) {
		return "refused", branch, repo, lines
	}
	return "done", branch, repo, lines
}

func hasRedLine(lines []string) bool {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "red:") || strings.HasPrefix(t, "red ") {
			return true
		}
	}
	return false
}

func firstNonEmpty(lines []string) string {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// lastRefusal is the last line of a harness log carrying a refusal word, else its last line.
func lastRefusal(logPath string) string {
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return "-"
	}
	lines := []string{}
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines = append(lines, strings.TrimSpace(l))
	}
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.ToLower(lines[i])
		if strings.Contains(l, "permission") || strings.Contains(l, "denied") || strings.Contains(l, "refused") {
			return oneline.Cap(lines[i], 200)
		}
	}
	if len(lines) == 0 {
		return "-"
	}
	return oneline.Cap(lines[len(lines)-1], 200)
}

// pushURL is the https clone url for a repo, used for the explicit refspec push.
func pushURL(repo string) string {
	if repo == "" {
		return ""
	}
	return "https://github.com/" + repo + ".git"
}

// push runs git push <url> <branch>:<branch> from the job's clone; never a bare git push.
func push(in HarvestInput, dir, url, branch string) error {
	if url == "" {
		return fmt.Errorf("no REPO line in RESULT.md")
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "push", url, branch+":"+branch)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", oneline.Err(err), oneline.Cap(string(out), 200))
	}
	return nil
}

// openPR opens a draft PR (or updates an existing one) whose body is the RESULT.md lines,
// capped at MaxBodyBytes. It returns the PR number.
func openPR(in HarvestInput, dir, url, label, branch string, resultLines []string) (int, error) {
	body := strings.Join(resultLines, "\n")
	if len(body) > in.MaxBodyBytes {
		body = body[:in.MaxBodyBytes]
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()

	update := exec.CommandContext(ctx, "gh", "pr", "view", branch, "--json", "number")
	update.Dir = dir
	if _, err := update.CombinedOutput(); err == nil {
		// An existing PR is updated in place; the number is re-read from the view output.
		edit := exec.CommandContext(ctx, "gh", "pr", "edit", branch, "--body-file", "-")
		edit.Dir = dir
		edit.Stdin = strings.NewReader(body)
		if _, err := edit.CombinedOutput(); err != nil {
			return 0, err
		}
		view2 := exec.CommandContext(ctx, "gh", "pr", "view", branch, "--json", "number")
		view2.Dir = dir
		out, err := view2.CombinedOutput()
		if err != nil {
			return 0, err
		}
		return parsePRNumber(string(out)), nil
	}

	create := exec.CommandContext(ctx, "gh", "pr", "create", "--draft", "--title", label, "--body-file", "-")
	create.Dir = dir
	create.Stdin = strings.NewReader(body)
	out, err := create.CombinedOutput()
	if err != nil {
		return 0, err
	}
	return parsePRNumber(string(out)), nil
}

func parsePRNumber(s string) int {
	// Accept a trailing /pull/<n> or a bare trailing number.
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if i := strings.LastIndex(s, "/pull/"); i >= 0 {
		n, err := strconv.Atoi(strings.TrimSpace(s[i+len("/pull/"):]))
		if err == nil {
			return n
		}
	}
	fields := strings.Fields(s)
	if len(fields) > 0 {
		if n, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			return n
		}
	}
	return 0
}

// readUSD parses the usd token from a saved swarm packet's BATCH line, else "-".
func readUSD(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "-"
	}
	first := strings.SplitN(string(raw), "\n", 2)[0]
	for _, tok := range strings.Fields(first) {
		if v, ok := strings.CutPrefix(tok, "usd="); ok {
			return v
		}
	}
	return "-"
}

// writeSeen appends or updates a seen.tsv row: source<TAB>id<TAB>state.
func writeSeen(root string, c CardRow, state string) {
	path := filepath.Join(root, "seen.tsv")
	line := fmt.Sprintf("%s\t%s\t%s\n", "card", c.Label, state)
	existing, _ := os.ReadFile(path)
	var b strings.Builder
	b.Write(existing)
	b.WriteString(line)
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

// appendRetry appends label<TAB>card<TAB>refusal to retry.tsv.
func appendRetry(root string, c CardRow, refusal string) {
	f, err := os.OpenFile(filepath.Join(root, "retry.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%s\n", c.Label, c.Card, refusal)
}

// appendNext appends a read card to next.tsv: pr<TAB>repo#n<TAB>read<TAB>title<TAB>read.
func appendNext(root, repo string, pr int, label string) {
	f, err := os.OpenFile(filepath.Join(root, "next.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "pr\t%s#%d\tread\t%s\tread\n", repo, pr, label)
}

const childTimeout = 120 * time.Second


// harvestCards is where a harvest's cards come from, most authoritative first: this tool's
// own launch.tsv, then a cards.tsv a cut wrote, then the job directories under the root
// (#628: a root filled by `nova-swarm batch` by hand folds like any other). It returns the
// cards and the name of the source it read, which the HARVEST line carries.
func harvestCards(root, id string) ([]harvestCard, string, error) {
	if rows, err := readLaunchTSV(filepath.Join(root, "launch.tsv"), id); err == nil && len(rows) > 0 {
		var out []harvestCard
		for _, r := range rows {
			stamp, _ := time.Parse("2006-01-02T15:04:05Z", r.Stamp)
			out = append(out, harvestCard{Label: r.Label, Model: r.Model, Bench: r.Bench, Card: cardPathFor(root, id, r.Label), Launched: stamp})
		}
		return out, "launch.tsv", nil
	}
	if rows, err := readCards(filepath.Join(root, "cards.tsv")); err == nil && len(rows) > 0 {
		var out []harvestCard
		for _, r := range rows {
			out = append(out, harvestCard{Label: r.Label, Model: r.Model, Bench: "-", Card: r.Card})
		}
		return out, "cards.tsv", nil
	}
	labels, err := jobLabels(root)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read %s (a harvest folds launch.tsv, cards.tsv or the job directories under the root): %s", root, oneline.Err(err))
	}
	var out []harvestCard
	for _, l := range labels {
		out = append(out, harvestCard{Label: l, Model: "-", Bench: "-"})
	}
	return out, "jobs", nil
}

// cardPathFor finds the card file a launch row named, looking where launch and cut put them.
func cardPathFor(root, id, label string) string {
	for _, p := range []string{
		filepath.Join(root, "cards", id, label+".md"),
		filepath.Join(root, "cards", label+".md"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// The card's own copy inside the job is the contract line's last home.
	if jd := findJobDir(root, label); jd != "" {
		if p := filepath.Join(jd, "card.md"); fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// jobLabels lists every label with a job directory under the root, in slot order.
func jobLabels(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		jobs, err := os.ReadDir(filepath.Join(root, e.Name(), "jobs"))
		if err != nil {
			continue
		}
		for _, j := range jobs {
			if !j.IsDir() || seen[j.Name()] {
				continue
			}
			seen[j.Name()] = true
			out = append(out, j.Name())
		}
	}
	return out, nil
}

// findJobDir is a label's job directory under any slot of the root, remote slots included
// (<bench>-<n>), or "" when no slot holds one.
func findJobDir(root, label string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name(), "jobs", label)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// abstainReason is the ONE token an abstain is counted by (#12 of the probe's issues): the
// card's own ABSTAIN reason when it wrote one, `no-result` when it wrote no RESULT.md, and
// `deadline` when the job holds the batch's own deadline mark.
func cardAbstainReason(jobDir string, resultLines []string) string {
	if r := abstainReason(resultLines); r != "" {
		return oneline.Field(r)
	}
	if !fileExists(filepath.Join(jobDir, "RESULT.md")) {
		if fileExists(filepath.Join(jobDir, "harness.log")) {
			return "no-result"
		}
		return "no-job"
	}
	return "no-result"
}

// publishCommand is the `nova-swarm publish` a person (or --publish) runs for a done card.
// Pushing and opening a PR stays with publish: harvest names the command and never grows a
// second implementation of it.
func publishCommand(jobDir, branch, label string) string {
	return fmt.Sprintf("nova-swarm publish --job %s --branch %s --base main --title %s --body-file %s",
		filepath.Join(jobDir, "repo"), branch, label, filepath.Join(jobDir, "RESULT.md"))
}

// readCardUSD reads the usd column of one card's usage.tsv (thirteen columns, one header
// line and one row), or 0 when there is none.
func readCardUSD(path string) float64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		return 0
	}
	head := strings.Split(lines[0], "\t")
	row := strings.Split(lines[1], "\t")
	for i, h := range head {
		if strings.TrimSpace(h) != "usd" || i >= len(row) {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(row[i]), 64)
		if err != nil {
			return 0
		}
		return v
	}
	return 0
}

// formatUSD prints a spend the way the swarm's own BATCH line does: four decimals, or "-"
// when nothing was spent.
func formatUSD(v float64) string {
	if v == 0 {
		return "-"
	}
	return strconv.FormatFloat(v, 'f', 4, 64)
}
