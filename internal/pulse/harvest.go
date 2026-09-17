package pulse

import (
	"context"
	"encoding/json"
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
	Stdout       io.Writer
	Stderr       io.Writer
	Now          func() time.Time
}

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

	if strings.TrimSpace(in.ID) == "" {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("missing --id; refusing to guess (supply the pulse id)"))
	}
	if strings.TrimSpace(in.Root) == "" {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("missing --root; refusing to guess (supply the root directory)"))
	}

	cards, err := readCards(filepath.Join(in.Root, "cards.tsv"))
	if err != nil {
		return refusal(in.Stderr, "HARVEST", err)
	}

	var done, pushed, prs, abstain, mismatch, refused, retried int
	lines := make([]string, 0) // HARVEST PR / RETRY / REFUSED per-card lines

	for _, c := range cards {
		jobDir := jobDir(in.Root, c.Slot, c.Label)
		contract := cardContract(c.Card)
		state, branch, repo, resultLines := classify(jobDir, c, contract)

		// The pool layout beside the slot layout (SPEC-PULSE rule 12): launch
		// admits cards into <root>/pool and run finalizes each task's report
		// to pool/reports/<id>/RESULT.md with the task file in
		// pool/{done,failed}/<id>.task. A pool card is never under the slot
		// job directory, so without this it is never harvested.
		if state != "done" {
			if ps, pb, pr, pl, pd := classifyPool(in.Root, c, contract); ps != "" {
				state, branch, repo, resultLines, jobDir = ps, pb, pr, pl, pd
			}
		}

		switch state {
		case "mismatch":
			mismatch++
			writeSeen(in.Root, c, "mismatch")
		case "abstain":
			abstain++
			retried++
			refusal := lastRefusal(filepath.Join(jobDir, "harness.log"))
			writeSeen(in.Root, c, "retry")
			lines = append(lines, fmt.Sprintf("HARVEST RETRY label=%s card=%s: %s",
				field(c.Label), field(c.Card), oneline.Escape(refusal)))
			appendRetry(in.Root, c, refusal)
		case "refused":
			refused++
			writeSeen(in.Root, c, "refused")
			fmt.Fprintf(in.Stderr, "HARVEST REFUSED label=%s: fix card with no red: line and no test file in its diff (add the red test output before the fix)\n", field(c.Label))
		case "done":
			done++
			url := pushURL(repo)
			if err := push(in, jobDir, url, branch); err != nil {
				fmt.Fprintf(in.Stderr, "HARVEST NOTE push failed label=%s: %s\n", field(c.Label), oneline.Err(err))
				continue
			}
			pushed++
			pr, err := openPR(in, jobDir, url, c.Label, branch, resultLines)
			if err != nil {
				fmt.Fprintf(in.Stderr, "HARVEST NOTE pr failed label=%s: %s\n", field(c.Label), oneline.Err(err))
				continue
			}
			prs++
			lines = append(lines, fmt.Sprintf("HARVEST PR repo=%s pr=%d label=%s branch=%s",
				field(repo), pr, field(c.Label), field(branch)))
			appendNext(in.Root, repo, pr, c.Label)
		}
	}

	usd := readUSD(filepath.Join(in.Root, "pulses", in.ID+".packet"))

	grouped := bound(in.Stdout, in.Max)
	for _, l := range lines {
		grouped.Line(l)
	}
	grouped.More()

	code := 0
	result := "OK"
	if mismatch > 0 || abstain > 0 || refused > 0 {
		code = 1
	}
	fmt.Fprintf(in.Stdout, "HARVEST %s id=%s done=%d pushed=%d prs=%d abstain=%d mismatch=%d refused=%d retry=%d usd=%s took=%s\n",
		result, field(in.ID), done, pushed, prs, abstain, mismatch, refused, retried, usd,
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
	return classifyResult(c, contract, string(raw))
}

// classifyResult folds one RESULT.md body by the card's own two lines -- line 1
// the contract, line 2 the verdict -- and by the BRANCH line.
func classifyResult(c CardRow, contract, body string) (state, branch, repo string, resultLines []string) {
	norm := strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	line1 := strings.TrimSpace(firstNonEmpty(lines))
	if line1 != contract {
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
		return "mismatch", "", "", lines
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
	if branch == "" || branch == "main" || branch == "master" {
		return "mismatch", branch, repo, lines
	}
	if c.Model == "pro" && !hasRedLine(lines) {
		return "refused", branch, repo, lines
	}
	return "done", branch, repo, lines
}

// classifyPool folds the pool layout beside the slot layout: launch admits cards
// into <root>/pool and run finalizes each task's published report to
// pool/reports/<id>/RESULT.md while the task file sits in
// pool/{done,failed}/<id>.task. The task id maps back to its card label through
// the task file's own label line -- line 1, the card's RESULT contract line --
// or the sidecar's label written by nova-swarm add --label. A task in failed/
// whose RESULT.md exists with a first line equal to the card's RESULT line is
// harvested as a result, not a failure: done/ is read first, failed/ second,
// latest task first. It returns "" when no pool task carries this card's label
// with a published report.
func classifyPool(root string, c CardRow, contract string) (state, branch, repo string, resultLines []string, dir string) {
	pool := filepath.Join(root, "pool")
	for _, st := range []string{"done", "failed"} {
		for _, id := range poolTaskIDs(pool, st, c.Label) {
			raw, err := os.ReadFile(filepath.Join(pool, "reports", id, "RESULT.md"))
			if err != nil {
				continue
			}
			state, branch, repo, resultLines := classifyResult(c, contract, string(raw))
			return state, branch, repo, resultLines, poolPushDir(pool, st, id)
		}
	}
	return "", "", "", nil, ""
}

// poolTaskIDs returns the task ids in pool/<state>/ whose task file's label line
// or sidecar label names the card, latest first.
func poolTaskIDs(pool, state, label string) []string {
	entries, err := os.ReadDir(filepath.Join(pool, state))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".task") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".task")
		if poolTaskLabel(pool, state, id) == label {
			out = append(out, id)
		}
	}
	// ReadDir sorts by name and an id begins with its UTC stamp: reverse for latest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// poolTaskLabel is the card label a pool task carries: the label on the task
// file's own first line (the card's RESULT contract line), else the sidecar's
// label written by nova-swarm add --label.
func poolTaskLabel(pool, state, id string) string {
	if raw, err := os.ReadFile(filepath.Join(pool, state, id+".task")); err == nil {
		norm := strings.ReplaceAll(string(raw), "\r\n", "\n")
		if label := contractLabel(firstNonEmpty(strings.Split(norm, "\n"))); label != "" {
			return label
		}
	}
	if raw, err := os.ReadFile(filepath.Join(pool, state, id+".json")); err == nil {
		var sc struct {
			Label string `json:"label"`
		}
		if json.Unmarshal(raw, &sc) == nil {
			return sc.Label
		}
	}
	return ""
}

// contractLabel is the label on a card's RESULT contract line: the word after RESULT.
func contractLabel(line string) string {
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) >= 2 && f[0] == "RESULT" {
		return f[1]
	}
	return ""
}

// poolPushDir is where harvest pushes a pool card's branch from: the job's own
// directory while the sidecar still names one on disk, else the retained report
// directory.
func poolPushDir(pool, state, id string) string {
	if raw, err := os.ReadFile(filepath.Join(pool, state, id+".json")); err == nil {
		var sc struct {
			Job string `json:"job"`
		}
		if json.Unmarshal(raw, &sc) == nil && sc.Job != "" {
			if fi, err := os.Stat(sc.Job); err == nil && fi.IsDir() {
				return sc.Job
			}
		}
	}
	return filepath.Join(pool, "reports", id)
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
