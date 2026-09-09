package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const rosterJSON = `{
  "participants": [
    {"name": "Rowan", "lane": "from-rowan", "aliases": ["Rowan Claude", "the keeper"],
     "git_name": "Rowan", "git_email": "rowan@mas-bandwidth.com"},
    {"name": "Stella", "lane": "from-stella", "aliases": ["Stella Codex"],
     "git_name": "Stella", "git_email": "stella@mas-bandwidth.com"},
    {"name": "Glenn"}
  ],
  "groups": [{"name": "Everybody at the table", "members": ["Rowan", "Stella", "Glenn"]}]
}`

func now() time.Time {
	t, err := time.Parse(time.RFC3339, "2026-09-09T12:34:56Z")
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// hermetic makes git ignore the machine's own configuration, so these tests do not depend
// on the ~/.gitconfig of whoever runs them -- including a commit.gpgsign that would
// otherwise make them hang on a key.
func hermetic(t *testing.T) {
	t.Helper()
	none := filepath.Join(t.TempDir(), "no-such-gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", none)
	t.Setenv("GIT_CONFIG_SYSTEM", none)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
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

// table builds a bare remote and one checkout of it, with a roster and two notes from
// Stella already on the table: one carrying a question, one a bare acknowledgement.
func table(t *testing.T) (checkout, bare string) {
	t.Helper()
	bare = filepath.Join(t.TempDir(), "table.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, "init", "--bare", "--quiet", "--initial-branch=main")
	checkout = filepath.Join(t.TempDir(), "checkout")
	gitIn(t, filepath.Dir(checkout), "clone", "--quiet", bare, checkout)
	gitIn(t, checkout, "checkout", "-q", "-B", "main")
	writeFile(t, checkout, "participants.json", rosterJSON)
	writeFile(t, checkout, "from-stella/2026-09-07T0001Z-a-question-abcdef012345.md",
		"From: Stella Codex\nTo: Rowan\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: stella-abcdef012345\nSubject: A question about the gate\n\nShould the gate run on the merge queue too?\n")
	writeFile(t, checkout, "from-stella/2026-09-07T0002Z-heard-111111111111.md",
		"From: Stella\nTo: Rowan\nDate: Mon Sep  7 00:02:00 UTC 2026\nId: stella-111111111111\nSubject: Heard\n\nHeard, thank you.\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com", "commit", "-q", "-m", "the table")
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

const draft = `From: Rowan (bud, the Studio, the mas account)
To: Stella
Cc: Glenn
Re: stella-abcdef012345
Subject: Yes, on the merge queue too

Stella,

Yes, and the key is misspelled in the matrix.
`

func TestUsageAndUnknownVerb(t *testing.T) {
	invoke(t, "").mustCode(t, 2).mustContain(t, "stderr", "nova-message-bus:")
	invoke(t, "", "help").mustCode(t, 0).mustContain(t, "stdout", "usage:")
	invoke(t, "", "wibble").mustCode(t, 2).mustContain(t, "stderr", `unknown subcommand "wibble"`)
}

// Every required flag, refused by name. A missing one is never a guess.
func TestRefusingToGuess(t *testing.T) {
	checkout, _ := table(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"send without --table", []string{"send", "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3"}, "--table is required"},
		{"send without --remote", []string{"send", "--table", checkout, "--stdin", "--branch", "main", "--attempts", "3"}, "--remote is required"},
		{"send without --branch", []string{"send", "--table", checkout, "--stdin", "--remote", "origin", "--attempts", "3"}, "--branch is required"},
		{"send without --attempts", []string{"send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main"}, "--attempts must be given"},
		{"send with attempts 0", []string{"send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "0"}, "--attempts must be given"},
		{"send with neither --file nor --stdin", []string{"send", "--table", checkout, "--remote", "origin", "--branch", "main", "--attempts", "3"}, "exactly one of --file and --stdin"},
		{"send with both", []string{"send", "--table", checkout, "--stdin", "--file", "x.md", "--remote", "origin", "--branch", "main", "--attempts", "3"}, "exactly one of --file and --stdin"},
		{"inbox without --as", []string{"inbox", "--table", checkout, "--receipt-max-words", "40"}, "--as is required"},
		{"inbox without --receipt-max-words", []string{"inbox", "--table", checkout, "--as", "Rowan"}, "--receipt-max-words must be given"},
		{"receipt without --note", []string{"receipt", "--table", checkout, "--as", "Rowan", "--remote", "origin", "--branch", "main", "--attempts", "3"}, "--note is required"},
		{"check without --table", []string{"check"}, "--table is required"},
		{"names without --table", []string{"names"}, "--table is required"},
		{"a positional argument", []string{"check", "--table", checkout, "extra"}, "takes no positional arguments"},
		{"an unknown flag", []string{"check", "--table", checkout, "--wibble"}, "nova-message-bus check:"},
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
	checkout, _ := table(t)
	invoke(t, "", "check", "--table", checkout, "-h").mustCode(t, 2)
}

func TestSendLandsANoteAndCheckPasses(t *testing.T) {
	hermetic(t)
	checkout, bare := table(t)
	r := invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=rowan-").
		mustContain(t, "stdout", "pushed=true attempts=1")
	if !strings.Contains(r.stdout, "path=from-rowan/2026-09-09T1234Z-yes-on-the-merge-queue-too-") {
		t.Fatalf("the path is not the table's naming convention plus the id: %s", r.stdout)
	}
	// It is on the REMOTE, not merely committed.
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "from-rowan/2026-09-09T1234Z-yes-on-the-merge-queue-too-") {
		t.Fatalf("the note is not on the remote:\n%s", files)
	}
	// The commit is the sender's identity, from the roster.
	if who := strings.TrimSpace(gitIn(t, bare, "log", "-1", "--format=%an <%ae>", "main")); who != "Rowan <rowan@mas-bandwidth.com>" {
		t.Fatalf("the note was committed as %q", who)
	}
	invoke(t, "", "check", "--table", checkout).mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
	// And the question it answers is no longer open.
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Rowan open=1 notes=0 receipts=1")
}

func TestSendFromAFile(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	path := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(path, []byte(draft), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke(t, "", "send", "--table", checkout, "--file", path, "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).mustContain(t, "stdout", "SEND OK id=rowan-")
}

func TestSendNoPushCommitsAndSaysTheNoteIsNotOnTheTable(t *testing.T) {
	hermetic(t)
	checkout, bare := table(t)
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 0).mustContain(t, "stdout", "pushed=false")
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if strings.Contains(files, "from-rowan/") {
		t.Fatalf("--no-push pushed:\n%s", files)
	}
}

// A refusal is exit 1 and a SEND FAIL line, and the table is untouched.
func TestSendRefusesAndWritesNothing(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	cases := []struct{ name, draft, want string }{
		{"an unknown recipient", "From: Rowan\nTo: Stela\nSubject: s\n\nbody\n", `"Stela"`},
		{"an unknown sender", "From: Nobody\nTo: Rowan\nSubject: s\n\nbody\n", "names no one at this table"},
		{"a sender with no lane", "From: Glenn\nTo: Rowan\nSubject: s\n\nbody\n", "has no lane"},
		{"an author-written Id", "From: Rowan\nTo: Stella\nId: rowan-000000000000\nSubject: s\n\nbody\n", "already carries an Id line"},
		{"a Re naming a slug", "From: Rowan\nTo: Stella\nRe: from-stella/renamed.md\nSubject: s\n\nbody\n", "a slug is not a thread"},
		{"a misspelled header key", "From: Rowan\nTo: Stella\nSbuject: s\n\nbody\n", `unknown header key "Sbuject"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invoke(t, tc.draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
				mustCode(t, 1).
				mustContain(t, "stderr", "SEND FAIL (stdin): ").
				mustContain(t, "stderr", tc.want)
			if entries, err := os.ReadDir(filepath.Join(checkout, "from-rowan")); err == nil && len(entries) > 0 {
				t.Fatalf("a refused draft left %d files in the lane", len(entries))
			}
		})
	}
}

// The one-line guarantee, at the place a caller's own text reaches a refusal.
func TestARefusalIsOneLineWhateverTheDraftHolds(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	// U+2028 ends a line for every reader that follows Unicode rather than counting
	// newlines, so an unresolved recipient holding one could otherwise forge a second line.
	r := invoke(t, "From: Rowan\nTo: Stella\u2028Nobody\nSubject: s\n\nbody\n",
		"send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1)
	if !strings.Contains(r.stderr, `\u2028`) {
		t.Fatalf("the separator was not escaped:\n%q", r.stderr)
	}
	if n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n"); n != 0 {
		t.Fatalf("the refusal is %d lines, want one:\n%q", n+1, r.stderr)
	}
}

func TestInboxSeparatesNotesFromReceiptsAndPutsNotesFirst(t *testing.T) {
	checkout, _ := table(t)
	r := invoke(t, "", "inbox", "--table", checkout, "--as", "the keeper", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX NOTE id=stella-abcdef012345 from=Stella addr=to").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=2 notes=1 receipts=1")
	noteAt := strings.Index(r.stdout, "INBOX NOTE")
	receiptAt := strings.Index(r.stdout, "INBOX RECEIPT")
	if noteAt < 0 || receiptAt < 0 || noteAt > receiptAt {
		t.Fatalf("the notes that carry something must be listed before the bare receipts:\n%s", r.stdout)
	}
}

func TestInboxRefusesANameItDoesNotKnow(t *testing.T) {
	checkout, _ := table(t)
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowna", "--receipt-max-words", "40").
		mustCode(t, 2).mustContain(t, "stderr", "names no one at this table")
	invoke(t, "", "inbox", "--table", checkout, "--as", "Glenn", "--receipt-max-words", "40").
		mustCode(t, 2).mustContain(t, "stderr", "has no lane")
}

func TestReceiptMarksHeardAndInboxHonoursIt(t *testing.T) {
	hermetic(t)
	checkout, bare := table(t)
	invoke(t, "", "receipt", "--table", checkout, "--as", "Rowan", "--note", "stella-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).mustContain(t, "stdout", "RECEIPT OK recorded=1 already=0")
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(files, "from-rowan/RECEIPTS") {
		t.Fatalf("the receipt is not on the remote:\n%s", files)
	}
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "open=1 notes=0 receipts=1")
	// Twice is reported, not written twice, and needs no commit.
	invoke(t, "", "receipt", "--table", checkout, "--as", "Rowan", "--note", "stella-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "RECEIPT ALREADY note=stella-abcdef012345").
		mustContain(t, "stdout", "RECEIPT OK recorded=0 already=1 commit=- pushed=false attempts=0")
	invoke(t, "", "check", "--table", checkout).mustCode(t, 0)
}

func TestReceiptRefuses(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	invoke(t, "", "receipt", "--table", checkout, "--as", "Rowan", "--note", "stella-deadbeefcafe",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1).mustContain(t, "stderr", "RECEIPT FAIL")
	invoke(t, "", "receipt", "--table", checkout, "--as", "Rowna", "--note", "stella-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).mustContain(t, "stderr", "names no one at this table")
}

func TestCheckFailsAndNamesEveryFinding(t *testing.T) {
	checkout, _ := table(t)
	writeFile(t, checkout, "from-rowan/broken.md", "From: Rowan\nthis is prose\n\nbody\n")
	writeFile(t, checkout, "from-rowan/stranger.md", "From: Rowan\nTo: Stela\nSubject: s\n\nbody\n")
	r := invoke(t, "", "check", "--table", checkout).mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-rowan/broken.md: ").
		mustContain(t, "stderr", "BUS FAIL from-rowan/stranger.md")
	if n := strings.Count(r.stderr, "BUS FAIL"); n < 2 {
		t.Fatalf("check reported %d findings over two broken files:\n%s", n, r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("a failing check wrote to stdout: %q", r.stdout)
	}
}

func TestCheckRefusesATableWithNoRoster(t *testing.T) {
	invoke(t, "", "check", "--table", t.TempDir()).mustCode(t, 2).mustContain(t, "stderr", "participants.json")
}

func TestNamesEchoesTheRoster(t *testing.T) {
	checkout, _ := table(t)
	invoke(t, "", "names", "--table", checkout).mustCode(t, 0).
		mustContain(t, "stdout", "NAMES NAME name=Rowan lane=from-rowan aliases=Rowan\\x20Claude;the\\x20keeper").
		mustContain(t, "stdout", "NAMES NAME name=Glenn lane=-").
		mustContain(t, "stdout", "NAMES GROUP name=Everybody\\x20at\\x20the\\x20table").
		mustContain(t, "stdout", "NAMES OK participants=3 groups=1 senders=2")
}

// send refuses to run over a checkout that is not on the branch named, or that holds
// somebody's unrelated work, because the retry loop rebases.
func TestSendRefusesAWrongBranchOrADirtyCheckout(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "trunk", "--attempts", "3").
		mustCode(t, 1).mustContain(t, "stderr", `is on branch "main", not "trunk"`)
	writeFile(t, checkout, "stray.txt", "unrelated work in flight\n")
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1).mustContain(t, "stderr", "stray.txt")
}

func TestSendRefusesATableThatIsNotARepository(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "participants.json", rosterJSON)
	invoke(t, draft, "send", "--table", root, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2)
}

// A push publishes the BRANCH. send must refuse a checkout carrying commits it did not
// make, before it writes the note, because otherwise a note's push publishes somebody
// else's unfinished work and nothing in the output says so.
func TestSendRefusesWhenTheBranchIsAheadOfTheRemote(t *testing.T) {
	hermetic(t)
	checkout, bare := table(t)
	writeFile(t, checkout, "notes-to-self.txt", "half a thought\n")
	gitIn(t, checkout, "add", "notes-to-self.txt")
	gitIn(t, checkout, "-c", "user.name=Someone", "-c", "user.email=someone@example.com", "commit", "-q", "-m", "wip")

	r := invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1).
		mustContain(t, "stderr", "SEND REFUSED: ").
		mustContain(t, "stderr", "ahead of origin/main by 1 commits the tool did not make").
		mustContain(t, "stderr", "push or drop them first")
	if strings.Contains(r.stdout, "SEND OK") {
		t.Fatalf("a refused send reported success: %s", r.stdout)
	}
	// Nothing was written and nothing was published.
	if entries, err := os.ReadDir(filepath.Join(checkout, "from-rowan")); err == nil && len(entries) > 0 {
		t.Fatalf("a refused send left %d files in the lane", len(entries))
	}
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); strings.Contains(files, "notes-to-self.txt") {
		t.Fatalf("the unrelated commit was published:\n%s", files)
	}
}

// --remote and --branch become git's own argv. A value beginning with a dash is an option
// to git, not a name, and this tool would have run it.
func TestRemoteAndBranchThatCouldBeOptionsAreRefused(t *testing.T) {
	checkout, _ := table(t)
	cases := [][]string{
		{"send", "--table", checkout, "--stdin", "--remote", "--upload-pack=touch /tmp/pwned", "--branch", "main", "--attempts", "3"},
		{"send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "--exec=id", "--attempts", "3"},
		{"receipt", "--table", checkout, "--as", "Rowan", "--note", "stella-abcdef012345", "--remote", "origin;id", "--branch", "main", "--attempts", "3"},
	}
	for _, args := range cases {
		invoke(t, draft, args...).mustCode(t, 2).mustContain(t, "stderr", "nova-message-bus ")
	}
}

// --slug is the one piece of a note's path a caller supplies. It was written into the
// filename unchecked, so "../x" wrote outside the lane.
func TestSendRefusesASlugThatIsNotASlug(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	for _, slug := range []string{"../x", "a/b", "a\nb", " "} {
		r := invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--slug", slug).
			mustCode(t, 1).mustContain(t, "stderr", "--slug")
		if n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n"); n != 0 {
			t.Fatalf("the refusal is %d lines, want one:\n%q", n+1, r.stderr)
		}
	}
	// Nothing reached the lane, and nothing reached the table root either.
	if entries, err := os.ReadDir(filepath.Join(checkout, "from-rowan")); err == nil && len(entries) > 0 {
		t.Fatalf("a refused --slug left %d files in the lane", len(entries))
	}
	// A slug that is a slug still works.
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--slug", "ci-gate").
		mustCode(t, 0).mustContain(t, "stdout", "-ci-gate-")
}

// A note on the table that will not parse is not a note that does not exist. inbox used to
// step over it in silence, which is the same failure as a lost push with a quieter cause.
func TestInboxNamesTheNotesItCannotRead(t *testing.T) {
	checkout, _ := table(t)
	writeFile(t, checkout, "from-stella/2026-09-07T0009Z-prose.md",
		"Rowan, the checkpoint is pushed and the suite passed: zero divergence.\n\nMore prose.\n")
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX UNREADABLE path=from-stella/2026-09-07T0009Z-prose.md: ").
		mustContain(t, "stdout", "unreadable=1")
	// The reason is short: a paragraph up to its first colon is not a header key.
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "...")
}

// Heard is not answered. A note I receipted and never replied to is still owed an answer,
// and the receipt that says it arrived must not make it disappear from the listing.
func TestInboxShowsWhatWasHeardButNotAnswered(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	invoke(t, "", "receipt", "--table", checkout, "--as", "Rowan", "--note", "stella-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	r := invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX HEARD id=stella-abcdef012345 from=Stella addr=to").
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
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	r = invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").mustCode(t, 0)
	if strings.Contains(r.stdout, "stella-abcdef012345") {
		t.Fatalf("an answered note is still listed:\n%s", r.stdout)
	}
}

// The adoption path: a table written by hand for months, checked for the first time.
func TestCheckLegacyBefore(t *testing.T) {
	checkout, _ := table(t)
	writeFile(t, checkout, "from-stella/2026-09-01T0001Z-old-prose.md",
		"Rowan, this note predates the tool entirely.\n\nbody\n")
	writeFile(t, checkout, "from-stella/2026-09-08T0001Z-new-prose.md",
		"Rowan, this one does not.\n\nbody\n")

	// Without the flag, both fail and the run fails.
	r := invoke(t, "", "check", "--table", checkout).mustCode(t, 1)
	if n := strings.Count(r.stderr, "BUS FAIL"); n != 2 {
		t.Fatalf("check reported %d failures, want 2:\n%s", n, r.stderr)
	}

	// With it, the old one warns, the new one still fails, and the run still fails.
	r = invoke(t, "", "check", "--table", checkout, "--legacy-before", "2026-09-05").mustCode(t, 1).
		mustContain(t, "stderr", "BUS WARN from-stella/2026-09-01T0001Z-old-prose.md").
		mustContain(t, "stderr", "BUS FAIL from-stella/2026-09-08T0001Z-new-prose.md")
	if n := strings.Count(r.stderr, "BUS FAIL"); n != 1 {
		t.Fatalf("check failed %d findings, want only the one after the cutoff:\n%s", n, r.stderr)
	}

	// With only the old one left, the run passes and SAYS how much it forgave.
	if err := os.Remove(filepath.Join(checkout, "from-stella", "2026-09-08T0001Z-new-prose.md")); err != nil {
		t.Fatal(err)
	}
	invoke(t, "", "check", "--table", checkout, "--legacy-before", "2026-09-05").mustCode(t, 0).
		mustContain(t, "stdout", "BUS OK").
		mustContain(t, "stdout", "warn=1")
	// A clean table says warn=0, so a run that forgave nothing and one that forgave fifty
	// notes are not the same line.
	if err := os.Remove(filepath.Join(checkout, "from-stella", "2026-09-01T0001Z-old-prose.md")); err != nil {
		t.Fatal(err)
	}
	invoke(t, "", "check", "--table", checkout).mustCode(t, 0).mustContain(t, "stdout", "warn=0")
	// A date it cannot read is a bad invocation, not a guess.
	invoke(t, "", "check", "--table", checkout, "--legacy-before", "last Tuesday").
		mustCode(t, 2).mustContain(t, "stderr", "is not a UTC date")
}
