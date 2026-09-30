package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
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
	examples, err := onboarding.ExampleLines(banner, "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(examples, "\n") != strings.Join(documentedExamples, "\n") {
		t.Fatalf("the banner's examples are %q, this test names %q", examples, documentedExamples)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-work", lines)
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, s := range steps {
		commands = append(commands, "nova-work "+strings.Join(s.Args, " "))
	}
	if strings.Join(commands, "\n") != strings.Join(documentedExamples, "\n") {
		t.Fatalf("the transcript runs %q and the banner's examples are %q; they are one list", commands, documentedExamples)
	}

	const org, repo = "mas-bandwidth", "reliable" // the recording's
	dir := t.TempDir()
	gh, asked := fakeGh(t, dir, "../../internal/workgh/testdata/reliable")
	stand := strings.NewReplacer("$ORG", org, "$REPO", repo, "./", dir+"/")
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		// The reader's gh is on PATH; here a stand-in named with --gh answers
		// each call from the recording, in order, from the first call of each line.
		args := []string{}
		for _, a := range s.Args {
			args = append(args, stand.Replace(a))
		}
		args = append(args, "--gh", gh)
		if err := os.Remove(filepath.Join(dir, "calls")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		code, out, errs := do(t, nil, args...)
		if code != 0 {
			t.Errorf("the documented command %s exits %d; stderr: %s", s.Line, code, errs)
		}
		got = append(got, onboarding.Result{Code: code, Stdout: out, Stderr: errs})
		asked(s.Line)
	}
	volatile := []onboarding.Field{
		{Name: "tmpdir", Doc: ".", Run: dir},
		{Name: "recorded", Doc: "$ORG", Run: org},
		{Name: "recorded", Doc: "$REPO", Run: repo},
	}
	for _, p := range onboarding.CompareTranscript(steps, got, volatile) {
		t.Error(p)
	}
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
	if err != nil || len(names) == 0 {
		t.Fatalf("no recording under %s: %v", recording, err)
	}
	sort.Strings(names)
	var vars []map[string]any
	for i, n := range names {
		raw, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		var rec struct {
			Vars  map[string]any  `json:"vars"`
			Reply json.RawMessage `json:"reply"`
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		vars = append(vars, rec.Vars)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("reply-%d.json", i+1)), rec.Reply, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gh := filepath.Join(dir, "gh")
	script := "#!/bin/sh\nn=$(cat " + dir + "/calls 2>/dev/null || echo 0)\nn=$((n+1))\necho $n > " + dir + "/calls\n" +
		"cat > " + dir + "/request-$n.json\n[ -f " + dir + "/reply-$n.json ] || { echo 'past the recording' >&2; exit 1; }\ncat " + dir + "/reply-$n.json\n"
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return gh, func(line string) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, "calls"))
		if err != nil {
			t.Fatalf("under %s the stand-in gh was never called: %v", line, err)
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		for i := 1; i <= n && i <= len(vars); i++ {
			body, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("request-%d.json", i)))
			if err != nil {
				t.Fatal(err)
			}
			var req struct {
				Variables map[string]any `json:"variables"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(req.Variables, vars[i-1]) {
				t.Errorf("under %s call %d asked %v, the recording asked %v", line, i, req.Variables, vars[i-1])
			}
		}
	}
}
