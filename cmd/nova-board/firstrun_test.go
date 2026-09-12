package main

// The onboarding standard (ONBOARDING.md), pinned for this binary. A newcomer's first
// stumble is the spec for these tests: the usage banner's examples are RUN rather than
// read, every refusal a first run hits must say what the flag WANTS and one run must name
// every independent problem, and the transcript in TESTS.md is compared against what the
// tool actually prints. Guidance nothing checks rots into a claim about a message that has
// since moved.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// exampleBoard is the fixture board that ships with this tool: five cards, two of them
// rows of an owed ledger, one closed, referenced by nothing outside testdata.
const exampleBoard = "testdata/example-board"

func runFixture(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit := run(localize(args), &out, &errb, time.Now().UTC(), &seq{})
	return exit, out.String(), errb.String()
}

// localize points an example or a transcript command at the fixture, so what is under test
// is the command's SHAPE and not the reader's directory layout.
func localize(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if a == "./board" {
			out[i] = exampleBoard
		}
	}
	return out
}

func examples(t *testing.T) []string {
	t.Helper()
	// The banner is asked for, because a bare invocation is not one: a refusal costs one
	// line and names the door. Reading it through that door is also a test that it opens.
	exit, stdout, stderr := runFixture(t, "help")
	if exit != 0 {
		t.Fatalf("`nova-board help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	}
	lines, err := onboarding.ExampleLines(stdout, "nova-board")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, stdout)
	}
	return lines
}

// (a) The usage banner ends in an `example:` block of lines that actually run. They are
// run here against the fixture: an example that has drifted out of the flag set teaches
// the wrong invocation to exactly the reader who cannot tell.
//
// A line that runs answers 0 or 1. EXIT 2 IS "COULD NOT RUN", and an example exiting 2 is
// a broken example — which is the whole distinction this tool's `check` rests on, so the
// assertion here is the same one the shell guard makes.
func TestUsageBannerExamplesRun(t *testing.T) {
	for _, ex := range examples(t) {
		exit, stdout, stderr := runFixture(t, strings.Fields(ex)[1:]...)
		if exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr)
			continue
		}
		if stdout == "" {
			t.Errorf("the usage example %q printed nothing on stdout", ex)
		}
		if strings.HasPrefix(ex, "nova-board check ") && exit != 1 {
			t.Errorf("the example %q exits %d; it is in the banner BECAUSE it matches, and a check that matched says NO", ex, exit)
		}
	}
}

// quickstart is what a first run is offered first.
func TestQuickstartIsTheFirstThingTheBannerOffers(t *testing.T) {
	exs := examples(t)
	if !strings.HasPrefix(exs[0], "nova-board quickstart ") {
		t.Errorf("the first example is %q; a first run should be offered quickstart first", exs[0])
	}
	if !strings.Contains(usage, "nova-board quickstart (--issue ... | --dir ...) --stale <duration>") {
		t.Error("the usage block does not list the quickstart verb")
	}
	for _, verb := range []string{"list", "add", "take", "close", "check"} {
		if !strings.Contains(usage, "nova-board "+verb+" ") {
			t.Errorf("the usage block does not list the %s verb", verb)
		}
	}
}

// (b) A refusal says what the flag or input WANTS, not only what was wrong.
func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"list without a stale window", []string{"list", "--dir", "./board"}, staleHint},
		{"list without a backend", []string{"list", "--stale", "10m"}, backendHint},
		{"list with two backends", []string{"list", "--dir", "./board", "--issue", "a/b#1", "--stale", "10m"}, twoBackends},
		{"add without a name", []string{"add", "--dir", "./board", "--text", "x", "--by", "4h", "--default", "d"}, asHint},
		{"add without a text", []string{"add", "--dir", "./board", "--as", "rowan", "--by", "4h", "--default", "d"}, textHint},
		{"add without a deadline", []string{"add", "--dir", "./board", "--as", "rowan", "--text", "x", "--default", "d"}, byHint},
		{"add without a default", []string{"add", "--dir", "./board", "--as", "rowan", "--text", "x", "--by", "4h"}, defHint},
		{"add with half a row", []string{"add", "--dir", "./board", "--as", "rowan", "--text", "x", "--by", "4h", "--default", "d", "--leg", "cpp"}, legHint},
		{"take without a card", []string{"take", "--dir", "./board", "--as", "rowan", "--stale", "10m"}, cardHint},
		{"close without a how", []string{"close", "--dir", "./board", "--as", "rowan", "--card", "5a64568ee513a2544d6eb17cb445d4fc", "--stale", "10m"}, howHint},
		{"check without words", []string{"check", "--dir", "./board"}, wordsHint},
		{"quickstart without a stale window", []string{"quickstart", "--dir", "./board"}, staleHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runFixture(t, tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2 — guidance must not soften the refusal; stderr: %s", exit, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q,\nwant it to contain the hint %q", stderr, tc.want)
			}
			if stdout != "" {
				t.Errorf("a refusal must print nothing on stdout, got %q", stdout)
			}
			if !strings.Contains(stderr, "run: nova-board help") {
				t.Errorf("the refusal names no door: %q", stderr)
			}
		})
	}
}

// (b), the other half: ONE RUN REPORTS EVERY PROBLEM IT CAN FIND. Being sent back a second
// time for something the first run could already see is the stumble this pins shut.
func TestIndependentProblemsAreReportedInOneRun(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"add with nothing at all", []string{"add"}, []string{backendHint, asHint, textHint, byHint, defHint}},
		{"take with nothing at all", []string{"take"}, []string{backendHint, asHint, cardHint, staleHint}},
		{"close with nothing at all", []string{"close"}, []string{backendHint, asHint, cardHint, staleHint, howHint}},
		{"check with nothing at all", []string{"check"}, []string{backendHint, wordsHint}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, _, stderr := runFixture(t, tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stderr: %s", exit, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("one run must name every problem it can find; this one is missing:\n  %s\nfrom:\n%s", want, stderr)
				}
			}
		})
	}
}

// (c) The transcript in TESTS.md, checked against the tool. The commands in it are RUN
// against the fixture, and every transcript line must match a line the tool actually
// printed — the event prefix and the field names, in order. Numbers, paths and tails are a
// run's own business and are deliberately NOT compared: pinning those would make the
// document a fixture.
func TestTheFirstRunTranscriptMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-board")
	if err != nil {
		t.Fatal(err)
	}
	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-board "); ok {
			exit, stdout, stderr := runFixture(t, strings.Fields(cmd)...)
			if exit == 2 {
				t.Fatalf("the transcript command %q does not run: exit 2, stderr: %s", line, stderr)
			}
			printed = map[string]bool{}
			for _, out := range strings.Split(stdout, "\n") {
				if s := onboarding.Shape(out); s != "" {
					printed[s] = true
				}
			}
			continue
		}
		s := onboarding.Shape(line)
		if s == "" {
			continue
		}
		if printed == nil {
			t.Fatalf("transcript line before any command: %q", line)
		}
		if !printed[s] {
			t.Errorf("TESTS.md line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
		seen[strings.Join(strings.Fields(s)[:2], " ")]++
	}
	for prefix, want := range map[string]int{
		"QUICKSTART OK": 1, "BOARD NEXT": 2, "BOARD OK": 2, "CHECK OK": 1, "CHECK HIT": 1, "BOARD LEG": 3,
	} {
		if seen[prefix] != want {
			t.Errorf("the TESTS.md First run shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}

// The fixture is small enough to read in a sitting and is referenced by nothing outside
// testdata, which is what ONBOARDING asks of it.
func TestTheFixtureIsSmallEnoughToReadInASitting(t *testing.T) {
	entries, err := os.ReadDir(exampleBoard)
	if err != nil {
		t.Fatal(err)
	}
	bytesTotal := 0
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		bytesTotal += int(info.Size())
	}
	if len(entries) != 5 {
		t.Errorf("the fixture holds %d card files, want 5", len(entries))
	}
	if bytesTotal > 4096 {
		t.Errorf("the fixture is %d bytes; a fixture is meant to be read in a sitting", bytesTotal)
	}
}

// [194] THE COMMAND-REFERENCE TRANSCRIPT IS COMPARED AGAINST REAL OUTPUT. The transcript
// test above reads docs/TESTS.md; the command reference carried a second `### First run`
// for this tool that nothing executed, and it had drifted — an abridged run presented as a
// run, with a count line copied from a different command. A transcript nothing runs is a
// claim about a message that has since moved, and the command reference is where a first
// run reads it. That reference is docs/CLI.md since the README became an adoption guide.
func TestTheCommandReferenceFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	section := string(raw)[strings.Index(string(raw), "## nova-board"):]
	block := section[strings.Index(section, "### First run"):]
	start := strings.Index(block, "```\n$ nova-board ")
	if start < 0 {
		t.Fatal("docs/CLI.md's nova-board ### First run holds no runnable transcript")
	}
	block = block[start+4:]
	block = block[:strings.Index(block, "\n```")]
	lines := strings.Split(block, "\n")
	cmd, ok := strings.CutPrefix(lines[0], "$ nova-board ")
	if !ok {
		t.Fatalf("the first line of the transcript is not a command: %q", lines[0])
	}
	exit, stdout, stderr := runFixture(t, strings.Fields(cmd)...)
	if exit == 2 {
		t.Fatalf("docs/CLI.md's transcript command does not run: exit 2, stderr: %s", stderr)
	}
	printed := map[string]bool{}
	count := 0
	for _, out := range strings.Split(stdout, "\n") {
		if s := onboarding.Shape(out); s != "" {
			printed[s] = true
			count++
		}
	}
	shown := 0
	for _, line := range lines[1:] {
		s := onboarding.Shape(line)
		if s == "" {
			continue
		}
		shown++
		if !printed[s] {
			t.Errorf("README.md line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
	}
	// And the whole run is shown: an abridged transcript presented as a run is the drift
	// this test exists to catch, and it is what the README carried.
	if shown != count {
		t.Errorf("the README shows %d of the %d lines this command prints; an abridgement presented as a run is a claim about output nobody checked", shown, count)
	}
}

// Emma, dogfooding v0.12.0 (nova-tools #104, 2026-09-12): `nova-board quickstart
// --dir ./board --stale 10m` "Expected: `quickstart` to create the directory if
// it does not already exist (consistent with `nova-swarm quickstart --pool
// ./pool` which creates the pool directory)", and instead:
//
//	nova-board quickstart: --dir wants a directory of .board card files: stat
//	/Users/glenn/emma-working/scratch/test-board: no such file or directory;
//	run: nova-board help
//
// Glenn ruled on nova-tools #109: "It is best to do the right thing if a friend
// uses it a certain way, or to correct docs to show only right way. Pick one."
// The right thing: `quickstart` MAKES the directory (TestQuickstartMakesTheDirectory
// below), as `nova-swarm quickstart --pool` makes its pool. Every other verb still
// refuses a directory that is not there -- quickstart is the one verb whose whole
// job is a first run, and a first run has nowhere to write yet, while a `list` or a
// `take` against a directory that does not exist is a caller who named the wrong
// path, and making it for them would hide the typo behind an empty board.
//
// The refusal those verbs print carries the way forward. A refusal names what the
// flag wants (SPEC-BOARD.md:808-814, BUILD item 6 -- an entry in the build list,
// not a numbered rule), and here what it wants is a directory that exists.
func TestADirThatDoesNotExistIsRefusedWithTheMkdirThatFixesIt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "board")
	for _, verb := range []string{"list", "check", "add"} {
		args := []string{verb, "--dir", missing, "--stale", "10m"}
		switch verb {
		case "check":
			args = []string{verb, "--dir", missing, "--words", "anything"}
		case "add":
			args = []string{verb, "--dir", missing, "--as", "rowan", "--text", "a card",
				"--by", "4h", "--default", "the filer files it as a known gap"}
		}
		var out, errb bytes.Buffer
		exit := run(args, &out, &errb, time.Now().UTC(), &seq{})
		if exit != 2 {
			t.Errorf("%s against a missing --dir exits %d, want 2: this tool does not make the directory a caller named", verb, exit)
		}
		got := errb.String()
		if !strings.Contains(got, "mkdir -p \""+missing+"\"") {
			t.Errorf("%s refuses without naming the command that fixes it:\n%s", verb, got)
		}
		if !strings.Contains(got, "does not create one") {
			t.Errorf("%s does not say that the directory is the caller's to make:\n%s", verb, got)
		}
		if lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n"); len(lines) != 1 {
			t.Errorf("%s printed %d lines; the one-line guarantee holds for refusals too:\n%s", verb, len(lines), got)
		}
		if strings.Contains(got, "no such file or directory") {
			t.Errorf("%s still leans on the stat error rather than saying what it wants:\n%s", verb, got)
		}
	}

	// A path that exists and is a FILE is a different mistake and keeps its own
	// message: mkdir -p would not fix it.
	file := filepath.Join(t.TempDir(), "board")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if exit := run([]string{"list", "--dir", file, "--stale", "10m"}, &out, &errb, time.Now().UTC(), &seq{}); exit != 2 {
		t.Errorf("a --dir that is a file exits %d, want 2", exit)
	}
	if !strings.Contains(errb.String(), "is a file") || strings.Contains(errb.String(), "mkdir -p") {
		t.Errorf("a --dir that is a file is not a missing directory:\n%s", errb.String())
	}
}

// TestTheMkdirRemedyIsOnePastableCommandWhenTheDirHasASpace is the other half of the
// remedy: a command a reader cannot paste is not a way forward, it is a second mistake
// for them to find. A --dir with a space printed raw makes `mkdir -p /a/my board` two
// operands, so the paste creates `/a/my` and `board` and the next verb refuses again on
// the same path. The tool already quotes the lines quickstart prints so they can be
// pasted; the refusal's remedy is a line to be pasted too, and it is quoted the same way.
//
// Read by Fable and DeepSeek on nova-tools #109. The two of them also caught the citation
// in the test above: SPEC-BOARD's item 6 is a BUILD list entry (SPEC-BOARD.md:808-814),
// not a numbered rule.
func TestTheMkdirRemedyIsOnePastableCommandWhenTheDirHasASpace(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "my board", "cards")
	var out, errb bytes.Buffer
	if exit := run([]string{"list", "--dir", missing, "--stale", "10m"}, &out, &errb, time.Now().UTC(), &seq{}); exit != 2 {
		t.Fatalf("a missing --dir exits %d, want 2", exit)
	}
	got := errb.String()
	_, rest, ok := strings.Cut(got, "make it first: ")
	if !ok {
		t.Fatalf("the refusal carries no remedy:\n%s", got)
	}
	remedy, _, _ := strings.Cut(rest, "; run: nova-board help")
	if words := shellWords(t, remedy); len(words) != 3 || words[0] != "mkdir" || words[1] != "-p" || words[2] != missing {
		t.Errorf("the remedy %q is not one pastable `mkdir -p <dir>`; a shell reads it as %q, and the directory a caller named was %q", remedy, words, missing)
	}
}

// TestQuickstartMakesTheDirectory is Glenn's ruling on nova-tools #109 in a test:
// "It is best to do the right thing if a friend uses it a certain way, or to correct
// docs to show only right way. Pick one." Emma used quickstart the way nova-swarm's
// quickstart works -- `--pool ./pool` makes the pool -- so quickstart here makes the
// board directory, with MkdirAll and 0755, exactly as cmd/nova-swarm/main.go does.
//
// AND IT SAYS SO. A verb that creates a directory silently is a verb a reader cannot
// tell apart from one that found it already there, and the difference is whether the
// empty board they are looking at is new or is the wrong path. `created=` is a FIELD on
// the line quickstart already prints, not a new line kind, and it is spelled the way this
// tool spells every other boolean field -- `close` prints override=true|false through the
// same yesNo -- rather than the yes|no a sibling tool uses.
func TestQuickstartMakesTheDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "board")
	var out, errb bytes.Buffer
	if exit := run([]string{"quickstart", "--dir", missing, "--stale", "10m"}, &out, &errb, time.Now().UTC(), &seq{}); exit != 0 {
		t.Fatalf("quickstart against a missing --dir exits %d, want 0: it makes the directory, as `nova-swarm quickstart --pool` makes its pool; stderr: %s", exit, errb.String())
	}
	info, err := os.Stat(missing)
	if err != nil || !info.IsDir() {
		t.Fatalf("quickstart returned 0 and %s is not a directory (%v); a first run has nowhere to write", missing, err)
	}
	// THE MODE IS COMPARED AGAINST A CONTROL, NOT AGAINST THE LITERAL 0755. What this
	// test is for is that quickstart asks for the same mode `nova-swarm quickstart
	// --pool` asks for; what a directory ENDS UP with is the platform's and the umask's
	// business -- windows reports every writable directory as 0777 (the runner's log:
	// `the directory is -rwxrwxrwx, want 0755`) and a bench at `umask 077` would report
	// 0700. A sibling made by os.MkdirAll(0o755) in this same test runs on the same
	// platform under the same umask, so the two agree exactly when the tool asked for
	// the same thing, and the assertion stops being a bet on the runner.
	control := filepath.Join(filepath.Dir(missing), "control")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(control)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want.Mode().Perm() {
		t.Errorf("the directory is %v; a plain os.MkdirAll(0o755) on this platform and umask is %v, and quickstart makes its board the way nova-swarm's quickstart makes its pool", got, want.Mode().Perm())
	}
	if !strings.Contains(out.String(), "created=true") {
		t.Errorf("quickstart made the directory and did not say so; want created=true on the QUICKSTART OK line:\n%s", out.String())
	}

	// A SECOND RUN IS NOT A FIRST RUN. The same line, the other value: a reader who
	// pastes quickstart twice can tell which run made the board.
	out.Reset()
	errb.Reset()
	if exit := run([]string{"quickstart", "--dir", missing, "--stale", "10m"}, &out, &errb, time.Now().UTC(), &seq{}); exit != 0 {
		t.Fatalf("quickstart against the directory it just made exits %d, want 0; stderr: %s", exit, errb.String())
	}
	if !strings.Contains(out.String(), "created=false") {
		t.Errorf("a second quickstart found the directory already there and did not say so; want created=false:\n%s", out.String())
	}

	// A --dir that exists and is a FILE is still a refusal: MkdirAll would not fix it,
	// and a board is not a file this tool overwrites.
	file := filepath.Join(t.TempDir(), "board")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if exit := run([]string{"quickstart", "--dir", file, "--stale", "10m"}, &out, &errb, time.Now().UTC(), &seq{}); exit != 2 {
		t.Errorf("quickstart against a --dir that is a file exits %d, want 2", exit)
	}
	if !strings.Contains(errb.String(), "is a file") {
		t.Errorf("a --dir that is a file is not a directory this verb makes:\n%s", errb.String())
	}
}

// TestQuickstartRefusedMakesNothing is DeepSeek's read of nova-tools #109: quickstart is
// the one verb that makes its --dir, and it was making it in f.backend() BEFORE the rest
// of the line had been judged. A first run that fat-fingers --stale is exactly the run
// that has no board yet, and "making it would answer the typo with an empty board" -- a
// reader would then be looking at a directory the tool created on their behalf while
// refusing them. A REFUSED RUN LEAVES NOTHING BEHIND: every flag and every problem is
// found first, and only a line that will be obeyed is allowed to touch the filesystem.
func TestQuickstartRefusedMakesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"--stale missing", []string{"quickstart", "--dir", "", "--stale", ""}},
		{"--stale not a duration", []string{"quickstart", "--dir", "", "--stale", "10"}},
		{"--stale not a window", []string{"quickstart", "--dir", "", "--stale", "0s"}},
		{"a positional argument", []string{"quickstart", "--dir", "", "--stale", "10m", "board"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			missing := filepath.Join(t.TempDir(), "board")
			args := append([]string(nil), tc.args...)
			for i, a := range args {
				if a == "--dir" {
					args[i+1] = missing
				}
			}
			// --stale "" is the flag left off the line entirely, not given empty.
			for i := 0; i+1 < len(args); i++ {
				if args[i] == "--stale" && args[i+1] == "" {
					args = append(args[:i], args[i+2:]...)
					break
				}
			}
			var out, errb bytes.Buffer
			exit := run(args, &out, &errb, time.Now().UTC(), &seq{})
			if exit != 2 {
				t.Errorf("%v exits %d, want 2; stderr: %s", args, exit, errb.String())
			}
			if _, err := os.Stat(missing); !os.IsNotExist(err) {
				t.Errorf("%v was refused and still made %s (%v); a refused run makes no board", args, missing, err)
			}
		})
	}
}
