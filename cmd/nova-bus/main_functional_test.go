//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const draft = `From: Ada (day shift, the west host, the shared account)
To: Bo
Cc: Dana
Re: bo-abcdef012345
Subject: Yes, on the merge queue too

Bo,

Yes, and the key is misspelled in the matrix.
`

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

// -h on a verb is help (the CLI style's rule (b), #4505): that verb's usage on stdout at exit
// 0, and the bus is not checked. It used to be refused at exit 2, which an AI caller read
// as a syntax error rather than as the answer it asked for.
func TestVerbHelpIsAnsweredOnStdout(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	r := invoke(t, "", "check", "--bus", checkout, "--full", "-h").mustCode(t, 0).mustContain(t, "stdout", "usage: nova-bus check")
	assert.Emptyf(t, r.stderr, "help wrote to stderr: %q", r.stderr)
}

func TestSendLandsANoteAndCheckPasses(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	r := invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-").
		mustContain(t, "stdout", "pushed=true attempts=1")
	require.Containsf(t, r.stdout, "path=from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-", "the path is not the bus's naming convention plus the id: %s", r.stdout)
	// It is on the REMOTE, not merely committed.
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	require.Containsf(t, files, "from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-", "the note is not on the remote:\n%s", files)
	// The commit is the sender's identity, from the roster.
	{
		who := strings.TrimSpace(gitIn(t, bare, "log", "-1", "--format=%an <%ae>", "main"))
		require.Equalf(t, "Ada <ada@example.com>", who, "the note was committed as %q", who)
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
	require.NoError(t, os.WriteFile(path, []byte(draft), 0o644))
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
	require.NotContainsf(t, files, "from-ada/", "--no-push pushed:\n%s", files)
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
		{"a Re naming a slug", "From: Ada\nTo: Bo\nRe: from-bo/renamed.md\nSubject: s\n\nbody\n", "a slug is not a thread"},
		{"a misspelled header key", "From: Ada\nTo: Bo\nSbuject: s\n\nbody\n", `unknown header key "Sbuject"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invoke(t, tc.draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
				mustCode(t, 1).
				mustContain(t, "stderr", "SEND FAIL (stdin): ").
				mustContain(t, "stderr", tc.want)
			{
				entries, err := os.ReadDir(filepath.Join(checkout, "from-ada"))
				require.Falsef(t, err == nil && len(entries) > 0, "a refused draft left %d files in the lane", len(entries))
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
	require.Containsf(t, r.stderr, `\u2028`, "the separator was not escaped:\n%q", r.stderr)
	{
		n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n")
		require.Equalf(t, 0, n, "the refusal is %d lines, want one:\n%q", n+1, r.stderr)
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
	require.Falsef(t, noteAt < 0 || receiptAt < 0 || noteAt > receiptAt, "the notes that carry something must be listed before the bare receipts:\n%s", r.stdout)
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
	{
		files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
		require.Containsf(t, files, "from-ada/RECEIPTS", "the receipt is not on the remote:\n%s", files)
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
	{
		n := strings.Count(r.stderr, "BUS FAIL")
		require.Falsef(t, n < 2, "check reported %d findings over two broken files:\n%s", n, r.stderr)
	}
	// A failing check says on stdout what it WALKED and nothing else: no OK line, no
	// count that a caller could read as a pass. The scope line is there whether the run
	// passed or failed, because "which files did you look at" is the first thing a person
	// reading a failure asks.
	require.NotContainsf(t, r.stdout, "BUS OK", "a failing check printed an OK line: %q", r.stdout)
	{
		got := strings.TrimSpace(r.stdout)
		require.Equalf(t, "BUS SCOPE mode=full cursor=- changed=0", got, "a failing check wrote more than its scope to stdout: %q", r.stdout)
	}
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
	require.NotContainsf(t, r.stdout, "SEND OK", "a refused send reported success: %s", r.stdout)
	// Nothing was written and nothing was published.
	{
		entries, err := os.ReadDir(filepath.Join(checkout, "from-ada"))
		require.Falsef(t, err == nil && len(entries) > 0, "a refused send left %d files in the lane", len(entries))
	}
	{
		files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
		require.NotContainsf(t, files, "notes-to-self.txt", "the unrelated commit was published:\n%s", files)
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
		{
			n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n")
			require.Equalf(t, 0, n, "the refusal is %d lines, want one:\n%q", n+1, r.stderr)
		}
	}
	// Nothing reached the lane, and nothing reached the bus root either.
	{
		entries, err := os.ReadDir(filepath.Join(checkout, "from-ada"))
		require.Falsef(t, err == nil && len(entries) > 0, "a refused --slug left %d files in the lane", len(entries))
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
	require.Containsf(t, r.stdout, "open=1 notes=0 receipts=1 heard=1", "a heard note is still counted as open:\n%s", r.stdout)
	heardAt, receiptAt := strings.Index(r.stdout, "INBOX HEARD"), strings.Index(r.stdout, "INBOX RECEIPT")
	require.Falsef(t, heardAt < 0 || receiptAt < 0 || heardAt > receiptAt, "HEARD must sit between the notes and the bare receipts:\n%s", r.stdout)
	// And a note that was actually REPLIED to is gone, not merely heard.
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	r = invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").mustCode(t, 0)
	require.NotContainsf(t, r.stdout, "bo-abcdef012345", "an answered note is still listed:\n%s", r.stdout)
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
	{
		n := strings.Count(r.stderr, "BUS FAIL")
		require.Equalf(t, 2, n, "check reported %d failures, want 2:\n%s", n, r.stderr)
	}

	// With it, the old one warns, the new one still fails, and the run still fails.
	r = invoke(t, "", "check", "--bus", checkout, "--full", "--legacy-before", "2026-09-05").mustCode(t, 1).
		mustContain(t, "stdout", "BUS WARN from-bo/2026-09-01T0001Z-old-prose.md").
		mustContain(t, "stderr", "BUS FAIL from-bo/2026-09-08T0001Z-new-prose.md")
	require.NotContainsf(t, r.stderr, "BUS WARN", "a warning reached stderr, where only failures and refusals go:\n%s", r.stderr)
	{
		n := strings.Count(r.stderr, "BUS FAIL")
		require.Equalf(t, 1, n, "check failed %d findings, want only the one after the cutoff:\n%s", n, r.stderr)
	}

	// With only the old one left, the run passes and SAYS how much it forgave.
	require.NoError(t, os.Remove(filepath.Join(checkout, "from-bo", "2026-09-08T0001Z-new-prose.md")))
	invoke(t, "", "check", "--bus", checkout, "--full", "--legacy-before", "2026-09-05").mustCode(t, 0).
		mustContain(t, "stdout", "BUS OK").
		mustContain(t, "stdout", "warn=1")
	// A clean bus says warn=0, so a run that forgave nothing and one that forgave fifty
	// notes are not the same line.
	require.NoError(t, os.Remove(filepath.Join(checkout, "from-bo", "2026-09-01T0001Z-old-prose.md")))
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0).mustContain(t, "stdout", "warn=0")
	// A line it cannot read is a bad invocation, not a guess.
	invoke(t, "", "check", "--bus", checkout, "--full", "--legacy-before", "last Tuesday").
		mustCode(t, 2).mustContain(t, "stderr", "neither a UTC date")
}

// When --receipt-max-words is absent, the value is read first from
// <bus>/.nova-bus/defaults, then from NOVA_BUS_RECEIPT_MAX_WORDS, and refused only when
// neither supplies one. The flag wins over both. Not t.Parallel: it moves the process-wide
// environment, and the refusal must be seen by no other test.
func TestReceiptMaxWordsDefaultsFromBusFileThenEnv(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)

	// Neither flag, file nor env: the refusal names both default sources as the remedy.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada").
		mustCode(t, 2).
		mustContain(t, "stderr", "--receipt-max-words must be given").
		mustContain(t, "stderr", ".nova-bus/defaults").
		mustContain(t, "stderr", "NOVA_BUS_RECEIPT_MAX_WORDS")

	// The file supplies the value when the flag is absent.
	writeFile(t, checkout, ".nova-bus/defaults", "receipt-max-words=40\n")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "receipts=1")

	// The flag wins over the file.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--full", "--receipt-max-words", "1").
		mustCode(t, 0).
		mustContain(t, "stdout", "receipts=0")

	// The environment supplies the value when the flag is absent and there is no file.
	t.Setenv("NOVA_BUS_RECEIPT_MAX_WORDS", "40")
	require.NoError(t, os.Remove(filepath.Join(checkout, ".nova-bus", "defaults")))
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "receipts=1")

	// The file wins over the environment.
	writeFile(t, checkout, ".nova-bus/defaults", "receipt-max-words=1\n")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "receipts=0")
}

// A fresh clone that wrote <bus>/.nova-bus/defaults -- its per-clone state, the file
// `inbox` reads for --receipt-max-words -- must be able to `send` a note with no hand step
// in between. The dirty-checkout guard used to refuse over .nova-bus/defaults as "changes
// that are not this note", so every fresh bus clone locked: inbox demands the file, send
// then refuses the checkout that holds it.
func TestSendIgnoresDotNovaBusPerCloneState(t *testing.T) {
	t.Parallel()

	hermetic(t)
	checkout, _ := busDir(t)
	writeFile(t, checkout, ".nova-bus/defaults", "receipt-max-words=40\n")
	note := filepath.Join(filepath.Dir(checkout), "note.md")
	writeFile(t, filepath.Dir(checkout), "note.md", draft)
	invoke(t, "", "send", "--bus", checkout, "--as", "Ada", "--file", note,
		"--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-")
}
