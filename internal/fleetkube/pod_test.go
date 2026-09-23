package fleetkube

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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

// The Job's container command carries the same order: the harness under nova-secrets exec,
// then nova-work clip as the final command.
func TestJobCommandEndsWithTheClip(t *testing.T) {
	cmd := JobCommand(Pod{Label: "card-9", Attempt: 1, Worktree: "/w", Branch: "b", Base: "dev", JobDir: "/j"}, "nova-secrets exec --only GH_TOKEN -- nova-swarm native --label card-9")
	if len(cmd) == 0 {
		t.Fatal("JobCommand is empty")
	}
	last := cmd[len(cmd)-1]
	if !strings.HasPrefix(last, "nova-work clip ") || !strings.Contains(last, "--harvest") {
		t.Fatalf("last command = %q, want nova-work clip with --harvest", last)
	}
	for _, c := range cmd[:len(cmd)-1] {
		if strings.Contains(c, "clip") {
			t.Fatalf("a clip ran before the last step: %q", cmd)
		}
	}
}
