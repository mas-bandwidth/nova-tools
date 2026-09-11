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

// The usage banner ends in the quickstart line and one example per retrieval
// verb, and the examples are RUN here rather than read: an example that has
// drifted out of the flag set teaches the wrong invocation to exactly the
// reader who cannot tell. The quickstart line comes first because it is the
// one a reader with nothing but a directory can type.
func TestUsageBannerExamplesRun(t *testing.T) {
	examples := usageExamples(t)
	if len(examples) != 3 {
		t.Fatalf("want a quickstart, a search and a check example under `example:`, got %d: %q", len(examples), examples)
	}
	if !strings.HasPrefix(examples[0], "nova-memory quickstart ") {
		t.Errorf("the first example is not the quickstart: %q", examples[0])
	}
	if !strings.HasPrefix(examples[1], "nova-memory search ") {
		t.Errorf("the second example is not a search: %q", examples[1])
	}
	if !strings.HasPrefix(examples[2], "nova-memory check ") {
		t.Errorf("the third example is not a check: %q", examples[2])
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
//
// It asks for the banner, because a bare invocation no longer IS one: a refusal now costs
// one line and names the door (`run: nova-memory help`), and this reads what is behind
// that door -- which is also a test that the door opens.
func usageExamples(t *testing.T) []string {
	t.Helper()
	exit, stdout, stderr := runCLI(t, "", "help")
	if exit != 0 {
		t.Fatalf("`nova-memory help` must be exit 0, got %d; stderr: %s", exit, stderr)
	}
	_, tail, found := strings.Cut(stdout, "\nexample:\n")
	if !found {
		t.Fatalf("the usage banner has no `example:` section:\n%s", stdout)
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
	blocks := readmeFirstRun(t)
	lines := blocks[len(blocks)-1]
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

// readmeFirstRun returns the fenced transcripts under `### First run`, one
// slice of lines per block: the quickstart block first, the two-verb block
// last. They are checked separately because they are two different promises —
// one run that printed everything, and two runs a reader types themselves.
func readmeFirstRun(t *testing.T) [][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Scoped to THIS tool's section first: every command in this repo now carries
	// a `### First run` (ONBOARDING.md), so cutting on the first one in the file
	// would hand this test another binary's transcript to run.
	_, section, found := strings.Cut(string(raw), "\n## nova-memory\n")
	if !found {
		t.Fatal("README.md has no `## nova-memory` section")
	}
	_, tail, found := strings.Cut(section, "\n### First run\n")
	if !found {
		t.Fatal("README.md `## nova-memory` has no `### First run` section; it is what a stranger reads before anything else here")
	}
	body, _, found := strings.Cut(tail, "\n## ")
	if !found {
		body = tail
	}
	var blocks [][]string
	var lines []string
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "```") {
			if fenced && len(lines) > 0 {
				blocks = append(blocks, lines)
				lines = nil
			}
			fenced = !fenced
			continue
		}
		if fenced {
			lines = append(lines, line)
		}
	}
	if len(blocks) < 2 {
		t.Fatalf("`### First run` must hold a quickstart transcript and the two-verb transcript, got %d fenced blocks", len(blocks))
	}
	return blocks
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

// ---------------------------------------------------------------------------
// quickstart — the first run, and what it promises in print

// The three steps, in order, with the flags each one is echoed with. The
// promise quickstart makes is that the line above an output IS the line that
// produced it, so the echoes are checked as text and not as shapes: a
// demonstration whose printed command differs from the command it ran teaches
// an invocation that does not work.
func TestQuickstartEchoesEveryCommandItRuns(t *testing.T) {
	exit, stdout, stderr := runCLI(t, "", "quickstart", "--root", corpus)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")

	if !strings.HasPrefix(lines[0], "QUICKSTART OK root=") {
		t.Fatalf("the first line does not say what this run chose: %q", lines[0])
	}
	words := field(t, lines[0], "words")
	if got := field(t, lines[0], "words-source"); got != "corpus-top-terms" {
		t.Errorf("words-source = %q, want corpus-top-terms when --words was not given", got)
	}
	for _, w := range strings.Split(words, `\x20`) {
		if quickstartFunctionWords[w] {
			t.Errorf("the demonstration query offers %q, a function word, as one of this corpus's own terms", w)
		}
	}
	if want := "QUICKSTART NOTE " + quickstartChoiceNote; lines[len(lines)-1] != want {
		t.Errorf("the last line is\n  %s\nwant\n  %s", lines[len(lines)-1], want)
	}

	// Each echo, and the token the output under it must open with. The k
	// values are the ones the closing note names, so a change to one that
	// forgets the other fails here.
	steps := []struct{ echo, token string }{
		{"$ nova-memory stats --root " + corpus, "STATS"},
		{"$ nova-memory search --root " + corpus + " --channels bm25 --k 3 " + strings.ReplaceAll(words, `\x20`, " "), "SEARCH"},
		{"$ nova-memory check --root " + corpus + " --channels bm25 --k 2 -", "MEMORY"},
	}
	at := 0
	for _, st := range steps {
		i := indexOf(lines, st.echo)
		if i < 0 {
			t.Fatalf("quickstart never printed the command line\n  %s\ngot:\n%s", st.echo, stdout)
		}
		if i < at {
			t.Errorf("the steps are out of order: %q came before the step above it", st.echo)
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], st.token+" ") {
			t.Errorf("the line under %q is not that command's output: %q", st.echo, lines[i+1])
		}
		at = i
	}
	// The candidate is named before the check that reads it, because a
	// paragraph arriving on stdin is the one thing in the transcript a reader
	// cannot see the source of.
	demo := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "QUICKSTART DEMO ") {
			demo = i
		}
	}
	if demo < 0 || demo >= indexOf(lines, steps[2].echo) {
		t.Errorf("no QUICKSTART DEMO line above the check step:\n%s", stdout)
	}
	if !strings.Contains(quickstartChoiceNote, "not defaults") {
		t.Error("the closing note no longer says the choices are not defaults, which is the whole point of the verb")
	}
}

// --words and --draft are the caller's, and they must reach the echoed line:
// a flag that changed the run without changing the printed command would make
// the transcript a decoration.
func TestQuickstartRunsTheWordsAndDraftItWasGiven(t *testing.T) {
	draft := writeDraft(t)
	exit, stdout, stderr := runCLI(t, "", "quickstart", "--root", corpus, "--words", "glazing", "--words", "brass", "--draft", draft)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if got := field(t, lines[0], "words-source"); got != "given" {
		t.Errorf("words-source = %q, want given", got)
	}
	want := "$ nova-memory search --root " + corpus + " --channels bm25 --k 3 glazing brass"
	if indexOf(lines, want) < 0 {
		t.Errorf("quickstart never printed\n  %s\ngot:\n%s", want, stdout)
	}
	if want := "$ nova-memory check --root " + corpus + " --channels bm25 --k 2 " + draft; indexOf(lines, want) < 0 {
		t.Errorf("quickstart never printed\n  %s\ngot:\n%s", want, stdout)
	}
	if strings.Contains(stdout, "QUICKSTART DEMO") {
		t.Error("a run given its own --draft must not announce the corpus's first paragraph")
	}
}

// Exit 0 means all three ran. A step that could not run ends the demonstration
// there, and the closing note — the sentence a reader is meant to leave with —
// is not printed over a run that did not finish.
func TestQuickstartExitsTwoWhenAStepCouldNotRun(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := runCLI(t, "", "quickstart", "--root", corpus, "--draft", empty)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stderr, "the check step could not run") {
		t.Errorf("stderr does not name the step that failed: %q", stderr)
	}
	if strings.Contains(stdout, quickstartChoiceNote) {
		t.Error("a quickstart that did not finish printed its closing note anyway")
	}
	if !strings.Contains(stdout, "SEARCH OK") {
		t.Errorf("the steps that did run must still be on the page: %q", stdout)
	}
}

// The README's quickstart transcript, held to the tool: the first line is RUN,
// and every line under it must be a line that run printed. The echoed command
// lines are compared as text, with the root normalized because the README
// shows a reader's corpus and the test has its own; everything else is
// compared by shape, so the fixture's scores stay the fixture's business.
func TestREADMEFirstRunQuickstartBlockMatchesWhatTheToolPrints(t *testing.T) {
	lines := readmeFirstRun(t)[0]
	cmd, ok := strings.CutPrefix(lines[0], "$ nova-memory quickstart ")
	if !ok {
		t.Fatalf("`### First run` must open on the quickstart command, got %q", lines[0])
	}
	exit, stdout, stderr := runCLI(t, "", localize(append([]string{"quickstart"}, strings.Fields(cmd)...), "")...)
	if exit != 0 {
		t.Fatalf("the README quickstart does not run: exit %d, stderr: %s", exit, stderr)
	}
	printedShape, printedEcho := map[string]bool{}, map[string]bool{}
	for _, out := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(out, "$ nova-memory ") {
			printedEcho[rootless(out)] = true
			continue
		}
		if s := shape(out); s != "" {
			printedShape[s] = true
		}
	}
	seen := map[string]int{}
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "$ nova-memory ") {
			if !printedEcho[rootless(line)] {
				t.Errorf("README shows the command line\n  %s\nwhich this quickstart never printed. Re-run it and paste what it said.", line)
			}
			seen["$ nova-memory"]++
			continue
		}
		s := shape(line)
		if s == "" {
			continue
		}
		if !printedShape[s] {
			t.Errorf("README line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
		seen[strings.Join(strings.Fields(s)[:2], " ")]++
	}
	for prefix, want := range map[string]int{
		"$ nova-memory": 3, "QUICKSTART DEMO": 1, "QUICKSTART NOTE": 1,
		"SEARCH OK": 1, "SEARCH CAL": 1, "SEARCH HIT": 3,
		"MEMORY OK": 1, "MEMORY CAL": 1, "MEMORY CAND": 1, "MEMORY HIT": 2,
	} {
		if seen[prefix] != want {
			t.Errorf("the README quickstart transcript shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[len(lines)-1]), quickstartChoiceNote) {
		t.Errorf("the transcript does not end on the sentence the verb exists for: %q", lines[len(lines)-1])
	}
}

// rootless replaces the argument of --root, so a command line the README shows
// against a reader's corpus can be compared with the same line run against the
// fixture.
func rootless(line string) string {
	toks := strings.Fields(line)
	for i := 0; i < len(toks)-1; i++ {
		if toks[i] == "--root" {
			toks[i+1] = "<root>"
		}
	}
	return strings.Join(toks, " ")
}

func indexOf(lines []string, want string) int {
	for i, line := range lines {
		if line == want {
			return i
		}
	}
	return -1
}

// field reads one key=value field off an event line.
func field(t *testing.T, line, key string) string {
	t.Helper()
	for _, tok := range strings.Fields(line) {
		if k, v, ok := strings.Cut(tok, "="); ok && k == key {
			return v
		}
	}
	t.Fatalf("no %s= field on %q", key, line)
	return ""
}

// ---------------------------------------------------------------------------
// One run, every reason

// A first run is usually wrong about more than one thing, and the tool that
// answers one refusal at a time turns that into a guessing game played one
// round per invocation. Every reason a run cannot start is reported by the run
// that could not start.
func TestARefusalReportsEveryReasonAtOnce(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "search missing both channels and k",
			args: []string{"search", "--root", corpus, "x"},
			want: []string{
				"--channels is required", channelsHint,
				"--k is required", kHint,
			},
		},
		{
			name: "search with a bad channel and a bad k, and no query",
			args: []string{"search", "--root", corpus, "--channels", "journal", "--k", "0"},
			want: []string{
				`unknown channel "journal"`,
				"--k must be a positive receipt budget",
				"no query words given",
			},
		},
		{
			name: "check missing everything but the verb",
			args: []string{"check"},
			want: []string{
				"--channels is required", channelsHint,
				"--k is required", kHint,
				"--root is required", rootHint,
				"exactly one candidate file",
			},
		},
		{
			name: "eval with a bad channel, a bad k and a bad floor",
			args: []string{"eval", "--root", corpus, "--channels", "journal", "--k", "-1", "--floor", "2", exampleGold},
			want: []string{
				`unknown channel "journal"`,
				"--k must be a positive receipt budget",
				"--floor must be in (0,1]",
			},
		},
		// A missing flag says one thing, not two: the value of a flag nobody
		// gave is not a second mistake the caller made.
		{
			name: "a missing channels flag does not also complain about its empty value",
			args: []string{"search", "--root", corpus, "--k", "3", "x"},
			want: []string{"--channels is required"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stderr: %s", exit, stderr)
			}
			if stdout != "" {
				t.Errorf("a refusal must print nothing on stdout, got %q", stdout)
			}
			for _, w := range tc.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr does not report %q; one run must report them all, got:\n%s", w, stderr)
				}
			}
			if strings.Contains(stderr, "--channels is required") && strings.Contains(stderr, "named no channels") {
				t.Errorf("a missing --channels was reported twice, once as missing and once as empty:\n%s", stderr)
			}
		})
	}
}

// The echoed step is a line the reader is meant to PASTE, and the shell they
// paste it into is the one on their own machine. The specimen is the Windows
// path: rendered as a Go string literal every separator doubled, so the echo
// named C:\\Users\\... — a path that does not exist, printed by the one verb
// whose whole job is to be copied. Both platforms' rules are pinned here
// rather than on the platform that happens to be running.
func TestTheEchoedStepPastesBackIntoThatPlatformsShell(t *testing.T) {
	cases := []struct {
		name    string
		windows bool
		argv    []string
		want    string
	}{
		{
			name:    "a windows path is echoed verbatim, separators and all",
			windows: true,
			argv:    []string{"check", "--root", "corpus", `C:\Users\RUNNER~1\AppData\Local\Temp\Test1\001\draft.md`},
			want:    `check --root corpus C:\Users\RUNNER~1\AppData\Local\Temp\Test1\001\draft.md`,
		},
		{
			name:    "a windows path holding a space is double-quoted, which that shell takes literally",
			windows: true,
			argv:    []string{"check", `C:\Program Files\notes\draft.md`},
			want:    `check "C:\Program Files\notes\draft.md"`,
		},
		{
			name: "a posix path is echoed verbatim",
			argv: []string{"check", "--root", "corpus", "/var/folders/t7/Test1/001/draft.md"},
			want: "check --root corpus /var/folders/t7/Test1/001/draft.md",
		},
		{
			name: "a posix argument holding a space is single-quoted, and keeps every character",
			argv: []string{"search", "salt haze"},
			want: `search 'salt haze'`,
		},
		{
			name: "a posix argument the shell would act on is quoted rather than handed over",
			argv: []string{"search", "$HOME", "a'b", `back\slash`},
			want: `search '$HOME' 'a'\''b' 'back\slash'`,
		},
		{
			name:    "an empty argument is still a word on both shells",
			windows: true,
			argv:    []string{"search", ""},
			want:    `search ""`,
		},
		{
			name: "a newline in an argument cannot break the echo in two",
			argv: []string{"search", "one\ntwo"},
			want: `search 'one\x0atwo'`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandLineFor(tc.argv, tc.windows); got != tc.want {
				t.Errorf("commandLineFor(%q, windows=%v) =\n  %s\nwant\n  %s", tc.argv, tc.windows, got, tc.want)
			}
			if strings.Contains(commandLineFor(tc.argv, tc.windows), "\n") {
				t.Error("the echoed line is more than one line")
			}
		})
	}
}
