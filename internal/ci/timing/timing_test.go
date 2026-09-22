package timing

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// TestTimingTableReproduces runs the committed script the way its reader runs
// it -- go run cmd/nova-ci/timing.go over the committed harvest -- twice, and
// holds each run's stdout byte-for-byte against the table committed beside
// the harvest, and the two runs against each other. Re-running the script
// must reproduce the committed table; that is the whole point of the
// measurement, so it is the check.
func TestTimingTableReproduces(t *testing.T) {
	pkg, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(pkg))) // internal/ci/timing -> the checkout
	script := filepath.Join(root, "cmd", "nova-ci", "timing.go")
	log := filepath.Join(pkg, "testdata", "events.jsonl")
	golden, err := os.ReadFile(filepath.Join(pkg, "testdata", "table.tsv"))
	if err != nil {
		t.Fatalf("the committed table is missing; run the script once and commit its output: %v", err)
	}
	for run := 1; run <= 2; run++ {
		cmd := exec.Command("go", "run", script, "--log", log)
		cmd.Env = goenv.Clean(os.Environ())
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("run %d: the script did not print the table: %v\n%s", run, err, stderr.String())
		}
		if string(out) != string(golden) {
			t.Fatalf("run %d did not reproduce the committed table:\n got %q\nwant %q", run, out, golden)
		}
	}
}

// TestTimingRefusesABadHarvest holds Load to the refusals a bad harvest
// earns, each naming its line, so a one-line fix stays one line.
func TestTimingRefusesABadHarvest(t *testing.T) {
	good := `{"repo":"mas-bandwidth/nova-tools","pr":1404,"job":"test (1/3)","opened":"2026-09-01T08:00:00Z","queued":"2026-09-01T08:01:30Z","started":"2026-09-01T08:02:15Z","setup_done":"2026-09-01T08:04:15Z","done":"2026-09-01T08:24:15Z","green":true}`
	for _, tc := range []struct {
		name, line, want string
	}{
		{
			name: "not json",
			line: `{"repo":`,
			want: "line 1 is not one JSON object",
		},
		{
			name: "repo not owner/name",
			line: strings.Replace(good, `"repo":"mas-bandwidth/nova-tools"`, `"repo":"nova-tools"`, 1),
			want: `line 1: repo wants the repository as <owner>/<name> (got "nova-tools")`,
		},
		{
			name: "green not stated",
			line: strings.Replace(good, `,"green":true`, ``, 1),
			want: "line 1: green must be stated, true or false",
		},
		{
			name: "time not RFC 3339 UTC",
			line: strings.Replace(good, "2026-09-01T08:01:30Z", "2026-09-01 08:01:30", 1),
			want: "line 1: queued wants an RFC 3339 time in UTC",
		},
		{
			name: "clock runs backwards",
			line: strings.Replace(good, "2026-09-01T08:02:15Z", "2026-09-01T08:01:00Z", 1),
			want: "line 1: the clock runs backwards: started before it was queued",
		},
		{
			name: "pr not a number above zero",
			line: strings.Replace(good, `"pr":1404`, `"pr":0`, 1),
			want: "line 1: pr wants the pull request's number",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(strings.NewReader(tc.line + "\n"))
			if err == nil {
				t.Fatalf("Load took %q, want a refusal naming %q", tc.line, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal says %q, want it to say %q", err, tc.want)
			}
		})
	}
	t.Run("a job named twice", func(t *testing.T) {
		_, err := Load(strings.NewReader(good + "\n" + good + "\n"))
		if err == nil {
			t.Fatal("Load took the same job twice, want a refusal")
		}
		if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "appears twice") {
			t.Errorf("the refusal says %q, want the second line and the repeat named", err)
		}
	})
	t.Run("a blank line is skipped", func(t *testing.T) {
		events, err := Load(strings.NewReader("\n" + good + "\n\n"))
		if err != nil {
			t.Fatalf("Load refused a harvest with blank lines: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("Load kept %d events, want 1", len(events))
		}
	})
}

// TestTimingSelectAndRender holds the selection to whole pull requests and
// the render to the table's shape: sorted by repo, PR and job, spans in whole
// seconds, and "-" where a PR never went all-green.
func TestTimingSelectAndRender(t *testing.T) {
	job := func(repo string, pr int, name, opened, queued, started, setup, done string, green bool) Event {
		return Event{Repo: repo, PR: pr, Job: name, Opened: opened, Queued: queued,
			Started: started, SetupDone: setup, Done: done, Green: greenPtr(green)}
	}
	events := []Event{
		job("mas-bandwidth/schema", 700, "test", "2026-08-01T05:00:00Z", "2026-08-01T05:01:00Z", "2026-08-01T05:02:00Z", "2026-08-01T05:03:00Z", "2026-08-01T05:23:00Z", true),
		job("mas-bandwidth/schema", 699, "check", "2026-08-01T04:00:00Z", "2026-08-01T04:01:00Z", "2026-08-01T04:02:00Z", "2026-08-01T04:03:00Z", "2026-08-01T04:13:00Z", true),
		job("mas-bandwidth/schema", 699, "test", "2026-08-01T04:00:00Z", "2026-08-01T04:01:00Z", "2026-08-01T04:02:00Z", "2026-08-01T04:03:00Z", "2026-08-01T04:43:00Z", false),
		job("mas-bandwidth/nova-tools", 1404, "test (2/3)", "2026-08-01T06:00:00Z", "2026-08-01T06:01:00Z", "2026-08-01T06:03:00Z", "2026-08-01T06:05:30Z", "2026-08-01T06:35:30Z", true),
		job("mas-bandwidth/nova-tools", 1404, "test (1/3)", "2026-08-01T06:00:00Z", "2026-08-01T06:01:00Z", "2026-08-01T06:02:00Z", "2026-08-01T06:04:00Z", "2026-08-01T06:24:00Z", true),
		job("mas-bandwidth/other", 1, "test", "2026-08-01T07:00:00Z", "2026-08-01T07:01:00Z", "2026-08-01T07:02:00Z", "2026-08-01T07:03:00Z", "2026-08-01T07:13:00Z", true),
	}
	t.Run("a PR is selected whole", func(t *testing.T) {
		kept := Select(events, DefaultRepos, 1)
		got := map[int]int{}
		for _, e := range kept {
			got[e.PR]++
		}
		if len(kept) != 3 || got[1404] != 2 || got[700] != 1 {
			t.Fatalf("Select kept %+v, want both jobs of nova-tools PR 1404 and the whole of schema PR 700", kept)
		}
	})
	t.Run("last of zero or less keeps everything", func(t *testing.T) {
		if kept := Select(events, DefaultRepos, 0); len(kept) != 5 {
			t.Fatalf("Select with last=0 kept %d events, want 5 (the other repo dropped)", len(kept))
		}
	})
	t.Run("the table's shape", func(t *testing.T) {
		rows, err := Rows(Select(events, DefaultRepos, 0))
		if err != nil {
			t.Fatal(err)
		}
		want := "repo\tpr\tjob\topened\tqueue_s\tsetup_s\ttest_s\tpr_open_to_green_s\n" +
			"mas-bandwidth/nova-tools\t1404\ttest (1/3)\t2026-08-01T06:00:00Z\t60\t120\t1200\t2130\n" +
			"mas-bandwidth/nova-tools\t1404\ttest (2/3)\t2026-08-01T06:00:00Z\t120\t150\t1800\t2130\n" +
			"mas-bandwidth/schema\t699\tcheck\t2026-08-01T04:00:00Z\t60\t60\t600\t-\n" +
			"mas-bandwidth/schema\t699\ttest\t2026-08-01T04:00:00Z\t60\t60\t2400\t-\n" +
			"mas-bandwidth/schema\t700\ttest\t2026-08-01T05:00:00Z\t60\t60\t1200\t1380\n"
		if got := Render(rows); got != want {
			t.Errorf("Render printed:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("an event that does not say green", func(t *testing.T) {
		bad := job("mas-bandwidth/nova-tools", 1, "test", "2026-08-01T06:00:00Z", "2026-08-01T06:01:00Z", "2026-08-01T06:02:00Z", "2026-08-01T06:04:00Z", "2026-08-01T06:24:00Z", true)
		bad.Green = nil
		if _, err := Rows([]Event{bad}); err == nil {
			t.Fatal("Rows took an event that does not say whether it was green, want a refusal")
		}
	})
}

// greenPtr is the *bool a canned Event states its verdict with.
func greenPtr(b bool) *bool { return &b }
