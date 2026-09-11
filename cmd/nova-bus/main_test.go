package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const rosterJSON = `{
  "participants": [
    {"name": "Ada", "lane": "from-ada", "aliases": ["Ada Vale", "the archivist"],
     "git_name": "Ada", "git_email": "ada@example.com"},
    {"name": "Bo", "lane": "from-bo", "aliases": ["Bo Quill"],
     "git_name": "Bo", "git_email": "bo@example.com"},
    {"name": "Dana"}
  ],
  "groups": [{"name": "Everybody on the bus", "members": ["Ada", "Bo", "Dana"]}]
}`

func now() time.Time {
	t, err := time.Parse(time.RFC3339, "2026-09-09T12:34:56Z")
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// TestMain sets the hermetic git environment ONCE, for the whole process, and hermetic is
// what each test says to declare that it depends on it.
//
// It used to be four t.Setenv calls inside hermetic, which is the right shape for a test
// that runs alone and the one shape a parallel test may not have: t.Setenv panics in any
// test that has called t.Parallel, because the environment is process-wide and restoring it
// per test is not a thing that can be done while another test is reading it. Every test in
// this package shells out to git, so that one call made the whole package serial -- 96
// tests, each paying a git init, a clone, a commit and a push, one after another. The
// environment set here is identical and set in the one place where nothing is running yet.
//
// What it is FOR is unchanged: git ignores the machine's own configuration, so these tests
// do not depend on the ~/.gitconfig of whoever runs them -- including a commit.gpgsign that
// would otherwise make them hang on a key. The global config is a file of our own rather
// than a missing one, and it turns off git's auto-maintenance: `git receive-pack` starts
// `git gc --auto` after every push and does not wait for it, and a git that detaches that
// child before deciding whether there is work to do leaves it running in a fixture the test
// is about to remove. See the same comment on internal/bus's, where the race was found.
//
// The directory holding that config is the ONE thing in this package outside t.TempDir, and
// it has to be: it must exist before the first test starts and outlive the last one. It is
// created under the same TMPDIR t.TempDir uses, it holds one file this process wrote, and
// it is removed before the process exits on every path including a failing run.
func TestMain(m *testing.M) {
	os.Exit(func() int {
		dir, err := os.MkdirTemp("", "nova-bus-test-gitconfig-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "hermetic git config: %v\n", err)
			return 2
		}
		defer os.RemoveAll(dir)
		cfg := filepath.Join(dir, "gitconfig")
		if err := os.WriteFile(cfg, []byte(noMaintenanceConfig), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "hermetic git config: %v\n", err)
			return 2
		}
		os.Setenv("GIT_CONFIG_GLOBAL", cfg)
		os.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(dir, "no-such-gitconfig"))
		os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		os.Setenv("GIT_TERMINAL_PROMPT", "0")
		return m.Run()
	}())
}

// noMaintenanceConfig is the whole of that global config: no auto-gc anywhere, and if some
// git runs one anyway it runs in the foreground, where the call that started it waits for
// it and nothing outlives the test.
const noMaintenanceConfig = "[gc]\n\tauto = 0\n\tautoDetach = false\n[maintenance]\n\tauto = false\n[receive]\n\tautoGc = false\n"

// hermetic is the call each git-running test keeps, and it asserts what TestMain set rather
// than setting it. Kept as a call rather than deleted so that the dependency stays written
// at every site that has it, and so that a future TestMain that stopped doing this would
// fail loudly here instead of silently reading the runner's ~/.gitconfig.
func hermetic(t *testing.T) {
	t.Helper()
	// All four, not just the first: three of them are what keeps a machine's system
	// config, its ~/.gitconfig and its credential prompt out of these tests, and an
	// assertion on one of four would pass over a TestMain that set one of four.
	for _, key := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s is not set: the hermetic git environment is TestMain's, in this package, and it sets four", key)
		}
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bus builds a bare remote and one checkout of it, with a roster and two notes from
// Bo already on the bus: one carrying a question, one a bare acknowledgement.
func busDir(t *testing.T) (checkout, bare string) {
	t.Helper()
	bare = filepath.Join(t.TempDir(), "bus.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, "init", "--bare", "--quiet", "--initial-branch=main")
	checkout = filepath.Join(t.TempDir(), "checkout")
	gitIn(t, filepath.Dir(checkout), "clone", "--quiet", bare, checkout)
	gitIn(t, checkout, "checkout", "-q", "-B", "main")
	writeFile(t, checkout, "participants.json", rosterJSON)
	writeFile(t, checkout, "from-bo/2026-09-07T0001Z-a-question-abcdef012345.md",
		"From: Bo Quill\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-abcdef012345\nSubject: A question about the gate\n\nShould the gate run on the merge queue too?\n")
	writeFile(t, checkout, "from-bo/2026-09-07T0002Z-heard-111111111111.md",
		"From: Bo\nTo: Ada\nDate: Mon Sep  7 00:02:00 UTC 2026\nId: bo-111111111111\nSubject: Heard\n\nHeard, thank you.\n")
	// The lane's catalogue, which send would have written. A bus whose notes are not in
	// an INDEX is a bus check --full warns about, so the fixture is a bus in the shape
	// this tool leaves one in -- and TestCheckFullWarnsAboutANoteWithNoIndexLine covers the
	// other shape deliberately.
	writeFile(t, checkout, "from-bo/INDEX", strings.Join([]string{
		"bo-abcdef012345\tfrom-bo/2026-09-07T0001Z-a-question-abcdef012345.md\t2026-09-07T00:01:00Z\tAda\t-",
		"bo-111111111111\tfrom-bo/2026-09-07T0002Z-heard-111111111111.md\t2026-09-07T00:02:00Z\tAda\t-",
	}, "\n")+"\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "the bus")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	return checkout, bare
}

type result struct {
	code   int
	stdout string
	stderr string
}

func invoke(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut, now())
	return result{code, out.String(), errOut.String()}
}

func (r result) mustCode(t *testing.T, want int) result {
	t.Helper()
	if r.code != want {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, want, r.stdout, r.stderr)
	}
	return r
}

func (r result) mustContain(t *testing.T, stream, want string) result {
	t.Helper()
	got := r.stdout
	name := "stdout"
	if stream == "stderr" {
		got, name = r.stderr, "stderr"
	}
	if !strings.Contains(got, want) {
		t.Fatalf("%s does not contain %q:\n%s", name, want, got)
	}
	return r
}

const draft = `From: Ada (day shift, the west host, the shared account)
To: Bo
Cc: Dana
Re: bo-abcdef012345
Subject: Yes, on the merge queue too

Bo,

Yes, and the key is misspelled in the matrix.
`

func TestUsageAndUnknownVerb(t *testing.T) {
	t.Parallel()
	invoke(t, "").mustCode(t, 2).mustContain(t, "stderr", "nova-bus:")
	invoke(t, "", "help").mustCode(t, 0).mustContain(t, "stdout", "usage:")
	invoke(t, "", "wibble").mustCode(t, 2).mustContain(t, "stderr", `unknown subcommand "wibble"`)
}

// Every required flag, refused by name. A missing one is never a guess.
func TestRefusingToGuess(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"send without --bus", []string{"send", "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3"}, "--bus is required"},
		{"send without --remote", []string{"send", "--bus", checkout, "--stdin", "--branch", "main", "--attempts", "3"}, "--remote is required"},
		{"send without --branch", []string{"send", "--bus", checkout, "--stdin", "--remote", "origin", "--attempts", "3"}, "--branch is required"},
		{"send with attempts 0", []string{"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "0"}, "--attempts is a number of tries and is at least 1"},
		{"send with neither --file nor --stdin", []string{"send", "--bus", checkout, "--remote", "origin", "--branch", "main", "--attempts", "3"}, "exactly one of --file and --stdin"},
		{"send with both", []string{"send", "--bus", checkout, "--stdin", "--file", "x.md", "--remote", "origin", "--branch", "main", "--attempts", "3"}, "exactly one of --file and --stdin"},
		{"inbox without --as", []string{"inbox", "--bus", checkout, "--receipt-max-words", "40"}, "--as is required"},
		{"inbox without --receipt-max-words", []string{"inbox", "--bus", checkout, "--as", "Ada"}, "--receipt-max-words must be given"},
		{"receipt without --note", []string{"receipt", "--bus", checkout, "--as", "Ada", "--remote", "origin", "--branch", "main", "--attempts", "3"}, "--note is required"},
		{"check without --bus", []string{"check", "--full"}, "--bus is required"},
		{"names without --bus", []string{"names"}, "--bus is required"},
		{"a positional argument", []string{"check", "--bus", checkout, "--full", "extra"}, "takes no positional arguments"},
		{"an unknown flag", []string{"check", "--bus", checkout, "--full", "--wibble"}, "nova-bus check:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invoke(t, "", tc.args...).mustCode(t, 2).mustContain(t, "stderr", tc.want)
		})
	}
}

// -h on a verb is an unusable invocation, not a success. A caller gating on exit 0 must
// never see one from a flag it mistyped.
func TestVerbHelpIsRefusedNotAnswered(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	invoke(t, "", "check", "--bus", checkout, "--full", "-h").mustCode(t, 2)
}

func TestSendLandsANoteAndCheckPasses(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	r := invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-").
		mustContain(t, "stdout", "pushed=true attempts=1")
	if !strings.Contains(r.stdout, "path=from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-") {
		t.Fatalf("the path is not the bus's naming convention plus the id: %s", r.stdout)
	}
	// It is on the REMOTE, not merely committed.
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-") {
		t.Fatalf("the note is not on the remote:\n%s", files)
	}
	// The commit is the sender's identity, from the roster.
	if who := strings.TrimSpace(gitIn(t, bare, "log", "-1", "--format=%an <%ae>", "main")); who != "Ada <ada@example.com>" {
		t.Fatalf("the note was committed as %q", who)
	}
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
	// And the question it answers is no longer open.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=1 open=1 notes=0 receipts=1")
}

func TestSendFromAFile(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	path := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(path, []byte(draft), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke(t, "", "send", "--bus", checkout, "--file", path, "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).mustContain(t, "stdout", "SEND OK id=ada-")
}

func TestSendNoPushCommitsAndSaysTheNoteIsNotOnTheBus(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 0).mustContain(t, "stdout", "pushed=false")
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if strings.Contains(files, "from-ada/") {
		t.Fatalf("--no-push pushed:\n%s", files)
	}
}

// A refusal is exit 1 and a SEND FAIL line, and the bus is untouched.
func TestSendRefusesAndWritesNothing(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	cases := []struct{ name, draft, want string }{
		{"an unknown recipient", "From: Ada\nTo: Boe\nSubject: s\n\nbody\n", `"Boe"`},
		{"an unknown sender", "From: Nobody\nTo: Ada\nSubject: s\n\nbody\n", "names no one on this bus"},
		{"a sender with no lane", "From: Dana\nTo: Ada\nSubject: s\n\nbody\n", "has no lane"},
		{"an author-written Id", "From: Ada\nTo: Bo\nId: ada-000000000000\nSubject: s\n\nbody\n", "already carries an Id line"},
		{"a Re naming a slug", "From: Ada\nTo: Bo\nRe: from-bo/renamed.md\nSubject: s\n\nbody\n", "a slug is not a thread"},
		{"a misspelled header key", "From: Ada\nTo: Bo\nSbuject: s\n\nbody\n", `unknown header key "Sbuject"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invoke(t, tc.draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
				mustCode(t, 1).
				mustContain(t, "stderr", "SEND FAIL (stdin): ").
				mustContain(t, "stderr", tc.want)
			if entries, err := os.ReadDir(filepath.Join(checkout, "from-ada")); err == nil && len(entries) > 0 {
				t.Fatalf("a refused draft left %d files in the lane", len(entries))
			}
		})
	}
}

// The one-line guarantee, at the place a caller's own text reaches a refusal.
func TestARefusalIsOneLineWhateverTheDraftHolds(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	// U+2028 ends a line for every reader that follows Unicode rather than counting
	// newlines, so an unresolved recipient holding one could otherwise forge a second line.
	r := invoke(t, "From: Ada\nTo: Bo\u2028Nobody\nSubject: s\n\nbody\n",
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1)
	if !strings.Contains(r.stderr, `\u2028`) {
		t.Fatalf("the separator was not escaped:\n%q", r.stderr)
	}
	if n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n"); n != 0 {
		t.Fatalf("the refusal is %d lines, want one:\n%q", n+1, r.stderr)
	}
}

func TestInboxSeparatesNotesFromReceiptsAndPutsNotesFirst(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "the archivist", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX NOTE id=bo-abcdef012345 from=Bo addr=to").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2 open=2 notes=1 receipts=1")
	noteAt := strings.Index(r.stdout, "INBOX NOTE")
	receiptAt := strings.Index(r.stdout, "INBOX RECEIPT")
	if noteAt < 0 || receiptAt < 0 || noteAt > receiptAt {
		t.Fatalf("the notes that carry something must be listed before the bare receipts:\n%s", r.stdout)
	}
}

func TestInboxRefusesANameItDoesNotKnow(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Adda", "--receipt-max-words", "40").
		mustCode(t, 2).mustContain(t, "stderr", "names no one on this bus")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Dana", "--receipt-max-words", "40").
		mustCode(t, 2).mustContain(t, "stderr", "has no lane")
}

func TestReceiptMarksHeardAndInboxHonoursIt(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).mustContain(t, "stdout", "RECEIPT OK recorded=1 already=0")
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(files, "from-ada/RECEIPTS") {
		t.Fatalf("the receipt is not on the remote:\n%s", files)
	}
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "open=1 notes=0 receipts=1")
	// Twice is reported, not written twice, and needs no commit.
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "RECEIPT ALREADY note=bo-abcdef012345").
		mustContain(t, "stdout", "RECEIPT OK recorded=0 already=1 commit=- pushed=false attempts=0")
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0)
}

func TestReceiptRefuses(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-deadbeefcafe",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1).mustContain(t, "stderr", "RECEIPT FAIL")
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Adda", "--note", "bo-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).mustContain(t, "stderr", "names no one on this bus")
}

func TestCheckFailsAndNamesEveryFinding(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	writeFile(t, checkout, "from-ada/broken.md", "From: Ada\nthis is prose\n\nbody\n")
	writeFile(t, checkout, "from-ada/stranger.md", "From: Ada\nTo: Boe\nSubject: s\n\nbody\n")
	r := invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-ada/broken.md: ").
		mustContain(t, "stderr", "BUS FAIL from-ada/stranger.md")
	if n := strings.Count(r.stderr, "BUS FAIL"); n < 2 {
		t.Fatalf("check reported %d findings over two broken files:\n%s", n, r.stderr)
	}
	// A failing check says on stdout what it WALKED and nothing else: no OK line, no
	// count that a caller could read as a pass. The scope line is there whether the run
	// passed or failed, because "which files did you look at" is the first thing a person
	// reading a failure asks.
	if strings.Contains(r.stdout, "BUS OK") {
		t.Fatalf("a failing check printed an OK line: %q", r.stdout)
	}
	if got := strings.TrimSpace(r.stdout); got != "BUS SCOPE mode=full cursor=- changed=0" {
		t.Fatalf("a failing check wrote more than its scope to stdout: %q", r.stdout)
	}
}

func TestCheckRefusesABusWithNoRoster(t *testing.T) {
	t.Parallel()
	invoke(t, "", "check", "--bus", t.TempDir(), "--full").mustCode(t, 2).mustContain(t, "stderr", "participants.json")
}

func TestNamesEchoesTheRoster(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	invoke(t, "", "names", "--bus", checkout).mustCode(t, 0).
		mustContain(t, "stdout", `NAMES NAME name="Ada" lane=from-ada aliases="Ada Vale";"the archivist"`).
		mustContain(t, "stdout", `NAMES NAME name="Dana" lane=- aliases=-`).
		mustContain(t, "stdout", `NAMES GROUP name="Everybody on the bus" members="Ada";"Bo";"Dana"`).
		mustContain(t, "stdout", "NAMES OK participants=3 groups=1 senders=2")
}

// send refuses to run over a checkout that is not on the branch named, or that holds
// somebody's unrelated work, because the retry loop rebases.
func TestSendRefusesAWrongBranchOrADirtyCheckout(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "trunk", "--attempts", "3").
		mustCode(t, 1).mustContain(t, "stderr", `is on branch "main", not "trunk"`)
	writeFile(t, checkout, "stray.txt", "unrelated work in flight\n")
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1).mustContain(t, "stderr", "stray.txt")
}

func TestSendRefusesABusThatIsNotARepository(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "participants.json", rosterJSON)
	invoke(t, draft, "send", "--bus", root, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2)
}

// A push publishes the BRANCH. send must refuse a checkout carrying commits it did not
// make, before it writes the note, because otherwise a note's push publishes somebody
// else's unfinished work and nothing in the output says so.
func TestSendRefusesWhenTheBranchIsAheadOfTheRemote(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	writeFile(t, checkout, "notes-to-self.txt", "half a thought\n")
	gitIn(t, checkout, "add", "notes-to-self.txt")
	gitIn(t, checkout, "-c", "user.name=Someone", "-c", "user.email=someone@example.com", "commit", "-q", "-m", "wip")

	r := invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1).
		mustContain(t, "stderr", "SEND REFUSED: ").
		mustContain(t, "stderr", "ahead of origin/main by 1 commits, 1 of which the tool did not make").
		mustContain(t, "stderr", "git pull --rebase && git push")
	if strings.Contains(r.stdout, "SEND OK") {
		t.Fatalf("a refused send reported success: %s", r.stdout)
	}
	// Nothing was written and nothing was published.
	if entries, err := os.ReadDir(filepath.Join(checkout, "from-ada")); err == nil && len(entries) > 0 {
		t.Fatalf("a refused send left %d files in the lane", len(entries))
	}
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); strings.Contains(files, "notes-to-self.txt") {
		t.Fatalf("the unrelated commit was published:\n%s", files)
	}
}

// --remote and --branch become git's own argv. A value beginning with a dash is an option
// to git, not a name, and this tool would have run it.
func TestRemoteAndBranchThatCouldBeOptionsAreRefused(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	cases := [][]string{
		{"send", "--bus", checkout, "--stdin", "--remote", "--upload-pack=touch /tmp/pwned", "--branch", "main", "--attempts", "3"},
		{"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "--exec=id", "--attempts", "3"},
		{"receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-abcdef012345", "--remote", "origin;id", "--branch", "main", "--attempts", "3"},
	}
	for _, args := range cases {
		invoke(t, draft, args...).mustCode(t, 2).mustContain(t, "stderr", "nova-bus ")
	}
}

// --slug is the one piece of a note's path a caller supplies. It was written into the
// filename unchecked, so "../x" wrote outside the lane.
func TestSendRefusesASlugThatIsNotASlug(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	for _, slug := range []string{"../x", "a/b", "a\nb", " "} {
		r := invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--slug", slug).
			mustCode(t, 1).mustContain(t, "stderr", "--slug")
		if n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n"); n != 0 {
			t.Fatalf("the refusal is %d lines, want one:\n%q", n+1, r.stderr)
		}
	}
	// Nothing reached the lane, and nothing reached the bus root either.
	if entries, err := os.ReadDir(filepath.Join(checkout, "from-ada")); err == nil && len(entries) > 0 {
		t.Fatalf("a refused --slug left %d files in the lane", len(entries))
	}
	// A slug that is a slug still works.
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--slug", "ci-gate").
		mustCode(t, 0).mustContain(t, "stdout", "-ci-gate-")
}

// A note on the bus that will not parse is not a note that does not exist. inbox used to
// step over it in silence, which is the same failure as a lost push with a quieter cause.
func TestInboxNamesTheNotesItCannotRead(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	writeFile(t, checkout, "from-bo/2026-09-07T0009Z-prose.md",
		"Ada, the checkpoint is pushed and the suite passed: zero divergence.\n\nMore prose.\n")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX UNREADABLE path=from-bo/2026-09-07T0009Z-prose.md: ").
		mustContain(t, "stdout", "unreadable=1")
	// The reason is short: a paragraph up to its first colon is not a header key.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "...")
}

// Heard is not answered. A note I receipted and never replied to is still owed an answer,
// and the receipt that says it arrived must not make it disappear from the listing.
func TestInboxShowsWhatWasHeardButNotAnswered(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX HEARD id=bo-abcdef012345 from=Bo addr=to").
		mustContain(t, "stdout", "heard=1")
	// It is out of the open count, and between the notes and the bare receipts.
	if !strings.Contains(r.stdout, "open=1 notes=0 receipts=1 heard=1") {
		t.Fatalf("a heard note is still counted as open:\n%s", r.stdout)
	}
	heardAt, receiptAt := strings.Index(r.stdout, "INBOX HEARD"), strings.Index(r.stdout, "INBOX RECEIPT")
	if heardAt < 0 || receiptAt < 0 || heardAt > receiptAt {
		t.Fatalf("HEARD must sit between the notes and the bare receipts:\n%s", r.stdout)
	}
	// And a note that was actually REPLIED to is gone, not merely heard.
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	r = invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").mustCode(t, 0)
	if strings.Contains(r.stdout, "bo-abcdef012345") {
		t.Fatalf("an answered note is still listed:\n%s", r.stdout)
	}
}

// The adoption path: a bus written by hand for months, checked for the first time.
func TestCheckLegacyBefore(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	writeFile(t, checkout, "from-bo/2026-09-01T0001Z-old-prose.md",
		"Ada, this note predates the tool entirely.\n\nbody\n")
	writeFile(t, checkout, "from-bo/2026-09-08T0001Z-new-prose.md",
		"Ada, this one does not.\n\nbody\n")

	// Without the flag, both fail and the run fails.
	r := invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 1)
	if n := strings.Count(r.stderr, "BUS FAIL"); n != 2 {
		t.Fatalf("check reported %d failures, want 2:\n%s", n, r.stderr)
	}

	// With it, the old one warns, the new one still fails, and the run still fails.
	r = invoke(t, "", "check", "--bus", checkout, "--full", "--legacy-before", "2026-09-05").mustCode(t, 1).
		mustContain(t, "stdout", "BUS WARN from-bo/2026-09-01T0001Z-old-prose.md").
		mustContain(t, "stderr", "BUS FAIL from-bo/2026-09-08T0001Z-new-prose.md")
	if strings.Contains(r.stderr, "BUS WARN") {
		t.Fatalf("a warning reached stderr, where only failures and refusals go:\n%s", r.stderr)
	}
	if n := strings.Count(r.stderr, "BUS FAIL"); n != 1 {
		t.Fatalf("check failed %d findings, want only the one after the cutoff:\n%s", n, r.stderr)
	}

	// With only the old one left, the run passes and SAYS how much it forgave.
	if err := os.Remove(filepath.Join(checkout, "from-bo", "2026-09-08T0001Z-new-prose.md")); err != nil {
		t.Fatal(err)
	}
	invoke(t, "", "check", "--bus", checkout, "--full", "--legacy-before", "2026-09-05").mustCode(t, 0).
		mustContain(t, "stdout", "BUS OK").
		mustContain(t, "stdout", "warn=1")
	// A clean bus says warn=0, so a run that forgave nothing and one that forgave fifty
	// notes are not the same line.
	if err := os.Remove(filepath.Join(checkout, "from-bo", "2026-09-01T0001Z-old-prose.md")); err != nil {
		t.Fatal(err)
	}
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0).mustContain(t, "stdout", "warn=0")
	// A line it cannot read is a bad invocation, not a guess.
	invoke(t, "", "check", "--bus", checkout, "--full", "--legacy-before", "last Tuesday").
		mustCode(t, 2).mustContain(t, "stderr", "neither a UTC date")
}
