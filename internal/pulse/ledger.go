package pulse

// The approvals ledger and the sweep that walks it.
//
// Pit stop 3, bug 4 (issue #828, class D): the loop enqueued a PR only when a read said
// APPROVE *and* the checks were green at that instant. Fifty-one approved PRs whose checks
// were still QUEUED were logged `(not merged)` and never looked at again, and a person
// swept them by hand. The rule that retires the class: an approval is a ledger ROW, not a
// moment. Every read verdict is one row — pr, head, card, verdict, at, enqueued_at,
// closed_at — appended to <queue>/ledger.tsv, and every tick the sweep walks the open rows:
//
//   green, not draft, not held  -> enqueue once, and the row carries enqueued_at
//   head moved                  -> the row is marked stale; a read is owed on the new head
//   merged or closed            -> the row is closed
//   anything else               -> the row stays open and the next sweep looks again
//
// The file is append-only and has one writer: a mark is a NEW row for the same PR, under
// the queue's own lock (number.go), and a PR's state is its LAST row. Nothing is edited in
// place, so a reader that arrives mid-write reads whole rows and an older truth, never a
// torn one.
//
// The holds are lines the code reads, never prose in a policy document (bug 5): a `hold`
// label, a draft, and a title that opens `nova-work red tests`.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ledgerFileName is the one file this ledger lives in, under the queue directory.
const ledgerFileName = "ledger.tsv"

// ledgerHeader names the seven fields, so the file reads without this source beside it.
const ledgerHeader = "# pr\thead\tcard\tverdict\tat\tenqueued_at\tclosed_at\n"

// ledgerDash is an empty field: every row has seven fields, and a missing one is never a blank.
const ledgerDash = "-"

// redTestTitle is the title prefix a red-test PR carries; the policy holds those PRs, so
// the sweep never enqueues one (bug 5: seven red-test PRs enqueued and dequeued by hand).
const redTestTitle = "nova-work red tests"

// holdLabel is the label a person puts on a PR that must not merge yet.
const holdLabel = "hold"

// LedgerRow is one row of the approvals ledger.
type LedgerRow struct {
	PR         int
	Head       string
	Card       string
	Verdict    string // APPROVE, HOLD, STALE
	At         string // when the verdict was recorded, RFC3339 UTC
	EnqueuedAt string // when the merge was enqueued, or "-"
	ClosedAt   string // when the row was closed, or "-"
}

// PRCheck is one check on a PR, normalised to one state token.
type PRCheck struct {
	Name  string
	State string
}

// PRView is everything the sweep needs to know about one pull request.
type PRView struct {
	Number  int
	State   string // OPEN, MERGED, CLOSED
	IsDraft bool
	Head    string
	Labels  []string
	Title   string
	Checks  []PRCheck
	// AutoMerge says the forge is carrying a STANDING INSTRUCTION to merge this pull
	// request when its checks go green. It is not an enqueue and it is not ours: the sweep
	// reads it as a hold, and `nova-merge queue audit` takes it off.
	AutoMerge bool

	// The bounded, public features the ordering decision reads (#896). A source
	// that cannot answer one leaves it zero; the question still holds.
	ChangedFiles int
	Additions    int
	Deletions    int
	Packages     []string
	RedRuns      int     // red head runs in the last day
	AgeHours     float64 // how long the PR has been open
	GroupFailed  bool    // a group this PR joined failed before
}

// PRSource answers what a pull request looks like right now. The real one runs gh; a test
// drives a fake, so no test of this package ever reaches the network.
type PRSource interface {
	View(repo string, pr int) (PRView, error)
}

// Enqueuer puts one PR in the merge queue, exactly once per approval.
type Enqueuer interface {
	Enqueue(repo string, pr int) error
}

// SweepInput is the sweep verb's input, held apart from flag parsing.
type SweepInput struct {
	Repo     string
	Queue    string
	Source   PRSource
	Enqueuer Enqueuer
	Scorer   Scorer  // nil leaves the enqueue order exactly as it was
	Floor    float64 // the ordering score's floor; 0 is DefaultOrderFloor
	Now      func() time.Time
	Stdout   io.Writer
	Stderr   io.Writer
}

// Sweep walks every open ledger row once and prints one SWEEP line. It returns 0 when it
// ran, 2 when it could not.
func Sweep(in SweepInput) int {
	if strings.TrimSpace(in.Repo) == "" {
		fmt.Fprintf(in.Stderr, "SWEEP REFUSED: --repo is required (pass --repo <owner>/<name>)\n")
		return 2
	}
	if info, err := os.Stat(in.Queue); err != nil || !info.IsDir() {
		fmt.Fprintf(in.Stderr, "SWEEP REFUSED: --queue %s is not a directory (pass the queue directory the reads write their verdicts into)\n", oneline.Field(in.Queue))
		return 2
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	stamp := now().UTC().Format(time.RFC3339)

	seeded, err := seedFromApproved(in.Queue, in.Repo, stamp)
	if err != nil {
		fmt.Fprintf(in.Stderr, "SWEEP REFUSED: %s (fix the ledger under %s, then sweep again)\n", oneline.Err(err), oneline.Field(in.Queue))
		return 2
	}
	rows, err := ReadLedger(in.Queue)
	if err != nil {
		fmt.Fprintf(in.Stderr, "SWEEP REFUSED: %s (fix the ledger under %s, then sweep again)\n", oneline.Err(err), oneline.Field(in.Queue))
		return 2
	}

	var enqueued, stale, closed, held, pending, red, unread int
	var marks []LedgerRow
	var batch []orderCandidate
	open := OpenRows(rows)
	for _, row := range open {
		view, err := in.Source.View(in.Repo, row.PR)
		if err != nil {
			unread++
			continue
		}
		switch {
		case !strings.EqualFold(view.State, "OPEN") && view.State != "":
			closed++
			marks = append(marks, mark(row, row.EnqueuedAt, stamp, row.Verdict))
			// A merge is the day's one durable fact about this PR, and the only place it
			// is written down: `nova-pulse status --oneline` counts merges=<n> out of
			// <queue>/MERGED (statusline.go's countDay), and nothing was writing that file.
			// A closed-unmerged PR is not a merge and is not written here.
			if strings.EqualFold(view.State, "MERGED") {
				appendRow(filepath.Join(in.Queue, "MERGED"), stamp, "MERGED",
					fmt.Sprintf("%s#%d", oneline.Field(in.Repo), row.PR))
			}
		case view.Head != "" && view.Head != row.Head:
			stale++ // a read is owed on the new head; this approval no longer stands
			marks = append(marks, mark(row, row.EnqueuedAt, stamp, "STALE"))
		case row.EnqueuedAt != ledgerDash && row.EnqueuedAt != "":
			// already in the queue; it closes when the merge lands.
		case row.Verdict != "APPROVE" || view.IsDraft || heldPR(view):
			held++
		case view.AutoMerge:
			// AN AUTO-MERGE IS NOT AN ENQUEUE. This arm used to read it as one -- "somebody
			// enqueued it already" -- and mark the row; on 2026-09-18 that reading was
			// exactly backwards. --auto leaves a standing instruction on the forge which
			// fires later with nobody in the room, and four pull requests reached dev that
			// way while twenty-seven more sat armed. So the row is HELD and left open: the
			// instruction comes off with `nova-merge queue audit --repo <owner>/<name>`, and
			// then this row is swept like any other -- into a batch, which is the one thing
			// that reaches the queue (Glenn, 2026-09-18).
			held++
		default:
			switch checksVerdict(view.Checks) {
			case "pending":
				pending++
			case "red":
				red++
			default:
				batch = append(batch, orderCandidate{row: row, view: view})
			}
		}
	}

	// #896: order the whole batch once, then enqueue. With no Scorer the batch
	// keeps the sweep's existing order, so the fallback path is untouched.
	if in.Scorer != nil && len(batch) > 0 {
		floor := in.Floor
		if floor <= 0 {
			floor = DefaultOrderFloor
		}
		batch = scoreBatch(batch, in.Scorer, floor, in.Stdout)
	}
	for i := range batch {
		c := &batch[i]
		if err := in.Enqueuer.Enqueue(in.Repo, c.row.PR); err != nil {
			unread++
			continue
		}
		enqueued++
		marks = append(marks, mark(c.row, stamp, c.row.ClosedAt, c.row.Verdict))
	}

	if err := AppendLedger(in.Queue, marks...); err != nil {
		fmt.Fprintf(in.Stderr, "SWEEP REFUSED: %s (fix the ledger under %s, then sweep again)\n", oneline.Err(err), oneline.Field(in.Queue))
		return 2
	}

	fmt.Fprintf(in.Stdout, "SWEEP repo=%s open=%d seeded=%d enqueued=%d stale=%d closed=%d held=%d pending=%d red=%d unread=%d\n",
		oneline.Field(in.Repo), len(open), seeded, enqueued, stale, closed, held, pending, red, unread)
	return 0
}

// mark is a row re-appended with its marks: append-only, so the latest row is the state.
func mark(row LedgerRow, enqueuedAt, closedAt, verdict string) LedgerRow {
	row.EnqueuedAt, row.ClosedAt, row.Verdict = orDash(enqueuedAt), orDash(closedAt), verdict
	return row
}

// heldPR is every hold the code reads: the label, and the red-test title the policy holds.
func heldPR(view PRView) bool {
	if slices.ContainsFunc(view.Labels, func(l string) bool { return strings.EqualFold(strings.TrimSpace(l), holdLabel) }) {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(view.Title), redTestTitle)
}

// checksVerdict folds a PR's checks into one word: green, pending or red. No check having
// reported yet is pending, never green — the merge condition's own rule.
func checksVerdict(checks []PRCheck) string {
	if len(checks) == 0 {
		return "pending"
	}
	verdict := "green"
	for _, c := range checks {
		switch strings.ToUpper(strings.TrimSpace(c.State)) {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
		case "PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED", "WAITING", "REQUESTED":
			if verdict != "red" {
				verdict = "pending"
			}
		default:
			verdict = "red"
		}
	}
	return verdict
}

// seedFromApproved appends a row for every APPROVED line of this repo the ledger has not
// seen: the manager tier writes that file, and an approval recorded before this ledger
// existed is still walked every tick.
func seedFromApproved(queue, repo, stamp string) (int, error) {
	raw, err := os.ReadFile(filepath.Join(queue, "APPROVED"))
	if err != nil {
		return 0, nil
	}
	rows, err := ReadLedger(queue)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[fmt.Sprintf("%d %s", r.PR, r.Head)] = true
	}
	var fresh []LedgerRow
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != repo {
			continue
		}
		pr, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		key := fmt.Sprintf("%d %s", pr, f[2])
		if seen[key] {
			continue
		}
		fresh = append(fresh, LedgerRow{PR: pr, Head: f[2], Card: ledgerDash, Verdict: "APPROVE", At: stamp})
		seen[key] = true
	}
	if err := AppendLedger(queue, fresh...); err != nil {
		return 0, err
	}
	return len(fresh), nil
}

// AppendLedger appends rows under the queue's lock: one writer, append only, one take of
// the lock however many rows a sweep has to mark.
func AppendLedger(queue string, rows ...LedgerRow) error {
	if len(rows) == 0 {
		return nil
	}
	unlock, err := lockQueue(queue)
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(queue, ledgerFileName)
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("the ledger cannot be written: %w", err)
	}
	defer f.Close()
	if os.IsNotExist(statErr) {
		if _, err := io.WriteString(f, ledgerHeader); err != nil {
			return fmt.Errorf("the ledger header cannot be written: %w", err)
		}
	}
	var b strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", row.PR,
			oneline.Field(orDash(row.Head)), oneline.Field(orDash(row.Card)), oneline.Field(orDash(row.Verdict)),
			oneline.Field(orDash(row.At)), oneline.Field(orDash(row.EnqueuedAt)), oneline.Field(orDash(row.ClosedAt)))
	}
	if _, err := io.WriteString(f, b.String()); err != nil {
		return fmt.Errorf("the ledger row cannot be written: %w", err)
	}
	return nil
}

// ReadLedger reads every row of <queue>/ledger.tsv in file order.
func ReadLedger(queue string) ([]LedgerRow, error) {
	raw, err := os.ReadFile(filepath.Join(queue, ledgerFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("the ledger cannot be read: %w", err)
	}
	var rows []LedgerRow
	for n, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return nil, fmt.Errorf("ledger.tsv line %d wants pr, head, card, verdict, at, enqueued_at, closed_at, got %d fields", n+1, len(f))
		}
		pr, err := strconv.Atoi(strings.TrimSpace(f[0]))
		if err != nil {
			return nil, fmt.Errorf("ledger.tsv line %d: %q is not a PR number", n+1, f[0])
		}
		rows = append(rows, LedgerRow{PR: pr, Head: f[1], Card: f[2], Verdict: f[3], At: f[4], EnqueuedAt: f[5], ClosedAt: f[6]})
	}
	return rows, nil
}

// OpenRows folds the log into one row per PR — the last one written — and keeps the rows
// that are not closed, in PR order.
func OpenRows(rows []LedgerRow) []LedgerRow {
	latest := map[int]LedgerRow{}
	var order []int
	for _, r := range rows {
		if _, seen := latest[r.PR]; !seen {
			order = append(order, r.PR)
		}
		latest[r.PR] = r
	}
	slices.Sort(order)
	var open []LedgerRow
	for _, pr := range order {
		if r := latest[pr]; r.ClosedAt == ledgerDash || r.ClosedAt == "" {
			open = append(open, r)
		}
	}
	return open
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return ledgerDash
	}
	return s
}

// GHSource is the real PRSource: one bounded `gh pr view` per row, the one JSON call the
// sweep makes per open approval.
type GHSource struct{ Timeout time.Duration }

// ghFile is one changed file gh names, from which the packages touched are folded.
type ghFile struct {
	Path string `json:"path"`
}

// ghPRView is the JSON gh answers; the rollup carries both check runs and status contexts.
type ghPRView struct {
	State        string   `json:"state"`
	IsDraft      bool     `json:"isDraft"`
	HeadRefOid   string   `json:"headRefOid"`
	Title        string   `json:"title"`
	ChangedFiles int      `json:"changedFiles"`
	Additions    int      `json:"additions"`
	Deletions    int      `json:"deletions"`
	CreatedAt    string   `json:"createdAt"`
	Files        []ghFile `json:"files"`
	Labels       []struct {
		Name string `json:"name"`
	} `json:"labels"`
	StatusCheckRollup []struct {
		Name       string `json:"name"`
		Context    string `json:"context"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	} `json:"statusCheckRollup"`
	AutoMergeRequest *struct {
		EnabledAt string `json:"enabledAt"`
	} `json:"autoMergeRequest"`
}

// packagesOf folds a PR's changed files into the bounded, sorted set of package
// directories it touches, so the ordering question names packages, not paths.
func packagesOf(files []ghFile) []string {
	seen := map[string]bool{}
	for _, f := range files {
		d := path.Dir(strings.TrimSpace(f.Path))
		if d == "" || d == "/" || d == "." {
			continue
		}
		seen[d] = true
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func (g GHSource) View(repo string, pr int) (PRView, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(pr), "-R", repo,
		"--json", "state,isDraft,headRefOid,labels,title,statusCheckRollup,autoMergeRequest,changedFiles,additions,deletions,files,createdAt").Output()
	if err != nil {
		return PRView{}, fmt.Errorf("gh pr view %d: %w", pr, err)
	}
	var v ghPRView
	if err := json.Unmarshal(out, &v); err != nil {
		return PRView{}, fmt.Errorf("gh pr view %d did not answer JSON: %w", pr, err)
	}
	view := PRView{Number: pr, State: v.State, IsDraft: v.IsDraft, Head: v.HeadRefOid, Title: v.Title, AutoMerge: v.AutoMergeRequest != nil,
		ChangedFiles: v.ChangedFiles, Additions: v.Additions, Deletions: v.Deletions,
		Packages: packagesOf(v.Files)}
	if at, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil {
		view.AgeHours = time.Since(at).Hours()
	}
	for _, l := range v.Labels {
		view.Labels = append(view.Labels, l.Name)
	}
	for _, c := range v.StatusCheckRollup {
		name := c.Name
		if name == "" {
			name = c.Context
		}
		state := c.State
		switch {
		case c.Status != "" && !strings.EqualFold(c.Status, "COMPLETED"):
			state = c.Status // QUEUED or IN_PROGRESS: a check that has not finished is pending
		case c.Conclusion != "":
			state = c.Conclusion
		}
		view.Checks = append(view.Checks, PRCheck{Name: name, State: state})
	}
	return view, nil
}

// GHEnqueuer is the real Enqueuer, and it is now a THIN ADAPTER ONTO THE ONE DOOR
// (internal/merge.Enqueuer).
//
// It used to run `gh pr merge <n> -R <repo> --auto`, once per approval, on the reasoning
// that the sweep only reached it when every check was green and none pending. That
// reasoning was wrong in the one way that matters: --auto enqueues nothing. It leaves a
// STANDING INSTRUCTION on the forge, which fires later, with nobody in the room, against
// whatever the head has become. On 2026-09-18 twenty-seven open pull requests were
// carrying one, and four of them walked into the dev merge queue on their own. Glenn
// locked it the same day: nothing reaches the dev merge queue but a batch.
//
// So this reads the pull request's head and offers it to the one door, which admits a
// batch's head -- or a head with that head's BATCH OK receipt -- and refuses everything
// else. A refusal is an error like any other here, and the sweep leaves the row open. There
// is no path from this type to a merge of any kind.
type GHEnqueuer struct{ Timeout time.Duration }

func (g GHEnqueuer) Enqueue(repo string, pr int) error {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	host := merge.NewGHEnqueue(repo, timeout, nil)
	ref, sha, err := host.Head(ctx, pr)
	if err != nil {
		return fmt.Errorf("read pull request %d's head: %s", pr, oneline.Cap(strings.TrimSpace(err.Error()), 120))
	}
	return merge.NewEnqueuer(host).Enqueue(ctx, merge.EnqueuePR{Number: pr, HeadRef: ref, HeadSHA: sha}, false)
}

// appendRow appends one tab-separated record. It is appendLine's sibling, and the
// difference is the point: each FIELD is escaped to one line, and the tabs between them are
// left alone, because this file is a table a reader splits on them.
func appendRow(path string, fields ...string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	escaped := make([]string, 0, len(fields))
	for _, v := range fields {
		escaped = append(escaped, oneline.Escape(v))
	}
	fmt.Fprintln(f, strings.Join(escaped, "\t"))
}
