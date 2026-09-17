package pulse

// status is SPEC-PULSE's "Status" verb: the one-verb answer to the all-day questions --
// what runs where, how wide, at max throughput, what landed and who adopted it, what is in
// flight and what remains and how long, what new work appeared, are we converging. It makes
// no model call and prints at most --max lines per capped kind, counts not lists.
//
// Sources, all files or a cached gh step: the queue directory (pending, launched, done,
// failed, and the HOLD / ESCALATE / DOGFOOD / UNREAD / DIRTY / UNCARDED / COORDINATOR / REPO
// state files), the benches' slot files and usage.tsv rows under --roots, and the ADOPT
// files (<bench>/ADOPT/<friend>, one line "version=<v> receipt=<n> edges=<n>") walk of the
// in-scope benches. gh is called once per tick for PRs and once for issues.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// StatusInput is everything the status verb needs, apart from flag parsing so a test can
// drive it against fake directories and a fake gh on PATH.
type StatusInput struct {
	Queue          string // the queue directory: pending, launched, done, failed and the state files
	Roots          string // comma-separated bench roots, the benches in scope
	SlotsStores    string // comma-separated bench slot-lease stores to report utilisation for
	Day            string // YYYY-MM-DD the day window starts at; empty means today (UTC)
	Max            int
	Timeout        time.Duration
	ExpandingHours int // the sustained window the EXPANDING verdict requires, in whole hours; <= 0 means 2
	Stdout         io.Writer
	Stderr         io.Writer
	Now            func() time.Time
}

// usageRow is one usage.tsv row reduced to the columns status reads.
type usageRow struct {
	started time.Time
	ended   time.Time
	hasTime bool
	rc      int
	usd     float64
	wall    float64 // ended - started, seconds
}

// Status prints the eight line kinds and returns 0, or 2 when it could not run.
func Status(in StatusInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Max == 0 {
		in.Max = bounded.Default
	}
	if in.Timeout <= 0 {
		in.Timeout = 120 * time.Second
	}
	if in.ExpandingHours <= 0 {
		in.ExpandingHours = 2
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.Queue, "queue", "the queue directory holding pending, launched, done and the state files"},
		{in.Roots, "roots", "the benches to report, comma separated"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "STATUS", fmt.Errorf("missing --%s; refusing to guess (%s)", r.name, r.wants))
		}
	}
	now := in.Now()
	day := now.Format("2006-01-02")
	if strings.TrimSpace(in.Day) != "" {
		day = strings.TrimSpace(in.Day)
	}
	dayStart, err := time.Parse("2006-01-02", day)
	if err != nil {
		return refusal(in.Stderr, "STATUS", fmt.Errorf("--day wants YYYY-MM-DD, got %q (say the day the window starts at)", in.Day))
	}
	hourStart := now.Add(-time.Hour)

	roots := splitList(in.Roots)
	rows := collectUsage(roots)
	queue := readQueue(in.Queue)
	repo := firstLine(filepath.Join(in.Queue, "REPO"))
	prs, issues := readGh(repo, in.Timeout) // cached per tick

	out := in.Stdout

	// WIDTH, one per bench in scope, capped.
	width := bounded.Capped(out, in.Max, "STATUS", "width", "--max 0 to show every bench")
	for _, r := range roots {
		width.Line(widthLine(r))
	}
	width.More()

	fmt.Fprintf(out, "STATUS QUEUE pending=%d gated=%d launched=%d done=%d failed=%d\n",
		queue.pending, queue.gated, queue.launched, queue.done, queue.failed)

	rate := rateOf(rows, dayStart, now)
	if rate.known {
		fmt.Fprintf(out, "STATUS RATE cards_per_hour=%d p50_s=%d p90_s=%d usd_per_card=%s parallelism=%s\n",
			rate.cardsPerHour, rate.p50, rate.p90, rate.usdPerCard, rate.parallelism)
	} else {
		fmt.Fprintln(out, "STATUS RATE cards_per_hour=- p50_s=- p90_s=- usd_per_card=- parallelism=-")
	}

	remaining := remainingOf(addCards(queue.pending, queue.gated), in.Queue, rate)
	fmt.Fprintf(out, "STATUS REMAINING queue=%d unread_prs=%d dirty_prs=%d uncarded_issues=%d hours=%d\n",
		remaining.queue, remaining.unreadPrs, remaining.dirtyPrs, remaining.uncardedIssues, remaining.hours)

	// One verdict per tick, from the hourly samples: the sustained window and the
	// threshold that produced it print on both CONTRACTION lines, so a reader never has
	// to infer what the verdict required (#177: configurable windows and thresholds must
	// stay visible; avoid reacting to one arbitrary sampling instant).
	hourCut, hourDone := countStarted(rows, hourStart, now), countEnded(rows, hourStart, now)
	hourOpened, hourMerged := prsOpenedMerged(prs, hourStart, now)
	hourFiled, hourClosed := issuesFiledClosed(issues, hourStart, now)
	verdict := contractionVerdict(in.Queue,
		hourCut > hourDone || hourOpened > hourMerged || hourFiled > hourClosed, now, in.ExpandingHours)
	for _, w := range []struct {
		name  string
		start time.Time
	}{
		{"hour", hourStart}, {"day", dayStart},
	} {
		cut := countStarted(rows, w.start, now)
		done := countEnded(rows, w.start, now)
		opened, merged := prsOpenedMerged(prs, w.start, now)
		filed, closed := issuesFiledClosed(issues, w.start, now)
		fmt.Fprintf(out, "STATUS CONTRACTION %s cards=%d/%d prs=%d/%d issues=%d/%d verdict=%s window=%dh above=1\n",
			w.name, cut, done, opened, merged, filed, closed, verdict, in.ExpandingHours)
	}

	// STREAM, one per stream, and the two-hour EXPANDING alarm. The ratio is cards
	// opened / cards closed over the rolling tick window in <queue>/CONVERGENCE.tsv.
	windows := convergence(in.Queue, now)
	expanding := false
	streams := bounded.Capped(out, in.Max, "STATUS", "stream", "--max 0 to show every stream")
	for _, sw := range windows {
		streams.Line(fmt.Sprintf("STATUS STREAM %s opened=%d closed=%d ratio=%s",
			oneline.Field(sw.stream), sw.opened, sw.closed, sw.ratio))
		if sw.twoHour {
			expanding = true
		}
	}
	streams.More()
	for _, sw := range windows {
		if sw.twoHour {
			fmt.Fprintf(out, "STATUS EXPANDING stream=%s hours=%d\n", oneline.Field(sw.stream), sw.hours)
		}
	}

	// ADOPTION, one per friend (coordinator included), capped.
	friends := adoptions(roots, in.Queue)
	adopt := bounded.Capped(out, in.Max, "STATUS", "friend", "--max 0 to show every friend")
	names := make([]string, 0, len(friends))
	for f := range friends {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		adopt.Line(fmt.Sprintf("STATUS ADOPTION %s version=%s receipt=%d edges=%d",
			oneline.Field(f), field(friends[f].version), friends[f].receipt, friends[f].edges))
	}
	adopt.More()

	fmt.Fprintf(out, "STATUS OPEN dogfood=%d holds=%d escalations=%d\n",
		countLines(in.Queue, "DOGFOOD"), countLines(in.Queue, "HOLD"), countLines(in.Queue, "ESCALATE"))

	merged, toolNames := mergedTools(prs, dayStart, now)
	fmt.Fprintf(out, "STATUS TOOLS merged_since_adoption=%d %s\n", merged, strings.Join(toolNames, ", "))

	starved := false
	for _, store := range splitList(in.SlotsStores) {
		bench := filepath.Base(store)
		capacity, reserve, held, free, heldBy, shares, serr := swarm.SlotUtilisation(store, now)
		if serr != nil {
			return refusal(in.Stderr, "STATUS", serr)
		}
		fmt.Fprintf(out, "STATUS SLOTS bench=%s capacity=%d reserve=%d held=%d free=%d owners=%s\n",
			oneline.Field(bench), capacity, reserve, held, free, slotOwnersField(heldBy, shares))
		if queue.pending > 0 && free > 0 {
			marker := filepath.Join(in.Queue, "STARVED-"+bench)
			if _, merr := os.Stat(marker); merr == nil {
				fmt.Fprintf(out, "STATUS STARVED bench=%s free=%d pending=%d\n",
					oneline.Field(bench), free, queue.pending)
				starved = true
			} else {
				_ = os.WriteFile(marker, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o644)
			}
		} else {
			_ = os.Remove(filepath.Join(in.Queue, "STARVED-"+bench))
		}
	}
	if expanding || starved {
		// The alarm is a state the coordinator must act on: it exits like a refusal,
		// the same way PULSE UNDER-WIDTH does (SPEC-PULSE, exit codes).
		return 2
	}
	return 0
}

// slotOwnersField renders owner:held/share pairs in name order for the SLOTS line.
func slotOwnersField(heldBy map[string]int, shares map[string]int) string {
	names := map[string]bool{}
	for n := range heldBy {
		names[n] = true
	}
	for n := range shares {
		names[n] = true
	}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	parts := make([]string, 0, len(sorted))
	for _, n := range sorted {
		parts = append(parts, fmt.Sprintf("%s:%d/%d", oneline.Field(n), heldBy[n], shares[n]))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// widthLine reads one bench's slot files into its WIDTH line.
func widthLine(root string) string {
	var slots, running, load int
	matches, _ := filepath.Glob(filepath.Join(root, "pool", "slots", "*.json"))
	for _, m := range matches {
		slots++
		raw, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var sf struct {
			State string `json:"state"`
		}
		if json.Unmarshal(raw, &sf) != nil {
			continue
		}
		if sf.State == "launched" {
			running++
		}
		if sf.State != "free" {
			load++
		}
	}
	return fmt.Sprintf("STATUS WIDTH %s running=%d slots=%d load=%d headroom=%d",
		oneline.Field(filepath.Base(root)), running, slots, load, slots-load)
}

type queueCounts struct{ pending, gated, launched, done, failed int }

// readQueue counts the cards in each queue directory; a pending card carrying a gate line
// is gated, not pending.
func readQueue(dir string) queueCounts {
	var q queueCounts
	q.pending = countUngated(dir, "pending")
	q.gated = countGated(dir, "pending")
	q.launched = countCards(dir, "launched")
	q.done = countCards(dir, "done")
	q.failed = countCards(dir, "failed")
	return q
}

func countCards(dir, sub string) int {
	matches, _ := filepath.Glob(filepath.Join(dir, sub, "card-*.md"))
	return len(matches)
}

func countGated(dir, sub string) int {
	n := 0
	matches, _ := filepath.Glob(filepath.Join(dir, sub, "card-*.md"))
	for _, m := range matches {
		if raw, err := os.ReadFile(m); err == nil && gateLine.Match(raw) {
			n++
		}
	}
	return n
}

func countUngated(dir, sub string) int { return countCards(dir, sub) - countGated(dir, sub) }

// collectUsage walks every bench for usage.tsv rows.
func collectUsage(roots []string) []usageRow {
	var rows []usageRow
	for _, r := range roots {
		_ = filepath.WalkDir(r, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "usage.tsv" {
				return nil
			}
			if row, ok := parseUsage(path); ok {
				rows = append(rows, row)
			}
			return nil
		})
	}
	return rows
}

func parseUsage(path string) (usageRow, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return usageRow{}, false
	}
	lines := strings.Split(string(raw), "\n")
	for _, l := range lines {
		if l == "" || strings.HasPrefix(l, "job") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) < 13 {
			continue
		}
		started, errS := time.Parse(time.RFC3339, f[2])
		ended, errE := time.Parse(time.RFC3339, f[3])
		if errS != nil || errE != nil {
			continue
		}
		rc, _ := strconv.Atoi(f[4])
		usd := 0.0
		if f[12] != "-" {
			usd, _ = strconv.ParseFloat(f[12], 64)
		}
		return usageRow{started: started, ended: ended, hasTime: true, rc: rc, usd: usd,
			wall: ended.Sub(started).Seconds()}, true
	}
	return usageRow{}, false
}

type rateLine struct {
	known        bool
	cardsPerHour int
	p50, p90     int
	usdPerCard   string
	parallelism  string
}

// rateOf is per SPEC-PULSE's progress arithmetic over the day's usage rows.
func rateOf(rows []usageRow, dayStart, now time.Time) rateLine {
	var in []usageRow
	for _, r := range rows {
		if r.hasTime && !r.started.Before(dayStart) && !r.started.After(now) {
			in = append(in, r)
		}
	}
	if len(in) == 0 {
		return rateLine{} // known=false: a cost or latency never measured is unknown, never zero
	}
	r := rateLine{known: true}
	minStart, maxEnd := in[0].started, in[0].ended
	var busy, usd float64
	for _, u := range in {
		if u.started.Before(minStart) {
			minStart = u.started
		}
		if u.ended.After(maxEnd) {
			maxEnd = u.ended
		}
		busy += u.wall
		usd += u.usd
	}
	span := maxEnd.Sub(minStart).Seconds()
	if span < 1 {
		span = 1
	}
	r.cardsPerHour = int(float64(len(in)) / (span / 3600))
	r.p50, r.p90 = percentileSecs(in, 0.5), percentileSecs(in, 0.9)
	r.usdPerCard = fmt.Sprintf("%.4f", usd/float64(len(in)))
	r.parallelism = fmt.Sprintf("%.1f", busy/span)
	return r
}

func percentileSecs(rows []usageRow, p float64) int {
	walls := make([]float64, len(rows))
	for i, r := range rows {
		walls[i] = r.wall
	}
	sort.Float64s(walls)
	idx := int(float64(len(walls)-1) * p)
	if idx < 0 {
		idx = 0
	}
	return int(walls[idx])
}

func countStarted(rows []usageRow, start, end time.Time) int {
	n := 0
	for _, r := range rows {
		if r.hasTime && !r.started.Before(start) && !r.started.After(end) {
			n++
		}
	}
	return n
}

func countEnded(rows []usageRow, start, end time.Time) int {
	n := 0
	for _, r := range rows {
		if r.hasTime && !r.ended.Before(start) && !r.ended.After(end) {
			n++
		}
	}
	return n
}

type remainingLine struct {
	queue, unreadPrs, dirtyPrs, uncardedIssues, hours int
}

func remainingOf(queue int, dir string, rate rateLine) remainingLine {
	r := remainingLine{queue: queue}
	r.unreadPrs = countLines(dir, "UNREAD")
	r.dirtyPrs = countLines(dir, "DIRTY")
	r.uncardedIssues = countLines(dir, "UNCARDED")
	remaining := r.queue + r.unreadPrs + r.dirtyPrs + r.uncardedIssues
	par, _ := strconv.ParseFloat(rate.parallelism, 64)
	if par < 1 {
		par = 1
	}
	p90 := rate.p90
	if p90 < 1 {
		p90 = 1
	}
	r.hours = int(float64(remaining) * float64(p90) / par * 1.5)
	return r
}

type ghPR struct {
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	CreatedAt string  `json:"createdAt"`
	MergedAt  *string `json:"mergedAt"`
}

type ghIssueStatus struct {
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	CreatedAt string  `json:"createdAt"`
	ClosedAt  *string `json:"closedAt"`
}

// readGh runs the two cached gh queries; a missing repo or a failing gh yields empty slices.
func readGh(repo string, timeout time.Duration) ([]ghPR, []ghIssueStatus) {
	if repo == "" {
		return nil, nil
	}
	var prs []ghPR
	var issues []ghIssueStatus
	if out := runGh(timeout, "pr", "list", "-R", repo, "--state", "all", "--json", "number,title,createdAt,mergedAt"); out != "" {
		_ = json.Unmarshal([]byte(out), &prs)
	}
	if out := runGh(timeout, "issue", "list", "-R", repo, "--state", "all", "--json", "number,title,createdAt,closedAt"); out != "" {
		_ = json.Unmarshal([]byte(out), &issues)
	}
	return prs, issues
}

func runGh(timeout time.Duration, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func prsOpenedMerged(prs []ghPR, start, end time.Time) (opened, merged int) {
	for _, p := range prs {
		if t, err := time.Parse(time.RFC3339, p.CreatedAt); err == nil && !t.Before(start) && !t.After(end) {
			opened++
		}
		if p.MergedAt != nil {
			if t, err := time.Parse(time.RFC3339, *p.MergedAt); err == nil && !t.Before(start) && !t.After(end) {
				merged++
			}
		}
	}
	return opened, merged
}

func issuesFiledClosed(issues []ghIssueStatus, start, end time.Time) (filed, closed int) {
	for _, i := range issues {
		if t, err := time.Parse(time.RFC3339, i.CreatedAt); err == nil && !t.Before(start) && !t.After(end) {
			filed++
		}
		if i.ClosedAt != nil {
			if t, err := time.Parse(time.RFC3339, *i.ClosedAt); err == nil && !t.Before(start) && !t.After(end) {
				closed++
			}
		}
	}
	return filed, closed
}

// contractionVerdict is EXPANDING only when the stream ratios have been above the
// threshold for the sustained window's consecutive sampled hours; the run of
// above-threshold hours is remembered between ticks, so no one sampling instant decides
// it (#177). The marker holds the latest above-threshold hour and the run length as
// "<hour> <n>"; a marker in the old shape -- one bare hour key from before the run was
// counted -- is a run of one.
func contractionVerdict(queue string, above bool, now time.Time, hours int) string {
	path := filepath.Join(queue, "EXPANDING")
	if !above {
		_ = os.Remove(path)
		return "CONVERGING"
	}
	key := now.Format("2006-01-02T15")
	prevHour := now.Add(-time.Hour).Format("2006-01-02T15")
	run := 1
	if line := firstLine(path); line != "" {
		f := strings.Fields(line)
		n := 1
		if len(f) == 2 {
			if v, err := strconv.Atoi(f[1]); err == nil && v >= 1 {
				n = v
			}
		}
		if f[0] == prevHour {
			run = n + 1
		}
	}
	_ = os.WriteFile(path, []byte(key+" "+strconv.Itoa(run)+"\n"), 0o644)
	if run >= hours {
		return "EXPANDING"
	}
	return "CONVERGING"
}

type adoption struct {
	version        string
	receipt, edges int
}

// adoptions walks every in-scope bench's ADOPT files; the coordinator is always a friend.
func adoptions(roots []string, queue string) map[string]adoption {
	out := map[string]adoption{}
	for _, r := range roots {
		matches, _ := filepath.Glob(filepath.Join(r, "ADOPT", "*"))
		for _, m := range matches {
			name := filepath.Base(m)
			if v, ok := out[name]; !ok || v.version == "" {
				out[name] = parseAdoption(m)
			}
		}
	}
	if coord := firstLine(filepath.Join(queue, "COORDINATOR")); coord != "" {
		if _, ok := out[coord]; !ok {
			out[coord] = adoption{version: ""}
		}
	}
	return out
}

func parseAdoption(path string) adoption {
	raw, err := os.ReadFile(path)
	if err != nil {
		return adoption{}
	}
	a := adoption{}
	for _, f := range strings.Fields(string(raw)) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "version":
			a.version = v
		case "receipt":
			a.receipt, _ = strconv.Atoi(v)
		case "edges":
			a.edges, _ = strconv.Atoi(v)
		}
	}
	return a
}

// mergedTools counts the PRs merged in the day window and names them.
func mergedTools(prs []ghPR, dayStart, now time.Time) (int, []string) {
	var merged int
	var names []string
	for _, p := range prs {
		if p.MergedAt == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, *p.MergedAt)
		if err != nil || t.Before(dayStart) || t.After(now) {
			continue
		}
		merged++
		names = append(names, oneline.Cap(strings.TrimSpace(p.Title), oneline.TailBytes))
	}
	return merged, names
}

// firstLine reads a state file's first non-empty line, trimmed; a missing file is "".
func firstLine(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

func countLines(dir, name string) int {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func addCards(a, b int) int { return a + b }
