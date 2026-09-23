package fleetkube

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// gitT runs git in dir with a fixed identity, never the network: the remote is a bare
// repository on disk.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtureWorktree is a kept worktree on base dev, with the card's branch checked out,
// cloned from a bare remote on disk.
func fixtureWorktree(t *testing.T, root, branch string) (wt, baseSHA string) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	remote := filepath.Join(root, "remote.git")
	gitT(t, root, "init", "--bare", "-q", remote)
	seed := filepath.Join(root, "seed")
	gitT(t, root, "clone", "-q", remote, seed)
	gitT(t, seed, "checkout", "-q", "-b", "dev")
	mustWrite(t, filepath.Join(seed, "base.txt"), "base\n")
	gitT(t, seed, "add", "-A")
	gitT(t, seed, "commit", "-q", "-m", "base")
	gitT(t, seed, "push", "-q", "origin", "dev")
	wt = filepath.Join(root, "worktrees", "acme", "nova-tools")
	gitT(t, root, "clone", "-q", "-b", "dev", remote, wt)
	gitT(t, wt, "checkout", "-q", "-b", branch)
	return wt, gitT(t, wt, "rev-parse", "HEAD")
}

// fixedClock hands out the given instants in order, so start and end are facts the test
// knows rather than readings of the wall clock.
func fixedClock(ts ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		t := ts[i]
		if i < len(ts)-1 {
			i++
		}
		return t
	}
}

// readTSV splits a usage.tsv into its header and rows.
func readTSV(t *testing.T, path string) (head []string, rows []map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("usage.tsv: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	head = strings.Split(lines[0], "\t")
	for _, l := range lines[1:] {
		cells := strings.Split(l, "\t")
		if len(cells) != len(head) {
			t.Fatalf("usage.tsv row has %d cells, header %d: %q", len(cells), len(head), l)
		}
		row := map[string]string{}
		for i, c := range head {
			row[c] = cells[i]
		}
		rows = append(rows, row)
	}
	return head, rows
}

// a-job-with-the-fixture-card-produces-RESULT-md-and-a-usage-tsv (SPEC-FLEET-KUBE
// behaviour 34): the fixture card run as a pod lands a RESULT.md whose line 1 is the
// contract line and exactly one usage.tsv row carrying the pod's own start, end, exit and
// cost, in the launcher's schema (swarm.CardUsageColumns), so nova-swarm result and
// nova-pulse progress read it unchanged. The pod's stdout is the harness log and is never
// written into usage.tsv.
func TestAJobWithTheFixtureCardProducesRESULTmdAndAUsageTsv(t *testing.T) {
	root := t.TempDir()
	wt, _ := fixtureWorktree(t, root, "rowan/card-2227")
	jobDir := filepath.Join(root, "shared", "jobs", "card-2227")
	start := time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)
	end := start.Add(7 * time.Minute)
	var stdout bytes.Buffer

	rep, err := RunPod(Pod{
		Label: "card-2227", Attempt: 1,
		Worktree: wt, Branch: "rowan/card-2227", Base: "dev",
		JobDir: jobDir, Provider: "deepseek", Model: "deepseek-v4",
		Stdout: &stdout, Now: fixedClock(start, end),
		Harness: func(h HarnessRun) (int, swarm.ProviderUsage) {
			h.Stdout.Write([]byte("harness: the card's raw log line\n"))
			mustWrite(t, filepath.Join(h.Worktree, "work.txt"), "the card's work\n")
			mustWrite(t, filepath.Join(h.Worktree, "RESULT.md"), "RESULT: card-2227 sha=0123456789ab\nDONE\n")
			return 0, swarm.ProviderUsage{Observed: true, Values: map[string]string{
				"tokens_in": "1200", "tokens_out": "340", "cache_write": "-", "cache_read": "9000",
				"reasoning": "-", "usd": "0.0421"}}
		},
	})
	if err != nil {
		t.Fatalf("RunPod: %v", err)
	}
	if rep.RC != 0 {
		t.Fatalf("rc = %d, want 0", rep.RC)
	}

	result, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("RESULT.md was not harvested into the job dir: %v", err)
	}
	line1, _, _ := strings.Cut(string(result), "\n")
	if !swarm.IsCardContractLine(line1) {
		t.Fatalf("RESULT.md line 1 = %q, want the contract line", line1)
	}

	head, rows := readTSV(t, filepath.Join(jobDir, "usage.tsv"))
	if strings.Join(head, "\t") != strings.Join(swarm.CardUsageColumns, "\t") {
		t.Fatalf("usage.tsv header = %q, want the launcher's schema %q", head, swarm.CardUsageColumns)
	}
	if len(rows) != 1 {
		t.Fatalf("usage.tsv has %d rows, want exactly one", len(rows))
	}
	row := rows[0]
	want := map[string]string{
		"job": "card-2227", "attempt": "1",
		"started": start.Format(time.RFC3339), "ended": end.Format(time.RFC3339),
		"rc": "0", "provider": "deepseek", "model": "deepseek-v4",
		"tokens_in": "1200", "tokens_out": "340", "cache_read": "9000", "cache_write": "-",
		"usd": "0.0421",
	}
	for k, v := range want {
		if row[k] != v {
			t.Errorf("usage.tsv %s = %q, want %q", k, row[k], v)
		}
	}
	if !strings.Contains(stdout.String(), "the card's raw log line") {
		t.Fatalf("the harness log did not go to the pod's stdout: %q", stdout.String())
	}
	raw, _ := os.ReadFile(filepath.Join(jobDir, "usage.tsv"))
	if strings.Contains(string(raw), "raw log line") {
		t.Fatalf("the pod's stdout leaked into usage.tsv: %q", raw)
	}
}

// The same usage.tsv the launcher created: a pod appends its row to the file a native
// attempt already wrote, under the one header, so a reader folds both attempts from one
// schema and one file.
func TestAPodAppendsToTheLauncherUsageTsv(t *testing.T) {
	root := t.TempDir()
	wt, _ := fixtureWorktree(t, root, "rowan/card-7")
	jobDir := filepath.Join(root, "jobs", "card-7")
	usage := filepath.Join(jobDir, "usage.tsv")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swarm.AppendCardUsage(usage, swarm.UsageRow{"job": "card-7", "attempt": "1", "rc": "1"}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC)
	_, err := RunPod(Pod{
		Label: "card-7", Attempt: 2, Worktree: wt, Branch: "rowan/card-7", Base: "dev",
		JobDir: jobDir, Now: fixedClock(start, start.Add(time.Minute)),
		Harness: func(h HarnessRun) (int, swarm.ProviderUsage) { return 3, swarm.ProviderUsage{} },
	})
	if err != nil {
		t.Fatalf("RunPod: %v", err)
	}
	head, rows := readTSV(t, usage)
	if strings.Join(head, "\t") != strings.Join(swarm.CardUsageColumns, "\t") {
		t.Fatalf("header = %q, want the launcher's", head)
	}
	if len(rows) != 2 || rows[1]["attempt"] != "2" || rows[1]["rc"] != "3" || rows[1]["usd"] != swarm.Dash {
		t.Fatalf("rows = %v, want the launcher's row then the pod's attempt=2 rc=3 usd=-", rows)
	}
}

// TestClipIsThePodLastStep (SPEC-FLEET-KUBE behaviour 35): after each card the pod's last
// step is the clip -- commit the card's branch, harvest its RESULT.md, reset the kept
// worktree to base -- exactly SPEC-JOBS section 6. Nothing runs after it, and the next
// card on the kept worktree sees base, never this card's diff. A failing card is clipped
// too.
func TestClipIsThePodLastStep(t *testing.T) {
	for _, rc := range []int{0, 2} {
		root := t.TempDir()
		wt, baseSHA := fixtureWorktree(t, root, "rowan/card-35")
		jobDir := filepath.Join(root, "jobs", "card-35")
		var steps []string
		rep, err := RunPod(Pod{
			Label: "card-35", Attempt: 1, Worktree: wt, Branch: "rowan/card-35", Base: "dev",
			JobDir: jobDir, OnStep: func(s string) { steps = append(steps, s) },
			Harness: func(h HarnessRun) (int, swarm.ProviderUsage) {
				mustWrite(t, filepath.Join(h.Worktree, "work.txt"), "uncommitted\n")
				mustWrite(t, filepath.Join(h.Worktree, "RESULT.md"), "RESULT: card-35 sha=0123456789ab\n")
				return rc, swarm.ProviderUsage{}
			},
		})
		if err != nil {
			t.Fatalf("rc=%d RunPod: %v", rc, err)
		}
		if got := strings.Join(steps, ","); got != "harness,usage,clip" {
			t.Fatalf("rc=%d steps = %q, want harness,usage,clip", rc, got)
		}
		if got := strings.Join(rep.Steps, ","); got != "harness,usage,clip" || PodSteps[len(PodSteps)-1] != StepClip {
			t.Fatalf("rc=%d report steps = %q, declared %v; the clip must be last", rc, got, PodSteps)
		}
		// The branch holds the card's work, committed.
		if files := gitT(t, wt, "show", "--name-only", "--format=", "rowan/card-35"); !strings.Contains(files, "work.txt") {
			t.Fatalf("rc=%d the card's branch did not commit its work: %q", rc, files)
		}
		// The kept worktree is back on base, clean.
		if head := gitT(t, wt, "rev-parse", "HEAD"); head != baseSHA {
			t.Fatalf("rc=%d worktree HEAD = %s, want base %s", rc, head, baseSHA)
		}
		if st := gitT(t, wt, "status", "--porcelain"); st != "" {
			t.Fatalf("rc=%d worktree is not clean after the clip: %q", rc, st)
		}
		if _, err := os.Stat(filepath.Join(jobDir, "RESULT.md")); err != nil {
			t.Fatalf("rc=%d RESULT.md was not harvested: %v", rc, err)
		}
		if rep.Clip.Commit == "" || rep.Clip.Base != "dev" {
			t.Fatalf("rc=%d clip = %+v, want a commit and base dev", rc, rep.Clip)
		}
	}
}

// fakeNovaWorkEnv, when set in the environment, makes this test binary answer as
// `nova-work` for the Job command under test: it takes the clip verb's flags, notes in the
// order log (the variable's value) how many usage.tsv lines exist when the clip starts,
// then runs swarm.Clip -- what cmd/nova-work clip runs. An unknown flag is refused, so a
// Job command the real verb would reject fails here too.
const fakeNovaWorkEnv = "FLEETKUBE_TEST_AS_NOVA_WORK"

func TestMain(m *testing.M) {
	if order := os.Getenv(fakeNovaWorkEnv); order != "" {
		os.Exit(fakeNovaWork(order, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeNovaWork(order string, args []string) int {
	if len(args) == 0 || args[0] != "clip" {
		fmt.Fprintf(os.Stderr, "fake nova-work: want the clip verb, got %q\n", args)
		return 2
	}
	fs := flag.NewFlagSet("clip", flag.ContinueOnError)
	worktree := fs.String("worktree", "", "")
	branch := fs.String("branch", "", "")
	base := fs.String("base", "", "")
	message := fs.String("message", "", "")
	result := fs.String("result", "", "")
	harvest := fs.String("harvest", "", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "fake nova-work clip: %v %q\n", err, fs.Args())
		return 2
	}
	raw, _ := os.ReadFile(filepath.Join(*harvest, "usage.tsv"))
	f, err := os.OpenFile(order, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return 1
	}
	fmt.Fprintf(f, "clip usage-lines=%d\n", strings.Count(string(raw), "\n"))
	f.Close()
	if _, err := swarm.Clip(swarm.ClipRequest{Worktree: *worktree, Branch: *branch, Base: *base,
		Message: *message, Result: *result, Harvest: *harvest}); err != nil {
		fmt.Fprintf(os.Stderr, "fake nova-work clip: %v\n", err)
		return 1
	}
	return 0
}

// TestJobCommandRunsHarnessUsageThenClip (the hold on #3234 at 8e54dee3): the Job's
// container command is an argv Kubernetes can exec -- one executable, then its arguments --
// and executing it against the fixture card runs the harness (its argv untouched, its log on
// stdout), appends the pod's usage row, and runs nova-work clip last, for a card that exits
// 0 and one that exits 3; the container exits with the harness's code.
func TestJobCommandRunsHarnessUsageThenClip(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, rc := range []int{0, 3} {
		root := t.TempDir()
		wt, baseSHA := fixtureWorktree(t, root, "rowan/card-2227")
		jobDir := filepath.Join(root, "shared", "jobs", "card-2227")
		order := filepath.Join(root, "order.log")
		bin := filepath.Join(root, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(self, filepath.Join(bin, "nova-work")); err != nil {
			t.Fatal(err)
		}
		// The harness, as the caller builds it: one argv whose words carry spaces and quotes
		// the shell must not re-split.
		harness := []string{"/bin/sh", "-c",
			`cd "$1" && echo "harness: the card's raw log line" && ` +
				`printf 'RESULT: card-2227 sha=0123456789ab\nDONE\n' > RESULT.md && ` +
				`echo "the card's work" > work.txt && echo harness >> "$2" && exit "$3"`,
			"fixture-harness", wt, order, strconv.Itoa(rc)}
		cmd := JobCommand(Pod{Label: "card-2227", Attempt: 1, Worktree: wt, Branch: "rowan/card-2227",
			Base: "dev", JobDir: jobDir, Provider: "deepseek", Model: "deepseek-v4"}, harness)
		if cmd[0] != JobShell || !filepath.IsAbs(cmd[0]) {
			t.Fatalf("command[0] = %q, want the executable %s", cmd[0], JobShell)
		}
		if _, err := os.Stat(cmd[0]); err != nil {
			t.Fatalf("command[0] %q is not an executable on this host: %v", cmd[0], err)
		}

		run := exec.Command(cmd[0], cmd[1:]...)
		run.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), fakeNovaWorkEnv+"="+order)
		var stdout, stderr bytes.Buffer
		run.Stdout, run.Stderr = &stdout, &stderr
		err := run.Run()
		exit := 0
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("rc=%d the Job command did not run: %v", rc, err)
		}
		if exit != rc {
			t.Fatalf("rc=%d the container exited %d, want the harness's %d; stderr %q", rc, exit, rc, stderr.String())
		}
		if !strings.Contains(stdout.String(), "harness: the card's raw log line") {
			t.Fatalf("rc=%d the harness log is not on the pod's stdout: %q", rc, stdout.String())
		}

		// In order: the harness, then the clip, and the usage row (header + one row) was
		// already in usage.tsv when the clip started.
		got, err := os.ReadFile(order)
		if err != nil {
			t.Fatalf("rc=%d order log: %v", rc, err)
		}
		if string(got) != "harness\nclip usage-lines=2\n" {
			t.Fatalf("rc=%d order = %q, want harness, then the clip after the usage row", rc, got)
		}

		head, rows := readTSV(t, filepath.Join(jobDir, "usage.tsv"))
		if strings.Join(head, "\t") != strings.Join(swarm.CardUsageColumns, "\t") {
			t.Fatalf("rc=%d header = %q, want swarm.CardUsageColumns", rc, head)
		}
		if len(rows) != 1 {
			t.Fatalf("rc=%d usage rows = %v, want one", rc, rows)
		}
		row := rows[0]
		if row["job"] != "card-2227" || row["attempt"] != "1" || row["rc"] != strconv.Itoa(rc) ||
			row["provider"] != "deepseek" || row["model"] != "deepseek-v4" || row["usd"] != swarm.Dash {
			t.Fatalf("rc=%d usage row = %v", rc, row)
		}
		started, err1 := time.Parse(time.RFC3339, row["started"])
		ended, err2 := time.Parse(time.RFC3339, row["ended"])
		if err1 != nil || err2 != nil || ended.Before(started) {
			t.Fatalf("rc=%d started=%q ended=%q, want RFC3339 with ended >= started", rc, row["started"], row["ended"])
		}
		raw, _ := os.ReadFile(filepath.Join(jobDir, "usage.tsv"))
		if strings.Contains(string(raw), "raw log") {
			t.Fatalf("rc=%d the harness log leaked into usage.tsv: %q", rc, raw)
		}

		// The clip ran: branch committed, worktree back at base and clean, RESULT.md harvested.
		if files := gitT(t, wt, "show", "--name-only", "--format=", "rowan/card-2227"); !strings.Contains(files, "work.txt") {
			t.Fatalf("rc=%d the card's branch did not commit its work: %q", rc, files)
		}
		if h := gitT(t, wt, "rev-parse", "HEAD"); h != baseSHA {
			t.Fatalf("rc=%d worktree HEAD = %s, want base %s", rc, h, baseSHA)
		}
		if st := gitT(t, wt, "status", "--porcelain"); st != "" {
			t.Fatalf("rc=%d worktree is not clean after the clip: %q", rc, st)
		}
		result, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
		if err != nil || !strings.HasPrefix(string(result), "RESULT: card-2227 ") {
			t.Fatalf("rc=%d RESULT.md was not harvested: %v %q", rc, err, result)
		}
	}
}
