package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The onboarding standard (ONBOARDING.md), pinned for this binary: the usage banner's
// examples are RUN rather than read, every refusal a first run hits says what the flag
// WANTS and one run names every independent problem, and the README transcript is
// compared against what the tool actually prints.

// firstRunLab is a lab whose fixture holds one entry a first run can look at, so that the
// example sitting has something to say.
func firstRunLab(t *testing.T) *lab {
	l := newLab(t)
	oid := l.branch("rowan/twin-full-width-lanes", "lanes.md", "the change\n", "a change")
	l.host.PRs[949] = merge.PR{Number: 949, Author: "pat", Base: "main",
		HeadRef: "rowan/twin-full-width-lanes", HeadOID: oid, Mergeable: "MERGEABLE",
		URL: "https://example.invalid/949"}
	l.host.SetChecks(oid, 4, 1)
	l.host.SetChecks(l.baseSHA(), 4, 0)
	return l
}

// localize points an example or transcript command at this test's lane. The example says
// ./lane because a stranger types a path of their own; the test gives it one under
// t.TempDir(), and the repository resolves to the bare fixture repo.
//
// Three substitutions, and the last two are the REHEARSAL the first run is told to do
// first: a lane of its own (rehearsing into the live lane's directory would be INIT
// REFUSED on the line after), and "$PWD/rehearsal.git", which is the absolute path a
// shell hands the tool -- git runs inside the lane directory, so a relative --remote
// resolves against the lane and is refused. The documented line is substituted, never
// rewritten: what runs here is what a reader pastes.
func (l *lab) localize(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		switch a {
		case "./lane":
			out[i] = l.lane
		case "./rehearsal-lane":
			out[i] = filepath.Join(l.dir, "rehearsal-lane")
		case `"$PWD/rehearsal.git"`, "$PWD/rehearsal.git":
			out[i] = l.rehearsalRemote()
		}
	}
	return out
}

// rehearsalRemote is `git init -q --bare ./rehearsal.git`: a bare repository of the
// reader's own, with no branches in it at all, which is what the rehearsal's
// base_state=UNKNOWN and its one STATUS NOTE are about.
func (l *lab) rehearsalRemote() string {
	l.t.Helper()
	path := filepath.Join(l.dir, "rehearsal.git")
	if _, err := os.Stat(path); err != nil {
		l.git(l.dir, "init", "-q", "--bare", path)
	}
	return path
}

func exampleLines(t *testing.T, l *lab) []string {
	t.Helper()
	exit, stdout, stderr := l.run("help")
	if exit != 0 {
		t.Fatalf("`nova-merge help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	}
	lines, err := onboarding.ExampleLines(stdout, "nova-merge")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, stdout)
	}
	return lines
}

// (a) The usage banner ends in an `example:` block of lines that ACTUALLY RUN. "Run" is
// this repo's exit law: 0 or 1 is an answer and 2 is "could not run".
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()
	l := firstRunLab(t)
	exs := exampleLines(t, l)
	if len(exs) != 5 {
		t.Fatalf("want the five-line sitting under `example:`, got %d: %q", len(exs), exs)
	}
	for _, ex := range exs {
		args := l.localize(strings.Fields(ex)[1:])
		exit, stdout, stderr := l.run(args...)
		if exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr)
			continue
		}
		if stdout == "" && stderr == "" {
			t.Errorf("the usage example %q printed nothing", ex)
		}
	}
}

// (b) A refusal says what the flag or input WANTS, not only what was wrong.
func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"init with no lane", []string{"init", "--repo", "o/n", "--base", "main", "--lane-branch", "l"}, "the lane's own directory"},
		{"init with no repo", []string{"init", "--lane", l.lane, "--base", "main", "--lane-branch", "l"}, "as <owner>/<name>"},
		{"read with no head", []string{"read", "--lane", l.lane, "--pr", "9", "--who", "emma", "--verdict", "approve"}, "the full 40-character sha the reader had open"},
		{"read with a short head", []string{"read", "--lane", l.lane, "--pr", "9", "--who", "emma", "--head", "cbde1fc6ba10", "--verdict", "approve"}, "a truncated sha might name the wrong commit"},
		{"gate with no base-sha", []string{"gate", "--lane", l.lane, "--pr", "9", "--head", strings.Repeat("a", 40), "--merge", strings.Repeat("c", 40), "--verdict", "green", "--summary", "x"}, "--base-sha is required"},
		{"gate with no merge", []string{"gate", "--lane", l.lane, "--pr", "9", "--head", strings.Repeat("a", 40), "--base-sha", strings.Repeat("b", 40), "--verdict", "green", "--summary", "x"}, "--merge is required"},
		{"run with neither once nor loop", []string{"run", "--lane", l.lane}, "--once or --loop <duration> is required"},
		{"loop with no hours", []string{"run", "--lane", l.lane, "--loop", "5m"}, "--loop requires --hours"},
		{"packet with no who", []string{"packet", "--lane", l.lane, "--all"}, "the reader this packet is for"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := l.run(tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stderr: %s", exit, stderr)
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

// (b), the other half: ONE RUN NAMES EVERY PROBLEM IT CAN FIND. A caller can fix two
// things as easily as one, and sending a first run back three times for three independent
// flags is three refusals the first one already knew about.
func TestIndependentProblemsAreReportedInOneRun(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"init with nothing at all", []string{"init"}, []string{"--lane is required", "--repo is required", "--base is required", "--lane-branch is required"}},
		{"gate with a bad sha and no summary", []string{"gate", "--lane", l.lane, "--pr", "9", "--head", "abc", "--base-sha", "def", "--merge", "ghi", "--verdict", "green"},
			[]string{"--head wants a full 40-character sha", "--base-sha wants a full 40-character sha", "--merge wants a full 40-character sha", "--summary is required"}},
		{"read with nothing", []string{"read", "--lane", l.lane}, []string{"--pr <n> or --branch <name> is required", "--who is required", "--head is required", "--verdict is approve or hold"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, _, stderr := l.run(tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stderr: %s", exit, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("one run must name every problem it can find; %q is missing from:\n%s", want, stderr)
				}
			}
		})
	}
}

// (c) The README's `### First run` transcript, checked against the tool: every transcript
// line must match a line the tool actually printed, by event prefix and field names in
// order. Shas, paths and counts are a run's own business and are deliberately not
// compared, so the transcript stays a document rather than becoming a fixture.
func TestREADMEFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-merge")
	if err != nil {
		t.Fatal(err)
	}
	l := firstRunLab(t)
	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ "); ok {
			exit, stdout, stderr := l.run(l.localize(strings.Fields(cmd)[1:])...)
			if exit == 2 {
				t.Fatalf("the README command %q does not run: exit 2, stderr: %s", line, stderr)
			}
			printed = map[string]bool{}
			for _, out := range strings.Split(stdout+"\n"+stderr, "\n") {
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
			t.Errorf("README line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
		seen[strings.Join(strings.Fields(s)[:2], " ")]++
	}
	// Two INIT OK and three STATUS OK: the REHEARSAL against a bare repository of the
	// reader's own comes first (nova-tools #116), and then the live form. A transcript
	// that shows the live push without the rehearsal in front of it is the document
	// Johnny pasted.
	for prefix, want := range map[string]int{"INIT OK": 2, "STATUS OK": 3, "STATUS NOTE": 1, "ADD OK": 1, "STATUS ENTRY": 1} {
		if seen[prefix] != want {
			t.Errorf("docs/CLI.md First run shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}

// THE FIRST RUN IS A PUSH, AND THE DOCUMENT A STRANGER PASTES FROM HAS TO SAY SO.
//
// Johnny, dogfooding v0.12.0 (nova-tools #116, 2026-09-12): he pasted the README's first
// run at a live repository, substituting a throwaway `--lane-branch` so he would not join
// the family lane, and it created and pushed `johnny-dogfood/does-not-exist-on-purpose`
// to `mas-bandwidth/nova-tools` (commit 913ccf0b, author `nova-merge@localhost`). He
// deleted it the same minute. "No prompt. The help line `joined=false` does not say
// 'pushed to origin'."
//
// The push is what SPEC-MERGE.md demands, not a bug: rule 22 puts every read and gate in
// the lane's own branch of the repository, and demanded test 20 has `init` check out that
// branch "created with `.gitignore` on the fake remote, `joined=false`; a second lane on
// the same branch prints `joined=true` and creates nothing" -- a branch a second machine
// can join is a branch at the remote. So the repair is the document, and these two tests
// are the pair: the first pins the push as behaviour, the second pins that the first run's
// prose says it in words, above the line a stranger copies.

// laneBranchesAt lists the refs under refs/heads/ that the bare fixture repository holds.
func (l *lab) branchesAtRemote() []string {
	l.t.Helper()
	out := l.git(l.remote, "for-each-ref", "--format=%(refname)", "refs/heads/")
	if strings.TrimSpace(out) == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestTheFirstRunPushesTheLaneBranchToTheRepository(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	const branch = "nova-merge/main"
	exit, stdout, stderr := l.run("quickstart", "--lane", l.lane,
		"--repo", "mas-bandwidth/nova-tools", "--base", "main", "--lane-branch", branch)
	if exit != 0 {
		t.Fatalf("quickstart: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "joined=false")

	ref := "refs/heads/" + branch
	if got := l.branchesAtRemote(); !containsString(got, ref) {
		t.Fatalf("the first run left no %s at the remote; the refs there are %q.\nIf this tool has stopped pushing the record branch, rule 22 has no transport and docs/CLI.md's `### First run` is now wrong in the other direction", ref, got)
	}
	if got := l.pushes(); !anyHasPrefix(got, ref+" ") {
		t.Errorf("the remote's update hook recorded %q; the first run's push of %s is not among them", got, ref)
	}
	// The commit the first run leaves in a shared repository, named exactly as the
	// README now names it -- Johnny read this author off the commit he had to delete.
	who := l.git(l.remote, "log", "-1", "--format=%an <%ae>%n%cn <%ce>%n%s", ref)
	for _, want := range []string{"nova-merge <nova-merge@localhost>", "nova-merge: the lane's record branch"} {
		contains(t, who, want)
	}
}

// firstRunProse is docs/CLI.md's `## nova-merge` -> `### First run`, up to the first line a
// stranger copies. What is AFTER that line was not read before the push happened.
func firstRunProse(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	section, ok := onboarding.Section(string(raw), "nova-merge")
	if !ok {
		t.Fatal("docs/CLI.md has no `## nova-merge` section")
	}
	_, block, ok := strings.Cut(section, onboarding.FirstRunHeading+"\n")
	if !ok {
		t.Fatalf("docs/CLI.md `## nova-merge` has no `%s`", onboarding.FirstRunHeading)
	}
	paste, _, ok := strings.Cut(block, "$ nova-merge quickstart")
	if !ok {
		t.Fatal("docs/CLI.md `## nova-merge` `### First run` holds no `$ nova-merge quickstart` line; this test is looking in the wrong place")
	}
	return strings.ToLower(paste)
}

func TestTheFirstRunSectionSaysThatTheFirstRunPushes(t *testing.T) {
	t.Parallel()
	prose := firstRunProse(t)
	for _, want := range []struct{ phrase, because string }{
		{"pushes", "the first run is a push, and the word has to be in the prose"},
		{"origin", "a push says where it lands"},
		{"--lane-branch", "the ref it creates is the one the reader typed"},
		{"nova-merge@localhost", "the commit a shared repository keeps carries this author (nova-tools #116)"},
		{"placeholder", "and that author is a placeholder identity rather than a person (Stella, 2026-09-12)"},
		{"--remote", "the rehearsal form: a bare repository of your own, nothing reaching the host"},
		{`"$pwd/rehearsal.git"`, "the rehearsal's --remote is ABSOLUTE, because a relative one resolves against the lane and is refused"},
		{"./rehearsal-lane", "and the rehearsal gets a lane of its own, or the live line after it is INIT REFUSED"},
	} {
		if !strings.Contains(prose, want.phrase) {
			t.Errorf("docs/CLI.md's nova-merge `### First run`, above the line a stranger copies, never says %q: %s.\nwhat it says there:\n%s", want.phrase, want.because, prose)
		}
	}
}

// And the BANNER says it, on the page Johnny actually read: his complaint was that the
// help line `joined=false` does not say "pushed to origin" (nova-tools #116). Stella,
// 2026-09-12: the rehearsal form has to work end to end in help, the CLI and TESTS.md with
// an absolute remote, and the placeholder identity has to be named as one.
func TestTheUsageBannerSaysThatTheFirstRunPushes(t *testing.T) {
	t.Parallel()
	l := firstRunLab(t)
	exit, banner, stderr := l.run("help")
	if exit != 0 {
		t.Fatalf("help: exit %d; stderr: %s", exit, stderr)
	}
	for _, want := range []struct{ phrase, because string }{
		{"pushes it to", "the banner is where joined=false was read, so the push is named there too"},
		{"origin of the repository --repo names", "a push says where it lands"},
		{"nova-merge <nova-merge@localhost>", "the author a shared repository keeps"},
		{"PLACEHOLDER", "which is a placeholder identity, not a person"},
		{"REHEARSE FIRST", "the rehearsal comes before the live line here as well"},
		{"git init -q --bare ./rehearsal.git", "the whole rehearsal, runnable"},
		{`--remote "$PWD/rehearsal.git"`, "with the absolute path a relative --remote does not give"},
		{"--lane ./rehearsal-lane", "and a lane of its own, or the live line after it is INIT REFUSED"},
	} {
		if !strings.Contains(banner, want.phrase) {
			t.Errorf("the usage banner never says %q: %s", want.phrase, want.because)
		}
	}
	// The rehearsal the banner prints is the one TESTS.md runs: same flags, same
	// spellings. A banner that drifts from the executed transcript is two first runs.
	for _, flag := range []string{"--lane ./rehearsal-lane", "--lane-branch nova-merge/main", `--remote "$PWD/rehearsal.git"`} {
		if !strings.Contains(rehearsalTranscriptLine(t), flag) {
			t.Errorf("docs/TESTS.md's rehearsal line does not carry %q, which the banner prints", flag)
		}
	}
}

// rehearsalTranscriptLine is the `$ nova-merge quickstart ... --remote ...` line of
// docs/TESTS.md's nova-merge `### First run` -- the one the transcript test EXECUTES.
// docs/CLI.md carries a twin of it that nothing runs, held to this one by the phrase
// assertions above, which is why they name the flags rather than only the word "remote".
func rehearsalTranscriptLine(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-merge")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "$ nova-merge quickstart") && strings.Contains(line, "--remote") {
			return line
		}
	}
	t.Fatal("docs/TESTS.md's nova-merge `### First run` holds no quickstart line with --remote; the rehearsal is not in the transcript that gets executed")
	return ""
}

// docs/TESTS.md carries the same transcript, and it is the one the test above executes, so the
// warning belongs on both pages: a reader who pastes from the ledger pastes the same push.
func TestTheTestsLedgerFirstRunSaysThatTheFirstRunPushes(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	section, ok := onboarding.Section(string(raw), "nova-merge")
	if !ok {
		t.Fatal("docs/TESTS.md has no `## nova-merge` section")
	}
	paste, _, ok := strings.Cut(section, "$ nova-merge quickstart")
	if !ok {
		t.Fatal("docs/TESTS.md `## nova-merge` holds no `$ nova-merge quickstart` line")
	}
	if !strings.Contains(strings.ToLower(paste), "pushes") {
		t.Errorf("docs/TESTS.md's nova-merge first run does not say the first run pushes, above the line it invites a reader to run:\n%s", paste)
	}
}

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func anyHasPrefix(hay []string, prefix string) bool {
	for _, s := range hay {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
