package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A first run by someone who has never seen this tool hits three refusals in a
// row — a directory name passed as --channels, then a missing --k, then a
// missing --root — and the no-guessing law says every one of them must stay a
// refusal. What it does not say is that a refusal may only name what was
// wrong. These tests pin the sentence each one now ends with, because guidance
// nothing checks rots into a claim about a message that has since moved.

const firstRunDraft = "The lantern glazing is cleaned with two cloths, one for the brass and one for the glass, before the fog signal is tested.\n"

func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	cases := []struct {
		name, want string
		args       []string
	}{
		{
			name: "a directory name passed as a channel",
			args: []string{"search", "--root", corpus, "--channels", "journal", "--k", "3", "x"},
			want: `unknown channel "journal": --channels names a retrieval method, not a directory; the channels are bm25 and trigram, and bm25 alone is the usual start`,
		},
		{
			name: "search without k",
			args: []string{"search", "--root", corpus, "--channels", "bm25", "x"},
			want: "--k is the number of hits to return and is required (search: 3 to 5; check: 2 or 3 per paragraph)",
		},
		{
			name: "search without root",
			args: []string{"search", "--channels", "bm25", "--k", "3", "x"},
			want: "--root <dir> is your corpus directory, the tree to index; it is never guessed from the working directory or the environment, so write it out every run",
		},
		// The hint belongs to the flag, not to the verb that happened to want
		// it: a first run of check must not be told less than a first run of
		// search.
		{
			name: "check without k",
			args: []string{"check", "--root", corpus, "--channels", "bm25", "-"},
			want: "--k is the number of hits to return and is required",
		},
		{
			name: "check without channels",
			args: []string{"check", "--root", corpus, "--k", "3", "-"},
			want: "--channels names a retrieval method, not a directory",
		},
		{
			name: "an empty channel list",
			args: []string{"search", "--root", corpus, "--channels", "", "--k", "3", "x"},
			want: "--channels names a retrieval method, not a directory",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2 — guidance must not soften the refusal; stderr: %s", exit, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q,\nwant it to contain %q", stderr, tc.want)
			}
			if stdout != "" {
				t.Errorf("a refusal must print nothing on stdout, got %q", stdout)
			}
		})
	}
}

// The usage banner ends in one example per retrieval verb, and the examples are
// RUN here rather than read: an example that has drifted out of the flag set
// teaches the wrong invocation to exactly the reader who cannot tell.
func TestUsageBannerExamplesRun(t *testing.T) {
	examples := usageExamples(t)
	if len(examples) != 2 {
		t.Fatalf("want one search example and one check example under `example:`, got %d: %q", len(examples), examples)
	}
	if !strings.HasPrefix(examples[0], "nova-memory search ") {
		t.Errorf("the first example is not a search: %q", examples[0])
	}
	if !strings.HasPrefix(examples[1], "nova-memory check ") {
		t.Errorf("the second example is not a check: %q", examples[1])
	}
	draft := writeDraft(t)
	for _, ex := range examples {
		exit, stdout, stderr := runCLI(t, "", localize(strings.Fields(ex)[1:], draft)...)
		if exit != 0 {
			t.Fatalf("the usage example %q does not run: exit %d, stderr: %s", ex, exit, stderr)
		}
		if stdout == "" {
			t.Errorf("the usage example %q printed nothing", ex)
		}
	}
}

// usageExamples returns the command lines under the banner's `example:` heading.
func usageExamples(t *testing.T) []string {
	t.Helper()
	exit, _, stderr := runCLI(t, "")
	if exit != 2 {
		t.Fatalf("a bare invocation must be exit 2, got %d", exit)
	}
	_, tail, found := strings.Cut(stderr, "\nexample:\n")
	if !found {
		t.Fatalf("the usage banner has no `example:` section:\n%s", stderr)
	}
	var out []string
	for _, line := range strings.Split(tail, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "nova-memory ") {
			out = append(out, strings.Join(strings.Fields(line), " "))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The README's First run block, checked against the tool

// The transcript in README.md `## nova-memory` is the first thing a stranger
// copies, so it is not written by hand and left alone: the commands in it are
// run here against the fixture corpus, and every transcript line must match a
// line the tool actually printed — the event prefix and the field names, in
// order. Scores, counts, paths and snippets are a run's own business and are
// deliberately NOT compared: the block shows a real corpus's numbers, and
// pinning those would make the README a fixture instead of a document.
func TestREADMEFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	lines := readmeFirstRun(t)
	draft := writeDraft(t)

	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-memory "); ok {
			exit, stdout, stderr := runCLI(t, "", localize(strings.Fields(cmd), draft)...)
			if exit != 0 {
				t.Fatalf("the README command %q does not run: exit %d, stderr: %s", line, exit, stderr)
			}
			printed = map[string]bool{}
			for _, out := range strings.Split(stdout, "\n") {
				if s := shape(out); s != "" {
					printed[s] = true
				}
			}
			continue
		}
		s := shape(line)
		if s == "" {
			continue
		}
		if printed == nil {
			t.Fatalf("transcript line before any command: %q", line)
		}
		if !printed[s] {
			t.Errorf("README line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
		seen[strings.Join(strings.Fields(s)[:2], " ")]++
	}
	for prefix, want := range map[string]int{
		"SEARCH OK": 1, "SEARCH CAL": 1, "SEARCH HIT": 3,
		"MEMORY OK": 1, "MEMORY CAL": 1, "MEMORY CAND": 1, "MEMORY HIT": 1,
	} {
		if seen[prefix] != want {
			t.Errorf("README First run shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}

// shape reduces an output line to the part the README promises: the two-token
// event prefix, then the field names in order. Everything after the ": " that
// closes the fields is the tail the grammar says is never scanned, and is not
// compared here either.
func shape(line string) string {
	head := line
	if i := strings.Index(line, ": "); i >= 0 {
		head = line[:i]
	}
	toks := strings.Fields(head)
	if len(toks) < 2 || strings.ToUpper(toks[0]) != toks[0] {
		return ""
	}
	out := []string{toks[0], toks[1]}
	for _, tok := range toks[2:] {
		if k, _, ok := strings.Cut(tok, "="); ok {
			out = append(out, k+"=")
		}
	}
	return strings.Join(out, " ")
}

// readmeFirstRun returns the lines of the fenced transcript under `### First run`.
func readmeFirstRun(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, tail, found := strings.Cut(string(raw), "\n### First run\n")
	if !found {
		t.Fatal("README.md has no `### First run` section; it is what a stranger reads before anything else here")
	}
	body, _, found := strings.Cut(tail, "\n## ")
	if !found {
		body = tail
	}
	var lines []string
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		t.Fatal("`### First run` holds no fenced transcript")
	}
	return lines
}

// localize points an example or README command at the fixture corpus and at a
// real candidate file, so the command's SHAPE is what is under test and not
// the reader's directory layout.
func localize(args []string, draft string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == "--root" {
			out[i+1] = corpus
		}
	}
	if len(out) > 0 && strings.HasSuffix(out[len(out)-1], "draft.md") {
		out[len(out)-1] = draft
	}
	return out
}

func writeDraft(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(path, []byte(firstRunDraft), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
