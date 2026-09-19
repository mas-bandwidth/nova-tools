package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The working layout (SPEC-PULSE.md, "Harvest on the working layout"): a bench
// leaves jobs under <working>/tmp/<guid>-<label>/jobs/<label>, beside the swarm
// roots harvest already folds. Every path comes from a flag, the disposition is
// one typed class, and .harvested is the only state.

// jobClass is the one typed disposition of a job, never inferred from a log
// (rule 4, the typed decision behind the floor of card 8336).
type jobClass string

const (
	classFixed        jobClass = "fixed"
	classAlreadyFixed jobClass = "already-fixed"
	classNoChange     jobClass = "no-change"
	classFailed       jobClass = "failed"
	classOffBranch    jobClass = "off-branch"
)

// classFloor is the confidence a class decision must clear to stand alone; below
// it the caller keeps the mechanical disposition the rules spell out.
const classFloor = 0.5

// harvestJob is one job discovered on the working layout or an old swarm root.
type harvestJob struct {
	dir   string
	label string
}

// HarvestWorking folds the jobs a bench left on the working layout and, when
// --roots names them, the old swarm roots too: one HARVEST JOB line per job, one
// .harvested marker, one HARVEST OK summary. It never merges.
func HarvestWorking(in HarvestInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	started := in.Now()
	if in.Max < 0 {
		in.Max = 0
	}

	if strings.TrimSpace(in.Working) == "" && strings.TrimSpace(in.Roots) == "" {
		fmt.Fprintln(in.Stderr, "HARVEST REFUSED: refusing to guess (name --working or --roots)")
		return 2
	}
	if w := strings.TrimSpace(in.Working); w != "" {
		fi, err := os.Stat(w)
		if err != nil {
			fmt.Fprintf(in.Stderr, "HARVEST REFUSED working=%s: %s (fix the path or the permissions)\n",
				oneline.Field(w), oneline.Err(err))
			return 2
		}
		if !fi.IsDir() {
			fmt.Fprintf(in.Stderr, "HARVEST REFUSED working=%s: not a directory (fix the path or the permissions)\n",
				oneline.Field(w))
			return 2
		}
	}
	var since time.Time
	if s := strings.TrimSpace(in.SinceStamp); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			fmt.Fprintf(in.Stderr, "HARVEST REFUSED since=%s: not a timestamp (use RFC3339)\n", oneline.Field(s))
			return 2
		}
		since = t
	}
	if act := strings.TrimSpace(in.Timer); act != "" {
		if act != "install" {
			fmt.Fprintf(in.Stderr, "HARVEST REFUSED timer=%s (only install)\n", oneline.Field(act))
			return 2
		}
		if err := installHarvestTimer(in); err != nil {
			fmt.Fprintf(in.Stderr, "HARVEST REFUSED timer=install: %s (fix the unit path or the permissions)\n",
				oneline.Err(err))
			return 2
		}
	}

	jobs := discoverWorkingJobs(in.Working)
	jobs = append(jobs, discoverRootJobs(in.Roots)...)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].dir < jobs[j].dir })

	r := &workingRun{in: in, since: since, clock: started}
	r.base12 = r.resolveBase(jobs, in.Working)

	list := bounded.Capped(in.Stdout, in.Max, "HARVEST", "job", "--max <n>")
	counts := map[jobClass]int{}
	pushed, prs := 0, 0
	for _, j := range jobs {
		// .harvested is the marker and the only state: a job already carrying it
		// is skipped with no line and no read.
		if _, err := os.Stat(filepath.Join(j.dir, ".harvested")); err == nil {
			continue
		}
		out := r.one(j)
		counts[out.class]++
		pushed += out.pushed
		prs += out.prs
		if out.line != "" {
			list.Line(out.line)
		}
		markHarvestedLocal(j.dir)
	}
	list.More()

	took := in.Now().Sub(started).Round(time.Millisecond)
	ok := fmt.Sprintf("HARVEST OK jobs=%d fixed=%d already-fixed=%d no-change=%d off-branch=%d failed=%d pushed=%d prs=%d took=%s",
		total(counts), counts[classFixed], counts[classAlreadyFixed], counts[classNoChange],
		counts[classOffBranch], counts[classFailed], pushed, prs, took)
	fmt.Fprintln(in.Stdout, oneline.Escape(ok))
	if strings.TrimSpace(in.Working) != "" {
		appendHarvestLog(in.Working, ok)
	}
	if counts[classFailed] > 0 {
		return 1
	}
	return 0
}

func total(counts map[jobClass]int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

// workingRun carries the one-per-run values the job loop shares: the base ref
// read once, the session window, the clock and the PR list folded once.
type workingRun struct {
	in     HarvestInput
	base12 string
	since  time.Time
	clock  time.Time

	prs       []pullRequest
	prsLoaded bool
	prsErr    error
}

// pullRequest is the slice of an open or merged PR harvest matches on: the exact
// head ref and the head sha.
type pullRequest struct {
	Number  int    `json:"number"`
	Head    string `json:"headRefName"`
	HeadOID string `json:"headRefOid"`
	State   string `json:"state"`
	Title   string `json:"title"`
}

type workingOutcome struct {
	class  jobClass
	line   string
	pushed int
	prs    int
}

func (out workingOutcome) with(class jobClass) workingOutcome {
	out.class = class
	return out
}

// one disposes exactly one job by its own two lines and the typed class.
func (r *workingRun) one(j harvestJob) workingOutcome {
	clone := cloneDir(j.dir)
	body, err := os.ReadFile(filepath.Join(j.dir, "RESULT.md"))
	if err != nil {
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, "-", "-", "-")}
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	branch := resultToken(lines, "BRANCH ")
	repo := strings.TrimPrefix(resultToken(lines, "REPO "), "github.com/")
	took := tookMillis(r.clock, j.dir)

	logOut, err := runChild(clone, nil, "git", "log", "-1", "--format=%H %cI", r.base12+"..HEAD")
	if err != nil {
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, dash(branch), "-", "-")}
	}
	fields := strings.Fields(logOut)
	if len(fields) == 0 {
		return workingOutcome{class: classNoChange, line: fmt.Sprintf(
			"HARVEST JOB label=%s class=no-change base=%s branch=- pr=- commit=-",
			oneline.Field(j.label), oneline.Field(r.base12))}
	}
	commit := sha12(fields[0])
	commitAt := time.Time{}
	if len(fields) > 1 {
		commitAt, _ = time.Parse(time.RFC3339, fields[1])
	}
	// THE SAME RULE, THE SAME IMPLEMENTATION. This was a SECOND spelling of it -- a bare
	// `strings.HasPrefix(branch, "rowan/")` literal -- which happened to agree with the
	// local path's rule today and would have stopped agreeing the moment either was
	// edited. Johnny's hold on #1809 is about exactly that: one rule, one implementation,
	// every path. The classification an off-prefix job gets here is unchanged.
	if err := mustBranchPrefix(branch); err != nil {
		return workingOutcome{class: classOffBranch, line: fmt.Sprintf(
			"HARVEST JOB label=%s class=off-branch branch=%s reason=not-rowan",
			oneline.Field(j.label), oneline.Field(dash(branch)))}
	}
	if !r.since.IsZero() && !commitAt.IsZero() && commitAt.Before(r.since) {
		return workingOutcome{class: classOffBranch, line: fmt.Sprintf(
			"HARVEST JOB label=%s class=off-branch branch=%s reason=before-session",
			oneline.Field(j.label), oneline.Field(branch))}
	}
	if repo == "" {
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, "-", "-")}
	}

	prs, err := r.loadPRs(clone)
	if err != nil {
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, "-", commit)}
	}
	pr, found := findPR(prs, branch)
	if found && strings.EqualFold(pr.State, "MERGED") {
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, strconv.Itoa(pr.Number), commit)}
	}
	if found && (pr.HeadOID == fields[0] || (pr.HeadOID != "" && strings.HasPrefix(fields[0], pr.HeadOID))) {
		return workingOutcome{class: classAlreadyFixed, line: fmt.Sprintf(
			"HARVEST JOB label=%s class=already-fixed branch=%s pr=%d commit=%s base=%s took=%s",
			oneline.Field(j.label), oneline.Field(branch), pr.Number, oneline.Field(commit),
			oneline.Field(r.base12), took)}
	}

	// WHERE this force-pushes, and which repo its pull request is opened on, is the
	// resolver's answer and not the RESULT's claim. The line this replaced --
	// `url := "https://github.com/" + repo + ".git"` -- built the destination out of the
	// worker's own bytes, which is Johnny's HOLD of #1809 exactly. The working layout
	// carries no launch record (the job is all there is), so the clone's own origin must
	// answer or nothing does, and nothing does is a refusal. Resolved HERE, at the first
	// call that reaches the remote: a job already classified no-change or already-fixed
	// publishes nothing and needs no destination.
	dest, err := resolveDestination(j.label, clone, "", repo)
	if err != nil {
		fmt.Fprintln(r.in.Stderr, err)
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, "-", commit)}
	}
	url := dest.url

	remote := ""
	if out, err := runChild(clone, nil, "git", "ls-remote", url, "refs/heads/"+branch); err == nil {
		if f := strings.Fields(out); len(f) > 0 {
			remote = f[0]
		}
	} else {
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, "-", commit)}
	}
	// A lease that moved -- the live remote no longer holds the sha the PR
	// records -- is failed, not pushed.
	if found && remote != "" && pr.HeadOID != "" && remote != pr.HeadOID {
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, strconv.Itoa(pr.Number), commit)}
	}
	// `harvest --working` pushes with --force-with-lease, which makes the branch rule
	// MORE important here, not less: this is the path that can move a remote ref.
	if err := mustBranchPrefix(branch); err != nil {
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, "-", commit)}
	}
	// THE KEY-SHAPE SCAN COMES BEFORE THE PUSH (#1814): this path pushes the branch and
	// builds its PR body out of the same RESULT.md lines, so it passes the one guard every
	// publishing path passes. A hit refuses, quarantines the job beside its own jobs/
	// directory, writes the HUMAN line, and pushes nothing.
	findings, scanErr := secretFindings(j.dir, clone, r.base12+"..HEAD", lines)
	if scanErr != nil {
		fmt.Fprintln(r.in.Stderr, secretScanRefusalLine("harvest-working", j.label, scanErr))
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, "-", commit)}
	}
	if len(findings) > 0 {
		secretRefusal{Site: "harvest-working", Label: j.label, JobDir: j.dir,
			HumanDir: filepath.Dir(filepath.Dir(j.dir)), Out: r.in.Stderr}.refuse(findings)
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, "-", commit)}
	}
	if _, err := runChild(clone, nil, "git", "push", url, "refs/heads/"+branch,
		"--force-with-lease=refs/heads/"+branch+":"+remote); err != nil {
		return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, dash(strconv.Itoa(pr.Number)), commit)}
	}

	prNum := 0
	if found {
		if _, err := runChild(clone, nil, "gh", "pr", "edit", strconv.Itoa(pr.Number), "-R", dest.repo, "--body-file", "-"); err != nil {
			return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, strconv.Itoa(pr.Number), commit), pushed: 1}
		}
		prNum = pr.Number
	} else {
		out, err := runChild(clone, strings.NewReader(strings.Join(lines, "\n")), "gh", "pr", "create", "-R", dest.repo, "--draft", "--head", branch, "--title", j.label, "--body-file", "-")
		if err != nil {
			return workingOutcome{class: classFailed, line: r.jobLine(j, classFailed, branch, "-", commit), pushed: 1}
		}
		prNum = parsePRNumber(out)
	}
	return workingOutcome{class: classFixed, pushed: 1, prs: 1, line: fmt.Sprintf(
		"HARVEST JOB label=%s class=fixed branch=%s pr=%d commit=%s base=%s took=%s",
		oneline.Field(j.label), oneline.Field(branch), prNum, oneline.Field(commit),
		oneline.Field(r.base12), took)}
}

func (r *workingRun) jobLine(j harvestJob, class jobClass, branch, pr, commit string) string {
	return fmt.Sprintf("HARVEST JOB label=%s class=%s branch=%s pr=%s commit=%s base=%s took=%s",
		oneline.Field(j.label), class, oneline.Field(branch), oneline.Field(pr),
		oneline.Field(commit), oneline.Field(r.base12), tookMillis(r.clock, j.dir))
}

// loadPRs folds the hosting CLI's PRs once per run; every job matches against it.
func (r *workingRun) loadPRs(clone string) ([]pullRequest, error) {
	if r.prsLoaded {
		return r.prs, r.prsErr
	}
	r.prsLoaded = true
	out, err := runChild(clone, nil, "gh", "pr", "list", "--state", "all",
		"--json", "number,headRefName,headRefOid,state,title", "--limit", "200")
	if err != nil {
		r.prsErr = err
		return nil, err
	}
	var list []pullRequest
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &list); err != nil {
		r.prsErr = err
		return nil, err
	}
	r.prs = list
	return list, nil
}

// findPR is the exact head-branch match: a similar title is never a match.
func findPR(prs []pullRequest, branch string) (pullRequest, bool) {
	for _, p := range prs {
		if p.Head == branch {
			return p, true
		}
	}
	return pullRequest{}, false
}

// resolveBase reads the base ref once per run: a sha comes back as its sha12, a
// branch or tag is resolved through git, and the flag's own text is the fallback.
func (r *workingRun) resolveBase(jobs []harvestJob, working string) string {
	base := strings.TrimSpace(r.in.Base)
	if base == "" {
		base = "HEAD"
	}
	dir := working
	if len(jobs) > 0 {
		dir = cloneDir(jobs[0].dir)
	}
	if dir != "" {
		if out, err := runChild(dir, nil, "git", "rev-parse", base); err == nil {
			if f := strings.Fields(out); len(f) > 0 && isHex(f[0]) {
				return sha12(f[0])
			}
		}
	}
	return sha12(base)
}

// discoverWorkingJobs reads <working>/tmp/<guid>-<label>/jobs/<label>.
func discoverWorkingJobs(working string) []harvestJob {
	if strings.TrimSpace(working) == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(working, "tmp"))
	if err != nil {
		return nil
	}
	var out []harvestJob
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, jobsUnder(filepath.Join(working, "tmp", e.Name(), "jobs"))...)
		}
	}
	return out
}

// discoverRootJobs reads each old root's <root>/<slot>/jobs/<label>.
func discoverRootJobs(roots string) []harvestJob {
	var out []harvestJob
	for _, root := range strings.Split(roots, ",") {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		slots, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, s := range slots {
			if s.IsDir() {
				out = append(out, jobsUnder(filepath.Join(root, s.Name(), "jobs"))...)
			}
		}
	}
	return out
}

func jobsUnder(jobsDir string) []harvestJob {
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return nil
	}
	var out []harvestJob
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, harvestJob{dir: filepath.Join(jobsDir, e.Name()), label: e.Name()})
		}
	}
	return out
}

// cloneDir is where the job's clone lives: ./repo when the bench made one, else
// the job directory itself.
func cloneDir(jobDir string) string {
	sub := filepath.Join(jobDir, "repo")
	if fi, err := os.Stat(sub); err == nil && fi.IsDir() {
		return sub
	}
	return jobDir
}

// markHarvestedLocal writes the one empty marker beside a job in THIS filesystem,
// atomically; an existing marker is left untouched. harvestbench.go has the bench-shell
// twin that touches the same marker over ssh.
func markHarvestedLocal(jobDir string) {
	path := filepath.Join(jobDir, ".harvested")
	if _, err := os.Stat(path); err == nil {
		return
	}
	tmp, err := os.CreateTemp(jobDir, ".harvested-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	tmp.Close()
	_ = os.Rename(name, path)
}

// appendHarvestLog appends one line per run to <working>/harvest.log (rule 9).
func appendHarvestLog(working, line string) {
	f, err := os.OpenFile(filepath.Join(working, "harvest.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, oneline.Escape(line))
}

// installHarvestTimer gives the harvest a clock the bench owns: the two systemd
// user units carrying the harvest's own flags, then daemon-reload and enable.
//
// SYSTEMD IS LINUX'S, so this refuses anywhere else by name instead of leaving
// two files nothing will ever read in somebody's home. On windows it was worse
// than useless: os.UserHomeDir there is %USERPROFILE%, so the units landed in
// the real profile of whoever ran it.
func installHarvestTimer(in HarvestInput) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("the harvest timer is systemd's and systemd is linux's; this bench is %s, so run the harvest from its own scheduler", runtime.GOOS)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	flags := harvestFlags(in)
	service := "[Unit]\nDescription=nova-pulse harvest on a timer the bench owns\n\n" +
		"[Service]\nType=oneshot\nExecStart=nova-pulse harvest " + flags + "\n"
	timer := "[Unit]\nDescription=nova-pulse harvest timer\n\n" +
		"[Timer]\nOnBootSec=1min\nOnUnitActiveSec=5min\n\n" +
		"[Install]\nWantedBy=timers.target\n\n" +
		"# flags: nova-pulse harvest " + flags + "\n"
	if err := os.WriteFile(filepath.Join(unitDir, "nova-pulse-harvest.service"), []byte(service), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(unitDir, "nova-pulse-harvest.timer"), []byte(timer), 0o644); err != nil {
		return err
	}
	if _, err := runChild("", nil, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if _, err := runChild("", nil, "systemctl", "--user", "enable", "--now", "nova-pulse-harvest.timer"); err != nil {
		return err
	}
	return nil
}

func harvestFlags(in HarvestInput) string {
	var parts []string
	if v := strings.TrimSpace(in.Working); v != "" {
		parts = append(parts, "--working", v)
	}
	if v := strings.TrimSpace(in.Roots); v != "" {
		parts = append(parts, "--roots", v)
	}
	if v := strings.TrimSpace(in.Base); v != "" {
		parts = append(parts, "--base", v)
	}
	if v := strings.TrimSpace(in.SinceStamp); v != "" {
		parts = append(parts, "--since", v)
	}
	parts = append(parts, "--max", strconv.Itoa(in.Max))
	return strings.Join(parts, " ")
}

// runChild runs one child (git, gh, systemctl) under the shared timeout with the
// caller's stdin and working directory.
func runChild(dir string, stdin io.Reader, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %s", oneline.Err(err), oneline.Cap(strings.TrimSpace(string(out)), 200))
	}
	return string(out), nil
}

func resultToken(lines []string, prefix string) string {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(t, prefix))
		}
	}
	return ""
}

func tookMillis(now time.Time, jobDir string) string {
	fi, err := os.Stat(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		return "0"
	}
	ms := now.Sub(fi.ModTime()).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return strconv.FormatInt(ms, 10)
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
