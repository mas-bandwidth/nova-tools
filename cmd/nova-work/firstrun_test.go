package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain fixes the clock for every test of this package before any runs, so
// the tree an import writes records one instant and the transcript's sha256 and
// seconds= reproduce. No test here reads the real time.
func TestMain(m *testing.M) {
	fixed := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	os.Exit(m.Run())
}

// TestTESTSFirstRunIsWhatTheToolPrints: the `### First run` block of
// docs/TESTS.md is EXECUTED, every command in order, against the recorded
// conversation with GitHub the other tests use (internal/workgh/testdata/
// reliable: one public repository of twenty issues, read at fifteen a page), and
// the whole output is compared by the one comparator. The banner's `example:`
// block is the same three commands (docs/ONBOARDING.md point 6), so one sitting
// keeps both promises.
//
// $ORG and $REPO are the reader's: the test stands them for the recording's
// names on the command line, and the comparator's `recorded` entry writes the
// recording's names back as $ORG and $REPO where the tool prints them.
// ./tree.lisp is a file in a directory of the test's own, and ./gh the stand-in
// for the reader's gh, whose path the first line prints.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	// The banner's example lines, named in this test's own body so the
	// pasted-examples rule (internal/ci, SPEC-TOOLWORK.md documents rule 6) reads
	// the command text here; the transcript holds the same lines.
	documentedExamples := []string{
		"nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --dry-run",
		"nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --out ./tree.lisp",
		"nova-work verify --tree ./tree.lisp --repo $ORG/$REPO --page-size 15",
	}
	examples, err := onboarding.ExampleLines(workCLI(nil).OK(t, "help").Stdout, "nova-work")
	require.NoError(t, err)
	require.Equal(t, strings.Join(documentedExamples, "\n"), strings.Join(examples, "\n"), "the banner's examples are %q, this test names %q", examples, documentedExamples)

	raw := testkit.ReadFile(t, filepath.Join("..", "..", "docs", "TESTS.md"))
	lines, err := onboarding.FirstRun(raw, "nova-work")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-work", lines)
	require.NoError(t, err)
	var commands []string
	for _, s := range steps {
		commands = append(commands, "nova-work "+strings.Join(s.Args, " "))
	}
	require.Equal(t, strings.Join(documentedExamples, "\n"), strings.Join(commands, "\n"), "the transcript runs %q and the banner's examples are %q; they are one list", commands, documentedExamples)

	const org, repo = "mas-bandwidth", "reliable" // the recording's
	dir := t.TempDir()
	gh, asked := fakeGh(t, dir, "../../internal/workgh/testdata/reliable")
	stand := strings.NewReplacer("$ORG", org, "$REPO", repo, "./", dir+"/")
	got := make([]onboarding.Result, 0, len(steps))
	tool := workCLI(nil)
	for _, s := range steps {
		// The reader's gh is on PATH; here a stand-in named with --gh answers
		// each call from the recording, in order, from the first call of each line.
		args := []string{}
		for _, a := range s.Args {
			args = append(args, stand.Replace(a))
		}
		args = append(args, "--gh", gh)
		if err := os.Remove(filepath.Join(dir, "calls")); err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		res := tool.Run(args...)
		assert.Equal(t, 0, res.Code, "the documented command %s exits %d; stderr: %s", s.Line, res.Code, res.Stderr)
		got = append(got, onboarding.Result{Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr})
		asked(s.Line)
	}
	volatile := []onboarding.Field{
		{Name: "tmpdir", Doc: ".", Run: dir},
		{Name: "recorded", Doc: "$ORG", Run: org},
		{Name: "recorded", Doc: "$REPO", Run: repo},
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, volatile))
}

// fakeGh writes a stand-in gh into dir that answers `gh api graphql --input -`
// with the recording's replies in order, counting its calls in dir/calls and
// keeping each request body. The returned check holds the requests a line made
// to the variables the recording was made with, as workgh.Replay does.
func fakeGh(t *testing.T, dir, recording string) (string, func(line string)) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in gh runs through /bin/sh")
	}
	names, err := filepath.Glob(filepath.Join(recording, "call-*.json"))
	require.NoError(t, err, "no recording under %s: %v", recording, err)
	require.NotEmpty(t, names, "no recording under %s: %v", recording, err)
	sort.Strings(names)
	var vars []map[string]any
	for i, n := range names {
		raw := testkit.ReadFile(t, n)
		var rec struct {
			Vars  map[string]any  `json:"vars"`
			Reply json.RawMessage `json:"reply"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &rec))
		vars = append(vars, rec.Vars)
		testkit.WriteFile(t, filepath.Join(dir, fmt.Sprintf("reply-%d.json", i+1)), string(rec.Reply))
	}
	gh := filepath.Join(dir, "gh")
	script := "#!/bin/sh\nn=$(cat " + dir + "/calls 2>/dev/null || echo 0)\nn=$((n+1))\necho $n > " + dir + "/calls\n" +
		"cat > " + dir + "/request-$n.json\n[ -f " + dir + "/reply-$n.json ] || { echo 'past the recording' >&2; exit 1; }\ncat " + dir + "/reply-$n.json\n"
	require.NoError(t, os.WriteFile(gh, []byte(script), 0o755))
	return gh, func(line string) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, "calls"))
		require.NoError(t, err, "under %s the stand-in gh was never called: %v", line, err)
		n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		for i := 1; i <= n && i <= len(vars); i++ {
			type reqBody struct {
				Variables map[string]any `json:"variables"`
			}
			req := testkit.ReadJSON[reqBody](t, filepath.Join(dir, fmt.Sprintf("request-%d.json", i)))
			assert.Equal(t, vars[i-1], req.Variables, "under %s call %d asked %v, the recording asked %v", line, i, req.Variables, vars[i-1])
		}
	}
}
