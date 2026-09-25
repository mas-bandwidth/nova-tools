package pulse

// The twelve gaps a non-author found when he tried to put `status --html` in place of
// bin/status-page.sh on the Studio and correctly refused to install it. Adoption is the
// only real test of a verb: the four bench rows matched the script byte for byte, and the
// page was still not the page the fleet reads.
//
// Every one of these is red first. No test here starts ssh, scp or gh: the fleet reader,
// the self reader and the publisher are injected, and gh is the package's fake on PATH.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const edgeStamp = "2026-09-18T12:00:00Z"

// edgeRun drives the verb with every seam injected and gh faked on PATH.
type edgeRun struct {
	dir       string
	queue     string
	specs     string
	ghLog     string
	readings  []BenchReading
	self      *SelfReading
	published []publishedCall
	in        StatusHTMLInput
}

type publishedCall struct {
	dest  string
	files []PublishFile
}

func newEdgeRun(t *testing.T, benchNames ...string) *edgeRun {
	t.Helper()
	specs := fakePATH(t)
	e := &edgeRun{dir: t.TempDir(), queue: t.TempDir(), specs: specs}
	e.ghLog = filepath.Join(t.TempDir(), "gh.log")
	for _, n := range benchNames {
		e.readings = append(e.readings, BenchReading{Name: n, Live: 1, Cores: 4, Load: 1, FreeGB: 90, MemGB: 30, Allowed: 2})
	}
	benches := filepath.Join(t.TempDir(), "benches.tsv")
	var b strings.Builder
	for _, n := range benchNames {
		b.WriteString(n + "\tfake-" + n + "\t/tmp/" + n + "\t-\n")
	}
	if err := os.WriteFile(benches, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	now, _ := time.Parse(time.RFC3339, edgeStamp)
	e.in = StatusHTMLInput{
		HTML:    filepath.Join(e.dir, "index.html"),
		Benches: benches,
		Queue:   e.queue,
		Now:     func() time.Time { return now },
	}
	return e
}

// repo writes the queue's REPO file, the one thing that turns the gh rows on.
func (e *edgeRun) repo(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.queue, "REPO"), []byte(repo+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *edgeRun) gh(t *testing.T, rules ...fakeRule) {
	t.Helper()
	fakeTool(t, e.specs, "gh", fakeSpec{Log: e.ghLog, Rules: rules, Default: fakeRule{Stdout: "[]"}})
}

func (e *edgeRun) ghCalls(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(e.ghLog)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (e *edgeRun) run(t *testing.T) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	in := e.in
	in.Stdout, in.Stderr = &out, &errs
	in.Reader = func([]FleetBench, time.Time) []BenchReading { return e.readings }
	if e.self != nil {
		self := *e.self
		in.SelfRead = func(string, []string, time.Time) SelfReading { return self }
	}
	in.Ship = func(dest string, files []PublishFile, _ time.Duration) error {
		e.published = append(e.published, publishedCall{dest: dest, files: files})
		return nil
	}
	code := StatusHTML(in)
	return out.String(), errs.String(), code
}

func (e *edgeRun) page(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dir, "index.html"))
	if err != nil {
		t.Fatalf("no page: %v", err)
	}
	return string(raw)
}

func (e *edgeRun) metrics(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dir, "metrics.tsv"))
	if err != nil {
		t.Fatalf("no metrics row: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\t")
}

// prsJSON is a gh pr list answer: n merged inside the window, one outside it.
func prsJSON(t *testing.T, merged int, mergedAt, outsideAt string) string {
	t.Helper()
	type pr struct {
		Number    int     `json:"number"`
		Title     string  `json:"title"`
		CreatedAt string  `json:"createdAt"`
		MergedAt  *string `json:"mergedAt"`
	}
	var list []pr
	for i := 0; i < merged; i++ {
		at := mergedAt
		list = append(list, pr{Number: i + 1, Title: "in", CreatedAt: mergedAt, MergedAt: &at})
	}
	out := outsideAt
	list = append(list, pr{Number: 999, Title: "out", CreatedAt: outsideAt, MergedAt: &out})
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// ---- gap 1: the merged count capped at gh's default 30.

// The script passed --limit 500. Without it gh answers 30 and the page said 30 while the
// script said 273 -- and the metrics column would flatline at 30 for the rest of the day,
// which is worse than a wrong number because it looks like a steady fleet.
func TestStatusHTMLPRListAsksForMoreThanGhDefault(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.repo(t, "mas-bandwidth/nova-tools")
	e.gh(t, fakeRule{Arg: 1, Equals: "pr", Stdout: prsJSON(t, 3, "2026-09-18T11:00:00Z", "2026-09-01T00:00:00Z")})
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	found := false
	for _, call := range e.ghCalls(t) {
		if !strings.Contains(call, "pr list") {
			continue
		}
		found = true
		if !strings.Contains(call, "--limit") {
			t.Fatalf("gh pr list runs with no --limit, so the count caps at gh's default 30: %s", call)
		}
	}
	if !found {
		t.Fatalf("no gh pr list call at all: %v", e.ghCalls(t))
	}
}

// ---- gap 11: the unused issue fetch.

// The page shows no issue count, so fetching issues is a second gh round trip bought for
// nothing -- half the verb's gh time on this path.
func TestStatusHTMLDoesNotFetchIssues(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.repo(t, "mas-bandwidth/nova-tools")
	e.gh(t, fakeRule{Arg: 1, Equals: "pr", Stdout: "[]"})
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	for _, call := range e.ghCalls(t) {
		if strings.Contains(call, "issue list") {
			t.Fatalf("the page shows no issue count but the verb fetched issues: %s", call)
		}
	}
}

// ---- gap 8: a missing REPO read as zero.

// No REPO file is not "nothing merged today": it is "nobody asked the forge". A zero there
// is the DOWN bug again, in the one row a reader uses to decide whether the day is moving.
func TestStatusHTMLMissingRepoSaysDashNeverZero(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	stdout, errs, code := e.run(t)
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	if !strings.Contains(stdout, "merged=-") {
		t.Errorf("the STATUS HTML line does not say merged=-:\n%s", stdout)
	}
	page := e.page(t)
	if strings.Contains(page, "<b>0</b>") {
		t.Errorf("the page claims 0 merged with no repo to ask:\n%s", page)
	}
	if !strings.Contains(page, "<b>-</b>") {
		t.Errorf("the page does not say the merged count is unknown:\n%s", page)
	}
	m := e.metrics(t)
	if len(m) < 5 || m[3] != "-" || m[4] != "-" {
		t.Errorf("the metrics row writes a number nobody asked for: %v", m)
	}
}

// ---- gap 9: the day boundary.

// INSTALL-fleet.md's rate_counter resets at 02:00Z and the script counted merges from
// there. Counting from 00:00Z makes the page disagree with every other instrument for two
// hours a day.
func TestStatusHTMLMergedCountsFromTheDayStart(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.repo(t, "mas-bandwidth/nova-tools")
	// Two merged after 02:00Z today, one at 01:00Z today which the 02:00Z reset excludes.
	e.gh(t, fakeRule{Arg: 1, Equals: "pr", Stdout: prsJSON(t, 2, "2026-09-18T09:00:00Z", "2026-09-18T01:00:00Z")})
	stdout, errs, code := e.run(t)
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	if !strings.Contains(stdout, "merged=2") {
		t.Fatalf("merged is not counted from 02:00Z (the 01:00Z merge leaked in):\n%s", stdout)
	}
	if !strings.Contains(e.page(t), "02:00Z") {
		t.Errorf("the page does not say which boundary the count is from:\n%s", e.page(t))
	}
}

// The boundary is a flag, and a malformed one is refused rather than silently reset.
func TestStatusHTMLDayStartIsAFlagAndMalformedIsRefused(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.in.DayStart = "half past two"
	_, errs, code := e.run(t)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "--day-start") {
		t.Errorf("the refusal does not name --day-start:\n%s", errs)
	}
	if got := strings.Count(strings.TrimSpace(errs), "\n"); got != 0 {
		t.Errorf("the refusal is %d lines, want one remedy line:\n%s", got+1, errs)
	}
}

// ---- gap 2: the merge queue row.

func TestStatusHTMLCarriesTheMergeQueueRow(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.repo(t, "mas-bandwidth/nova-tools")
	e.gh(t,
		fakeRule{Arg: 1, Equals: "pr", Stdout: "[]"},
		fakeRule{Arg: 2, Equals: "graphql", Stdout: `{"data":{"repository":{"mergeQueue":{"entries":{"nodes":[
			{"pullRequest":{"number":1},"state":"AWAITING_CHECKS"},
			{"pullRequest":{"number":2},"state":"QUEUED"},
			{"pullRequest":{"number":3},"state":"QUEUED"},
			{"pullRequest":{"number":4},"state":"UNMERGEABLE"}]}}}}}`},
	)
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	page := e.page(t)
	for _, want := range []string{"merge queue", "<td>4</td>", "running 1", "waiting 2", "unmergeable 1"} {
		if !strings.Contains(page, want) {
			t.Errorf("the merge queue row is missing %q:\n%s", want, page)
		}
	}
}

// ---- gap 3: the branch tip and its CI run.

func TestStatusHTMLCarriesTheBranchTipAndItsRun(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.repo(t, "mas-bandwidth/nova-tools")
	e.gh(t,
		fakeRule{Arg: 1, Equals: "pr", Stdout: "[]"},
		fakeRule{Arg: 1, Equals: "run", Stdout: `[{"status":"completed","conclusion":"success"}]`},
		fakeRule{Arg: 2, Equals: "repos/mas-bandwidth/nova-tools/commits/dev", Stdout: `{"sha":"220c05d7abcdef0123456789"}`},
	)
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	page := e.page(t)
	for _, want := range []string{"dev 220c05d7", "completed", "success"} {
		if !strings.Contains(page, want) {
			t.Errorf("the branch tip row is missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "220c05d7abcdef") {
		t.Errorf("the page carries the whole sha, not the short one:\n%s", page)
	}
}

// ---- gap 4: the Studio row.

// The host running the verb is a bench too. It drowned at load 147 on 2026-09-17 and no
// page showed it, because the page only ever read the machines it ssh'd to.
func TestStatusHTMLCarriesTheSelfRow(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	e.in.Self = "studio"
	e.self = &SelfReading{Name: "studio", CIRunners: 4, Cores: 20, Load: 147, FreeGB: 300, Orphans: 19}
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	page := e.page(t)
	for _, want := range []string{"<td>studio</td>", "ci=4", "<td>20</td>", "<td>147</td>", "300 GB", "orphans=19"} {
		if !strings.Contains(page, want) {
			t.Errorf("the self row is missing %q:\n%s", want, page)
		}
	}
}

// No --self is no row: the verb never guesses that the host it runs on is part of the
// fleet being reported.
func TestStatusHTMLWithoutSelfHasNoSelfRow(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	if strings.Contains(e.page(t), "orphans=") {
		t.Errorf("a self row printed with no --self:\n%s", e.page(t))
	}
}

// ---- gap 5: the loops line.

// The loops are counted by pattern from the flag, never by a name baked into the tool: a
// verb carrying `harvest-loop.sh` in its source would freeze the scripts it exists to
// retire.
func TestStatusHTMLCarriesTheLoopCounts(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	e.in.Self = "studio"
	e.in.Loops = []string{"harvest=harvest-loop", "fill=fill-loop"}
	e.self = &SelfReading{Name: "studio", Cores: 20, Loops: []LoopCount{{"harvest", 1}, {"fill", 2}}}
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	page := e.page(t)
	for _, want := range []string{"loops on studio", "harvest=1", "fill=2"} {
		if !strings.Contains(page, want) {
			t.Errorf("the loops line is missing %q:\n%s", want, page)
		}
	}
}

// A --loop with no label=pattern shape is refused: a pattern nobody can name is a count
// nobody can read.
func TestStatusHTMLMalformedLoopIsRefused(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.in.Self = "studio"
	e.in.Loops = []string{"harvest-loop"}
	_, errs, code := e.run(t)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "--loop") {
		t.Errorf("the refusal does not name --loop:\n%s", errs)
	}
	if got := strings.Count(strings.TrimSpace(errs), "\n"); got != 0 {
		t.Errorf("the refusal is %d lines, want one remedy line:\n%s", got+1, errs)
	}
}

// --loop without --self is refused: there is no host to count them on.
func TestStatusHTMLLoopWithoutSelfIsRefused(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.in.Loops = []string{"harvest=harvest-loop"}
	_, errs, code := e.run(t)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "--self") {
		t.Errorf("the refusal does not name --self:\n%s", errs)
	}
}

// ---- gap 6: hygiene actions in the last hour.

func TestStatusHTMLCarriesHygieneActions(t *testing.T) {
	e := newEdgeRun(t, "alpha", "beta")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	e.readings[0].Hygiene = 7
	e.readings[1].Hygiene = 5
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	page := e.page(t)
	for _, want := range []string{"hygiene actions, last hour", "<td>12</td>"} {
		if !strings.Contains(page, want) {
			t.Errorf("the hygiene row is missing %q:\n%s", want, page)
		}
	}
}

// The remote script counts them in the same round trip as the liveness numbers: a second
// ssh per bench per minute for one integer is the kind of waste the fleet audits for.
func TestFleetStatusScriptCountsHygieneInTheSameTrip(t *testing.T) {
	script := fleetStatusScript("/home/bench", "2026-09-18T11:00:00Z")
	for _, want := range []string{"hygiene.log", "delete-job", "delete-slot", "reap", "2026-09-18T11:00:00Z"} {
		if !strings.Contains(script, want) {
			t.Errorf("the liveness script does not count hygiene actions (%q missing):\n%s", want, script)
		}
	}
	// One numeric line per run. The home guard's own STATUSFLEET NOHOME is the other arm
	// of the same answer and never prints beside it.
	if n := strings.Count(script, `STATUSFLEET\t%s`); n != 1 {
		t.Errorf("the script prints %d numeric STATUSFLEET lines, want exactly 1", n)
	}
}

// ---- gap 11 again: the /proc fallback must not be quadratic.

// The old fallback read every pid's cwd once per slot: forty slots against a few thousand
// processes is a hundred thousand readlinks, which is why a live bench read DOWN at
// --timeout 10. The pid list is walked ONCE and matched against the slots.
func TestFleetStatusScriptWalksProcOnce(t *testing.T) {
	script := fleetStatusScript("/home/bench", "2026-09-18T11:00:00Z")
	if n := strings.Count(script, "/proc/$pid/cwd"); n != 1 {
		t.Errorf("the script reads /proc/<pid>/cwd from %d places, want exactly one pass:\n%s", n, script)
	}
	// The one pass must come BEFORE the per-slot loop starts: inside it, the cost is
	// slots x pids readlinks.
	walk := strings.Index(script, "/proc/$pid/cwd")
	slots := strings.Index(script, "for s in")
	if walk < 0 || slots < 0 {
		t.Fatalf("the script lost its pid walk or its slot loop:\n%s", script)
	}
	if walk > slots {
		t.Errorf("the pid walk runs inside the per-slot loop (slots x pids readlinks):\n%s", script)
	}
	// And the cap: an unbounded pid list on a busy bench is the same stall by another name.
	if !strings.Contains(script, "head -") {
		t.Errorf("the pid walk is unbounded:\n%s", script)
	}
}

// ---- gap 7: the verb ships nothing.

// The script scp'd both files to space:status/. A verb that writes a page nobody serves
// has not replaced it.
func TestStatusHTMLPublishesBothFiles(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	e.in.Publish = "space:status"
	stdout, errs, code := e.run(t)
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	if len(e.published) != 1 {
		t.Fatalf("published %d times, want 1", len(e.published))
	}
	got := e.published[0]
	if got.dest != "space:status" {
		t.Errorf("published to %q, want %q", got.dest, "space:status")
	}
	names := map[string]bool{}
	for _, f := range got.files {
		names[f.Name] = true
		if len(f.Body) == 0 {
			t.Errorf("%s was published empty", f.Name)
		}
	}
	for _, want := range []string{"index.html", "metrics.tsv"} {
		if !names[want] {
			t.Errorf("%s was not published: %v", want, names)
		}
	}
	if !strings.Contains(stdout, "published=space:status") {
		t.Errorf("the STATUS HTML line does not say where it published:\n%s", stdout)
	}
}

// Without --publish the verb ships nothing and says so, so a caller reading the line knows
// the page is local.
func TestStatusHTMLWithoutPublishShipsNothing(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	stdout, _, code := e.run(t)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if len(e.published) != 0 {
		t.Fatalf("published without being asked: %v", e.published)
	}
	if !strings.Contains(stdout, "published=-") {
		t.Errorf("the line does not say nothing was published:\n%s", stdout)
	}
}

// A --publish that is not host:dir is refused before the page is even read.
func TestStatusHTMLMalformedPublishIsRefused(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.in.Publish = "space"
	_, errs, code := e.run(t)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "--publish") {
		t.Errorf("the refusal does not name --publish:\n%s", errs)
	}
}

// A publish that fails is loud and the exit says so: a page that silently stopped shipping
// is a page that quietly goes stale while everybody reads it.
func TestStatusHTMLFailedPublishIsLoud(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	e.in.Publish = "space:status"
	var out, errs bytes.Buffer
	in := e.in
	in.Stdout, in.Stderr = &out, &errs
	in.Reader = func([]FleetBench, time.Time) []BenchReading { return e.readings }
	in.Ship = func(string, []PublishFile, time.Duration) error { return os.ErrPermission }
	code := StatusHTML(in)
	if code == 0 {
		t.Fatalf("a failed publish exited 0:\n%s", errs.String())
	}
	if !strings.Contains(errs.String(), "publish") {
		t.Errorf("the failure does not name the publish:\n%s", errs.String())
	}
	if _, err := os.Stat(filepath.Join(e.dir, "index.html")); err != nil {
		t.Errorf("the local page was not written before the publish was tried: %v", err)
	}
}

// ---- gap 12: GH_CONFIG_DIR.

// gh on this bench answers as whoever GH_CONFIG_DIR says. A verb that drops it runs as
// somebody else, or as nobody.
func TestGhEnvCarriesTheConfigDir(t *testing.T) {
	env := ghEnv([]string{"PATH=/usr/bin", "GH_CONFIG_DIR=/old"}, "/home/me/.config/gh-rowan")
	found := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "GH_CONFIG_DIR=") {
			found++
			if kv != "GH_CONFIG_DIR=/home/me/.config/gh-rowan" {
				t.Errorf("GH_CONFIG_DIR = %q, want the flag's value", kv)
			}
		}
	}
	if found != 1 {
		t.Fatalf("GH_CONFIG_DIR appears %d times, want exactly one", found)
	}
}

// No --gh-config leaves the caller's environment exactly as it is: the flag overrides, it
// never invents.
func TestGhEnvWithoutTheFlagIsTheCallersOwn(t *testing.T) {
	in := []string{"PATH=/usr/bin", "GH_CONFIG_DIR=/theirs"}
	got := ghEnv(in, "")
	if len(got) != len(in) {
		t.Fatalf("the environment changed with no --gh-config: %v", got)
	}
}

// ---- gap 10: --timeout accepts a duration.

func TestTimeoutFlagAcceptsSecondsAndDurations(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"120", 120 * time.Second},
		{"10", 10 * time.Second},
		{"90s", 90 * time.Second},
		{"2m", 2 * time.Minute},
		{"1m30s", 90 * time.Second},
	} {
		got, err := parseTimeout(c.in)
		if err != nil {
			t.Errorf("parseTimeout(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseTimeout(%q) = %s, want %s", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "0", "-5", "soon", "120x"} {
		if got, err := parseTimeout(bad); err == nil {
			t.Errorf("parseTimeout(%q) = %s, want a refusal", bad, got)
		}
	}
}

// The stderr progress line and the flag speak the same units, which is what made the
// bare-seconds flag a trap: the line said 10s and the flag wanted 10.
func TestStatusHTMLProgressLineSaysTheTimeout(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	e.in.Timeout = 90 * time.Second
	_, errs, code := e.run(t)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errs, "1m30s") {
		t.Errorf("the progress line does not say the timeout as a duration:\n%s", errs)
	}
}

// ---- the thirteenth gap, found by probing the real fleet.

// A --benches home that is not a directory on the bench -- a typo, a user renamed, a
// machine reinstalled -- made df print nothing and the row read "0 GB free, allowed 0" on
// every bench. That is a page saying the fleet is out of disk and out of capacity, from a
// bench that answered perfectly well. It is the dash-vs-zero rule a third time, so the
// bench says DOWN and the row says WHY.
func TestFleetStatusScriptRefusesAHomeItCannotRead(t *testing.T) {
	script := fleetStatusScript("/home/nobody", "2026-09-18T11:00:00Z")
	if !strings.Contains(script, "NOHOME") {
		t.Fatalf("the script does not say when the home is not there:\n%s", script)
	}
	if i, j := strings.Index(script, "NOHOME"), strings.Index(script, "for s in"); i < 0 || j < 0 || i > j {
		t.Errorf("the home check runs after the work it makes pointless:\n%s", script)
	}
}

func TestStatusHTMLNoHomeIsDownWithItsReason(t *testing.T) {
	vals, ok := parseFleetStatus("STATUSFLEET\tNOHOME\n")
	if ok {
		t.Fatalf("a NOHOME answer parsed as numbers: %v", vals)
	}
	e := newEdgeRun(t, "alpha")
	e.gh(t, fakeRule{Arg: 0, Stdout: "[]"})
	e.readings[0] = BenchReading{Name: "alpha", Down: true, Note: "home /home/nobody is not a directory"}
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	page := e.page(t)
	if !strings.Contains(page, "<b>DOWN</b>") || !strings.Contains(page, "home /home/nobody is not a directory") {
		t.Errorf("the row does not say why the bench is down:\n%s", page)
	}
}

// The CI run's verdict is two words -- "completed success", "completed cancelled" -- and a
// real page rendered it "completed\x20cancelled" because it went through the one-token
// escaper. Prose on the page is prose: one line, no markup, and readable.
func TestStatusHTMLRunVerdictReadsAsWords(t *testing.T) {
	e := newEdgeRun(t, "alpha")
	e.repo(t, "mas-bandwidth/nova-tools")
	e.gh(t,
		fakeRule{Arg: 1, Equals: "pr", Stdout: "[]"},
		fakeRule{Arg: 1, Equals: "run", Stdout: `[{"status":"completed","conclusion":"cancelled"}]`},
		fakeRule{Arg: 2, Equals: "repos/mas-bandwidth/nova-tools/commits/dev", Stdout: `{"sha":"220c05d7abcdef"}`},
	)
	if _, errs, code := e.run(t); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	page := e.page(t)
	if !strings.Contains(page, "its run: completed cancelled") {
		t.Errorf("the run verdict is not readable:\n%s", page)
	}
	if strings.Contains(page, `\x20`) {
		t.Errorf("the page escapes a space in its prose:\n%s", page)
	}
}
