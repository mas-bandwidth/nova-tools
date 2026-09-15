package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// THE REPLY TRANSACTION, at the binary: one test per normative sentence of the reply
// contract in docs/SPEC-BUS-REPLY.md (PR #267, draft 8 at 937c9c42), the reply half only.
// The read half -- `--bodies`, the `INBOX BODY` frame and the continuation token -- is a
// separate contract and is not touched here.
//
// Every test below names its fixture and the string it asserts, because a test that says
// only "it worked" is a test that cannot go red for the right reason.

// replyBus is the fixture the whole reply contract is measured against: the package's own
// two-note bus, with Ada's cursor and open list already put down, and a scratch directory
// OUTSIDE the checkout for drafts to land in.
//
// --carry-history rather than --legacy-now, because the notes on that bus are dated two
// days before the package clock and this verb answers what a reader is CARRYING.
func replyBus(t *testing.T) (checkout, bare, drafts string) {
	t.Helper()
	hermetic(t)
	checkout, bare = busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	drafts = t.TempDir()
	return checkout, bare, drafts
}

// bodyFile writes a body the caller is about to reply with and returns its path.
func bodyFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// replyArgs is the whole invocation, with the four required reply flags in place.
func replyArgs(checkout, drafts, target, body string, extra ...string) []string {
	args := []string{"draft", "--bus", checkout, "--as", "Ada",
		"--reply-to", target, "--body-file", body, "--draft-dir", drafts,
		"--remote", "origin", "--branch", "main"}
	return append(args, extra...)
}

// draftsIn is every file in a draft directory, so "nothing was written" is an assertion
// and not a hope. Temporaries count: a refused run leaves no `.tmp` either.
func draftsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func mustEmptyDir(t *testing.T, dir string) {
	t.Helper()
	if got := draftsIn(t, dir); len(got) != 0 {
		t.Fatalf("the draft directory holds %v; a refusal writes no draft and leaves no temporary", got)
	}
}

// The name the tool composes: the UTC minute of the run, `-re-`, the target's id.
func replyPath(drafts, id string) string {
	return filepath.Join(drafts, now().UTC().Format(bus.FileTimeLayout)+"-re-"+id+".md")
}

// ------------------------------------------------------------- that the released tool is untouched

// docs/SPEC-BUS-REPLY.md, The operation: "Without it, `draft` is exactly the verb it is
// today: the same flags, the same skeleton on stdout, the same DRAFT REFUSED lines on
// stderr, the same exit 2, and no git at all."
//
// expected= the byte-for-byte stdout, stderr and exit code of the released form over the
// released fixtures: `From: Ada\nTo: Bo\nSubject: gate\n\n<the note goes here>\n`.
func TestDraftWithoutReplyToIsByteIdenticalToTodays(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	const expected = "From: Ada\nTo: Bo\nSubject: gate\n\n" + bus.PlaceholderBody + "\n"
	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate").mustCode(t, 0)
	if r.stdout != expected {
		t.Errorf("stdout is %q, want %q", r.stdout, expected)
	}
	if r.stderr != "DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>\n" {
		t.Errorf("the released form printed %q on stderr; want the one-line send hint", r.stderr)
	}
	// And the refusal side, unchanged: exit 2, one DRAFT REFUSED line per problem.
	bad := invoke(t, "", "draft", "--bus", checkout, "--as", "Nobody", "--to", "Bo").mustCode(t, 2)
	if !strings.Contains(bad.stderr, `DRAFT REFUSED: --as "Nobody" names no one on this bus`) {
		t.Errorf("the released refusal changed: %q", bad.stderr)
	}
	// No git at all: the released form runs against a checkout with no remote reachable.
	if strings.Contains(r.stderr, "fetch") {
		t.Errorf("the released form went near git: %q", r.stderr)
	}
}

// The operation: "`--remote`, `--branch`, `--body-file`, `--draft-dir` and `--git-timeout`
// are accepted only in the reply form. Given without `--reply-to` they are exit 2 naming
// the reason."
//
// expected= `DRAFT REFUSED: --remote belongs to --reply-to; without it draft runs no git
// and writes no file`, one per flag, exit 2.
func TestTheReplyFlagsAreRefusedWithoutReplyTo(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	for _, tc := range []struct{ flag, value string }{
		{"--remote", "origin"},
		{"--branch", "main"},
		{"--body-file", bodyFile(t, "x\n")},
		{"--draft-dir", drafts},
		{"--git-timeout", "30"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			expected := "DRAFT REFUSED: " + tc.flag + " belongs to --reply-to; without it draft runs no git and writes no file"
			invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", tc.flag, tc.value).
				mustCode(t, 2).mustContain(t, "stderr", expected)
		})
	}
}

// "a draft this form writes is an ordinary draft: it must pass the existing `prepare`
// validation with no tolerance applied."
//
// expected= `PREPARE` artifact JSON on stdout, exit 0, over the draft
// TestGeneratedReplyHeaderIsByteEqualToTheHandBuiltOne produces.
func TestPrepareAndSendAreUnchangedByThisSlice(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	body := bodyFile(t, "Yes, and on the merge queue too.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).mustCode(t, 0)
	path := replyPath(drafts, "bo-abcdef012345")
	r := invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", path).mustCode(t, 0)
	if !strings.Contains(r.stdout, `"id"`) {
		t.Errorf("prepare did not produce an artifact for a generated reply: %s\n%s", r.stdout, r.stderr)
	}
	if strings.Contains(r.stderr, "NOTE") {
		t.Errorf("prepare applied a tolerance to a generated reply, so the header is not the header it writes: %s", r.stderr)
	}
}

// ------------------------------------------------------------- that it is fresh

// What "fresh" means: "The refresh is not a flag that can be forgotten -- it is part of
// what `--reply-to` is"; Resolving the target: "it is resolved after the refresh, against
// the tree the fetch left."
//
// expected= exit 0 and `re=bo-999999999999` for a note that exists ONLY on the remote; and
// exit 1 `DRAFT REFUSED: --reply-to "bo-999999999999" is not an id on this bus` for the
// same run with the fetch disabled at the seam.
func TestReplyRefreshesBeforeItResolves(t *testing.T) {
	checkout, bare, drafts := replyBus(t)
	// A second checkout of the same bare remote pushes a note Ada's checkout has not seen.
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "--quiet", bare, other)
	writeFile(t, other, "from-bo/2026-09-09T1200Z-only-on-the-remote-999999999999.md",
		"From: Bo\nTo: Ada\nDate: Wed Sep  9 12:00:00 UTC 2026\nId: bo-999999999999\nSubject: Only on the remote\n\nIs the gate green?\n")
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "a note only on the remote")
	gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")

	body := bodyFile(t, "Green on all three.\n")
	r := invoke(t, "", replyArgs(checkout, drafts, "bo-999999999999", body)...).mustCode(t, 0)
	if !strings.Contains(r.stdout, "re=bo-999999999999") {
		t.Fatalf("the refresh did not put the remote-only note on the listing: %s", r.stdout)
	}
	if !strings.Contains(r.stdout, "moved=true") {
		t.Errorf("the receipt does not say the checkout moved: %s", r.stdout)
	}
	// The same run with the fetch disabled at the seam: the id is unknown locally.
	second := t.TempDir()
	withoutFetch(t, func() {
		invoke(t, "", replyArgs(checkout, second, "bo-888888888888", body)...).
			mustCode(t, 1).
			mustContain(t, "stderr", `DRAFT REFUSED: --reply-to "bo-888888888888" is not an id on this bus`)
	})
	// The other half of the live listing: a note already carried AND already receipted is
	// still a legal target, because heard is not answered.
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-111111111111",
		"--remote", "origin", "--branch", "main").mustCode(t, 0)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	heard := t.TempDir()
	invoke(t, "", replyArgs(checkout, heard, "bo-111111111111", body)...).
		mustCode(t, 0).mustContain(t, "stdout", "re=bo-111111111111")
}

// "A fetch that fails is a refusal and never a fall back to the checkout. Exit 1, naming
// the remote, the branch and git's own words under the event line."
//
// expected= `DRAFT REFUSED: the fetch that would say whether anything has arrived on
// nowhere/main failed`, exit 1, and an empty --draft-dir.
func TestRefreshFailureIsARefusalAndNeverAStaleAnswer(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	body := bodyFile(t, "Yes.\n")
	for _, tc := range []struct{ name, remote, branch, want string }{
		{"an unreachable remote", "nowhere", "main", "DRAFT REFUSED: the fetch that would say whether anything has arrived on nowhere/main failed"},
		{"a branch nobody has", "origin", "no-such-branch", "DRAFT REFUSED: the fetch that would say whether anything has arrived on origin/no-such-branch failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			args := []string{"draft", "--bus", checkout, "--as", "Ada", "--reply-to", "bo-abcdef012345",
				"--body-file", body, "--draft-dir", dir, "--remote", tc.remote, "--branch", tc.branch}
			invoke(t, "", args...).mustCode(t, 1).mustContain(t, "stderr", tc.want)
			mustEmptyDir(t, dir)
		})
	}
	_ = drafts
}

// "a checkout that has diverged is a refusal naming the recovery, and nothing is written."
//
// expected= `DRAFT REFUSED: this checkout and refs/remotes/origin/main have both moved`,
// exit 1, with the local commit and an unrelated dirty file both untouched.
func TestADivergedCheckoutIsRefusedAndLosesNothing(t *testing.T) {
	t.Parallel()
	checkout, bare, drafts := replyBus(t)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "--quiet", bare, other)
	writeFile(t, other, "from-bo/2026-09-09T1201Z-theirs-777777777777.md",
		"From: Bo\nTo: Ada\nDate: Wed Sep  9 12:01:00 UTC 2026\nId: bo-777777777777\nSubject: Theirs\n\nTheirs.\n")
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "theirs")
	gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")
	// A commit of ours that was never pushed, and a dirty file beside it.
	writeFile(t, checkout, "from-ada/2026-09-09T1202Z-ours-666666666666.md",
		"From: Ada\nTo: Bo\nDate: Wed Sep  9 12:02:00 UTC 2026\nId: ada-666666666666\nSubject: Ours\n\nOurs.\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", "ours")
	writeFile(t, checkout, "scratch.txt", "not the bus's business\n")
	head := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))

	body := bodyFile(t, "Yes.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).
		mustCode(t, 1).mustContain(t, "stderr", "DRAFT REFUSED: this checkout and origin/main have both moved since they last agreed")
	mustEmptyDir(t, drafts)
	if got := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD")); got != head {
		t.Errorf("the refusal moved HEAD from %s to %s", head, got)
	}
	raw, err := os.ReadFile(filepath.Join(checkout, "scratch.txt"))
	if err != nil || string(raw) != "not the bus's business\n" {
		t.Errorf("the refusal did not leave the dirty file alone: %v %q", err, raw)
	}
}

// "the refresh moves the checkout and nothing else. No commit, no push, no CURSOR, no
// OPEN, no RECEIPTS, no INDEX."
//
// expected= every lane state file byte-identical after a successful reply, including one
// whose target was found only in the new-since-the-cursor half of the listing.
func TestTheRefreshWritesNothingToTheBus(t *testing.T) {
	t.Parallel()
	checkout, bare, drafts := replyBus(t)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "--quiet", bare, other)
	writeFile(t, other, "from-bo/2026-09-09T1203Z-new-since-555555555555.md",
		"From: Bo\nTo: Ada\nDate: Wed Sep  9 12:03:00 UTC 2026\nId: bo-555555555555\nSubject: New since the cursor\n\nNew.\n")
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "new since")
	gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")

	before := laneState(t, checkout)
	commits := strings.TrimSpace(gitIn(t, checkout, "rev-list", "--count", "HEAD"))
	body := bodyFile(t, "Noted.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-555555555555", body)...).mustCode(t, 0)
	for path, want := range before {
		got, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s changed under a draft:\nbefore %q\nafter  %q", path, want, got)
		}
	}
	// The fetch moves the checkout; nothing this run did adds a commit of its own.
	after := strings.TrimSpace(gitIn(t, checkout, "rev-list", "--count", "HEAD"))
	if after != "3" || commits != "2" {
		t.Errorf("the checkout went from %s commits to %s; the refresh is a fast-forward of the bus's own history and writes none", commits, after)
	}
	if dirty := strings.TrimSpace(gitIn(t, checkout, "status", "--porcelain")); dirty != "" {
		t.Errorf("the run left the checkout dirty:\n%s", dirty)
	}
}

// laneState is every lane state file on the bus, keyed by repo-relative path.
func laneState(t *testing.T, checkout string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, lane := range []string{"from-ada", "from-bo"} {
		for _, name := range []string{"CURSOR", "OPEN", "RECEIPTS", "INDEX"} {
			rel := lane + "/" + name
			raw, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(rel)))
			if err != nil {
				continue
			}
			out[rel] = string(raw)
		}
	}
	if len(out) == 0 {
		t.Fatal("the fixture has no lane state files, so this test would assert nothing")
	}
	return out
}

// ------------------------------------------------------------- that it resolves the right note

// The refusal table: "`--reply-to` names no id, no path and no listed subject on the
// refreshed bus | that threads are named by id, and that a slug is not a thread | 1"
//
// expected= `DRAFT REFUSED: --reply-to "bo-000000000000" is not an id on this bus, not a
// note that exists, and not the subject of a note on your listing; threads are named by
// id, and a slug is not a thread`, exit 1, --draft-dir still empty.
func TestUnknownReplyTargetIsRefusedAndWritesNoDraft(t *testing.T) {
	t.Parallel()
	checkout, _, _ := replyBus(t)
	body := bodyFile(t, "Yes.\n")
	for _, target := range []string{"bo-000000000000", "from-bo/no-such-note.md", "a subject nobody wrote"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			invoke(t, "", replyArgs(checkout, dir, target, body)...).
				mustCode(t, 1).
				mustContain(t, "stderr", fmt.Sprintf("DRAFT REFUSED: --reply-to %q is not an id on this bus, not a note that exists, and not the subject of a note on your listing; threads are named by id, and a slug is not a thread", target))
			mustEmptyDir(t, dir)
		})
	}
}

// "A subject that matches two notes resolves to the newest and says so."
//
// expected= `DRAFT NOTE --reply-to: subject matched 2 notes; this draft names the newest
// bo-333333333333 from Bo; name the id to be exact`, and `re=bo-333333333333` on stdout.
func TestReplySubjectMatchingTwoNotesTakesTheNewestAndSaysSo(t *testing.T) {
	t.Parallel()
	checkout, bare, drafts := replyBus(t)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "--quiet", bare, other)
	writeFile(t, other, "from-bo/2026-09-09T1204Z-a-question-333333333333.md",
		"From: Bo\nTo: Ada\nDate: Wed Sep  9 12:04:00 UTC 2026\nId: bo-333333333333\nSubject: A question about the gate\n\nAgain.\n")
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "the same subject again")
	gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")

	body := bodyFile(t, "Yes.\n")
	r := invoke(t, "", replyArgs(checkout, drafts, "A question about the gate", body)...).mustCode(t, 0)
	r.mustContain(t, "stdout", "re=bo-333333333333")
	r.mustContain(t, "stderr", "DRAFT NOTE --reply-to: subject matched 2 notes; this draft names the newest bo-333333333333 from Bo; name the id to be exact")
}

// "A note that is on the bus but on neither half of that listing is refused, and the
// refusal says which of the reasons it is" -- four reasons, each with its door.
//
// expected= one `DRAFT REFUSED:` line per reason, exit 1, each naming its door.
func TestTargetNotOnTheOpenListIsItsOwnRefusal(t *testing.T) {
	t.Parallel()
	body := bodyFile(t, "Yes.\n")

	t.Run("never addressed to this reader", func(t *testing.T) {
		t.Parallel()
		checkout, bare, drafts := replyBus(t)
		other := filepath.Join(t.TempDir(), "other")
		gitIn(t, filepath.Dir(other), "clone", "--quiet", bare, other)
		writeFile(t, other, "from-bo/2026-09-09T1205Z-for-dana-444444444444.md",
			"From: Bo\nTo: Dana\nDate: Wed Sep  9 12:05:00 UTC 2026\nId: bo-444444444444\nSubject: For Dana\n\nDana.\n")
		gitIn(t, other, "add", "-A")
		gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "for dana")
		gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")
		invoke(t, "", replyArgs(checkout, drafts, "bo-444444444444", body)...).
			mustCode(t, 1).
			mustContain(t, "stderr", `DRAFT REFUSED: --reply-to bo-444444444444 was never addressed to you, on To: or Cc:, so it is on no listing of yours; draft --re names a note this form will not`)
		mustEmptyDir(t, drafts)
	})

	t.Run("already answered", func(t *testing.T) {
		t.Parallel()
		checkout, _, drafts := replyBus(t)
		invoke(t, draftAnswering, "send", "--bus", checkout, "--stdin", "--as", "Ada",
			"--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
		invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
			"--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
		invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).
			mustCode(t, 1).
			mustContain(t, "stderr", `DRAFT REFUSED: --reply-to bo-abcdef012345 is a note you have already answered, so it is on no listing of yours; draft --re names a closed thread`)
		mustEmptyDir(t, drafts)
	})

	t.Run("behind the switch-day line", func(t *testing.T) {
		t.Parallel()
		checkout, _ := busDir(t)
		invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
			"--full", "--advance", "--legacy-now", "--remote", "origin", "--branch", "main").mustCode(t, 0)
		drafts := t.TempDir()
		invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).
			mustCode(t, 1).
			mustContain(t, "stderr", `DRAFT REFUSED: --reply-to bo-abcdef012345 is behind your switch-day line, so this reader has taken it as read; draft --re names it anyway`)
		mustEmptyDir(t, drafts)
	})

	t.Run("no cursor at all", func(t *testing.T) {
		t.Parallel()
		hermetic(t)
		checkout, _ := busDir(t)
		drafts := t.TempDir()
		invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).
			mustCode(t, 1).
			mustContain(t, "stderr", `DRAFT REFUSED: --reply-to bo-abcdef012345 cannot be on a listing you have not got: this lane has no cursor yet; one inbox --advance run gives you both`)
		mustEmptyDir(t, drafts)
	})
}

// draftAnswering closes bo-abcdef012345, so the "already answered" fixture above is the
// state a reader is in after they have replied once.
const draftAnswering = `From: Ada
To: Bo
Re: bo-abcdef012345
Subject: Yes, on the merge queue too

Yes.
`

// "A target with no `Id:` line gets a derived id instead: the literal `legacy-` followed by
// the first 12 lowercase hex digits of the SHA-256 of the target's repo-relative path ...
// The `Re:` line itself is unaffected and still carries the path."
//
// expected= a filename `<minute>Z-re-legacy-<12 hex>.md` holding no `/`, and
// `Re: from-bo/2026-09-05T0900Z-old.md` inside it.
func TestReplyResolvesAPathForANoteWrittenBeforeIds(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	for _, name := range []string{"2026-09-05T0900Z-old.md", "2026-09-05T0901Z-older.md"} {
		writeFile(t, checkout, "from-bo/"+name,
			"From: Bo\nTo: Ada\nDate: Sat Sep  5 09:00:00 UTC 2026\nSubject: "+name+"\n\nBefore ids.\n")
	}
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "two legacy notes")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)

	drafts := t.TempDir()
	body := bodyFile(t, "Yes.\n")
	var names []string
	for _, target := range []string{"from-bo/2026-09-05T0900Z-old.md", "from-bo/2026-09-05T0901Z-older.md"} {
		r := invoke(t, "", replyArgs(checkout, drafts, target, body)...).mustCode(t, 0)
		if !strings.Contains(r.stdout, "re="+target) {
			t.Errorf("the receipt does not carry the path as the target: %s", r.stdout)
		}
	}
	for _, n := range draftsIn(t, drafts) {
		if !strings.Contains(n, "-re-legacy-") {
			t.Errorf("a legacy target produced the filename %q; the name is built from the derived id", n)
		}
		if strings.ContainsAny(n, "/\\") {
			t.Errorf("the filename %q holds a path separator", n)
		}
		names = append(names, n)
	}
	if len(names) != 2 {
		t.Fatalf("two legacy targets produced %d files: %v", len(names), names)
	}
	raw, err := os.ReadFile(filepath.Join(drafts, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Re: from-bo/2026-09-05T090") {
		t.Errorf("the Re line does not carry the path:\n%s", raw)
	}
}

// ------------------------------------------------------------- headers are the tool's, the body the author's

// The headers it writes: the whole table, against a hand-built reply committed as testdata.
//
// expected= cmd/nova-bus/testdata/reply/hand-built.md, byte for byte.
func TestGeneratedReplyHeaderIsByteEqualToTheHandBuiltOne(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	body := bodyFile(t, "Yes, and on the merge queue too.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).mustCode(t, 0)
	want, err := os.ReadFile(filepath.Join("testdata", "reply", "hand-built.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(replyPath(drafts, "bo-abcdef012345"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the generated reply is not the hand-built one:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// "`To` | `--to` when given; otherwise the resolved sender of the target note"; "`Cc` |
// `--cc` when given; otherwise absent, never inherited from the target".
//
// expected= `to="Bo" cc=-` on the receipt and no `Cc:` line in the file, for a target whose
// own Cc names Dana.
func TestReplyDefaultsToTheSendersCanonicalNameAndNoCc(t *testing.T) {
	t.Parallel()
	checkout, bare, drafts := replyBus(t)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "--quiet", bare, other)
	writeFile(t, other, "from-bo/2026-09-09T1206Z-with-a-cc-222222222222.md",
		"From: Bo Quill\nTo: Ada\nCc: Dana\nDate: Wed Sep  9 12:06:00 UTC 2026\nId: bo-222222222222\nSubject: With a Cc\n\nCopied.\n")
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "with a cc")
	gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")

	body := bodyFile(t, "Yes.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-222222222222", body)...).
		mustCode(t, 0).mustContain(t, "stdout", `to="Bo" cc=-`)
	raw, err := os.ReadFile(replyPath(drafts, "bo-222222222222"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Cc:") {
		t.Errorf("the reply inherited the target's Cc:\n%s", raw)
	}
	if !strings.Contains(string(raw), "To: Bo\n") {
		t.Errorf("To is not the target's resolved sender:\n%s", raw)
	}
}

// "`Subject` | ... the target's subject with one `Re: ` in front, and an existing `Re: ` is
// not stacked."
//
// expected= `Subject: Re: A question about the gate` for both, and one `DRAFT NOTE the
// subject is the target's own, unstacked: "Re: A question about the gate"` for the second.
func TestReplySubjectIsPrefixedOnceAndNeverStacked(t *testing.T) {
	t.Parallel()
	checkout, bare, drafts := replyBus(t)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "--quiet", bare, other)
	writeFile(t, other, "from-bo/2026-09-09T1207Z-already-a-reply-aaaaaaaaaaaa.md",
		"From: Bo\nTo: Ada\nDate: Wed Sep  9 12:07:00 UTC 2026\nId: bo-aaaaaaaaaaaa\nSubject: Re: A question about the gate\n\nStill asking.\n")
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "already a reply")
	gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")

	body := bodyFile(t, "Yes.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).mustCode(t, 0)
	plain, err := os.ReadFile(replyPath(drafts, "bo-abcdef012345"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), "Subject: Re: A question about the gate\n") {
		t.Errorf("the subject is not prefixed once:\n%s", plain)
	}
	r := invoke(t, "", replyArgs(checkout, drafts, "bo-aaaaaaaaaaaa", body)...).mustCode(t, 0)
	r.mustContain(t, "stderr", `DRAFT NOTE the subject is the target's own, unstacked: "Re: A question about the gate"`)
	stacked, err := os.ReadFile(replyPath(drafts, "bo-aaaaaaaaaaaa"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stacked), "Re: Re: ") {
		t.Errorf("the Re: prefix was stacked:\n%s", stacked)
	}
}

// "A reply to your own note requires an explicit `--to`, because the default -- the
// target's sender -- is you."
//
// expected= `DRAFT REFUSED: --reply-to ada-0f1e2d3c4b5a is your own note, so the default
// To: would be you; a reply to your own note needs an explicit --to`, exit 1.
func TestReplyToYourOwnNoteNeedsAnExplicitTo(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	// The fixture is the shape the rule is FOR: a note this reader is carrying whose
	// resolved sender is this reader -- a note mis-sent from another lane under Ada's own
	// name. The default To: is the target's sender, which here is Ada, and a note addressed
	// to its own author reaches nobody and lands on no open list.
	writeFile(t, checkout, "from-bo/2026-09-08T0900Z-under-my-own-name-cccccccccccc.md",
		"From: Ada\nTo: Ada\nDate: Tue Sep  8 09:00:00 UTC 2026\nId: bo-cccccccccccc\nSubject: Under my own name\n\nMine, in the wrong lane.\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "a note under Ada's own name")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)

	drafts := t.TempDir()
	body := bodyFile(t, "One more thing.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-cccccccccccc", body)...).
		mustCode(t, 1).
		mustContain(t, "stderr", "DRAFT REFUSED: --reply-to bo-cccccccccccc is your own note, so the default To: would be you; a reply to your own note needs an explicit --to")
	mustEmptyDir(t, drafts)
	invoke(t, "", replyArgs(checkout, drafts, "bo-cccccccccccc", body, "--to", "Bo")...).mustCode(t, 0)
}

// docs/SPEC-BUS-REPLY.md 427-431: the released form's own sentence --
// `nova-bus draft: --to is required; refusing to guess` -- is the error message for a
// `--to` flag that is GIVEN but BLANK (empty or all whitespace).
//
// expected= exit 2 with the exact sentence, and NO draft file in the directory afterwards.
func TestToBlankIsRefused(t *testing.T) {
	t.Parallel()
	checkout, _, _ := replyBus(t)
	body := bodyFile(t, "Yes.\n")
	for _, tc := range []struct{ name, to string }{
		{"--to \"\"", ""},
		{"--to \"   \"", "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			r := invoke(t, "", replyArgs(checkout, dir, "bo-abcdef012345", body, "--to", tc.to)...).
				mustCode(t, 2)
			r.mustContain(t, "stderr", "DRAFT REFUSED: nova-bus draft: --to is required; refusing to guess")
			mustEmptyDir(t, dir)
		})
	}
}

// "`--body-file` is body text and nothing else:: it is never parsed for headers, and a line
// in it reading `To: somebody` is a line of prose in the note that goes out."
//
// expected= `to="Bo"` on the receipt, and the body's own `To: somebody-else` line present
// below the blank line and nowhere above it.
func TestBodyFileIsPreservedAndItsFakeHeadersDoNotRoute(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	body := bodyFile(t, "To: somebody-else\nRe: bo-111111111111\n\nand the rest of the body.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).
		mustCode(t, 0).mustContain(t, "stdout", `to="Bo"`)
	raw, err := os.ReadFile(replyPath(drafts, "bo-abcdef012345"))
	if err != nil {
		t.Fatal(err)
	}
	header, rest, ok := strings.Cut(string(raw), "\n\n")
	if !ok {
		t.Fatalf("the draft has no blank line between header and body:\n%s", raw)
	}
	if strings.Contains(header, "somebody-else") || strings.Contains(header, "bo-111111111111") {
		t.Errorf("a line of the body routed something:\n%s", header)
	}
	if !strings.HasPrefix(rest, "To: somebody-else\nRe: bo-111111111111\n\nand the rest of the body.\n") {
		t.Errorf("the body was not preserved:\n%q", rest)
	}
}

// The refusal table: "`--to`, `--cc` or `--subject` carrying a control character or a line
// separator | the existing one-line validation's refusal | 2"
//
// expected= `DRAFT REFUSED:` naming the flag, exit 2, --draft-dir empty.
func TestControlCharactersInSubjectAndToAreRefused(t *testing.T) {
	t.Parallel()
	checkout, _, _ := replyBus(t)
	body := bodyFile(t, "Yes.\n")
	// EVERY FIXTURE RESOLVES. The first version of this test picked `--to "Bo\nDana"`
	// and `--cc "Bo\u202e"`, both of which the ROSTER refuses -- so it was exit 2 over a
	// rule it never reached, and it stayed green while a trailing newline on a name the
	// roster knows walked straight onto the header line and ended the header block. The
	// names below are all names this bus has, so the only thing that can refuse them is
	// the one-line check.
	for _, tc := range []struct{ name, flag, value, want string }{
		{"a newline in the subject", "--subject", "one\ntwo", `--subject: a header line is one line, and "one\ntwo" holds a line break or a control character`},
		{"U+2028 in the subject", "--subject", "one\u2028two", `--subject: a header line is one line, and "one\u2028two" holds a line break or a control character`},
		{"a trailing newline on a name the roster knows", "--to", "Bo\n", `--to: a header line is one line, and "Bo\n" holds a line break or a control character`},
		{"a newline between two names the roster knows", "--to", "Bo\nDana", `--to: a header line is one line, and "Bo\nDana" holds a line break or a control character`},
		{"U+2028 in --to", "--to", "Bo\u2028", `--to: a header line is one line, and "Bo\u2028" holds a line break or a control character`},
		{"a trailing newline in --cc", "--cc", "Dana\n", `--cc: a header line is one line, and "Dana\n" holds a line break or a control character`},
		{"a carriage return in --cc", "--cc", "Dana\r", `--cc: a header line is one line, and "Dana\r" holds a line break or a control character`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			invoke(t, "", replyArgs(checkout, dir, "bo-abcdef012345", body, tc.flag, tc.value)...).
				mustCode(t, 2).mustContain(t, "stderr", "DRAFT REFUSED: "+tc.want)
			mustEmptyDir(t, dir)
		})
	}
}

// The other half of the same defect, and the reason the check is on the RAW flag value
// rather than on the resolved names: the header line a reply carries is the caller's own
// line, so a group stays the group's name and no audience is expanded silently.
//
// docs/SPEC-BUS-REPLY.md 514-515: "No group or thread expansion. The audience is the
// target's sender or what the caller named."
//
// expected= `To: The twenty` on the header line, and `to=` on the receipt carrying the
// three RESOLVED names.
func TestAGroupStaysTheGroupsNameOnTheHeaderLine(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	body := bodyFile(t, "Yes.\n")
	r := invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body, "--to", "Everybody on the bus")...).mustCode(t, 0)
	r.mustContain(t, "stdout", `to="Ada";"Bo";"Dana"`)
	raw, err := os.ReadFile(replyPath(drafts, "bo-abcdef012345"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "To: Everybody on the bus\n") {
		t.Errorf("the group was expanded onto the header line:\n%s", raw)
	}
}

// ------------------------------------------------------------- outside the checkout, or not at all

// "A `--draft-dir` inside the bus checkout is refused, before anything is written."
//
// expected= `DRAFT REFUSED: --draft-dir ... is the bus checkout at <root>, or inside it;
// drafts go outside the bus, because send needs its tree clean`, exit 2.
func TestDraftInsideTheProtectedCheckoutIsRefused(t *testing.T) {
	t.Parallel()
	checkout, _, _ := replyBus(t)
	inside := filepath.Join(checkout, "scratch")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "through-a-link")
	if err := os.Symlink(inside, link); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
	body := bodyFile(t, "Yes.\n")
	for _, dir := range []string{checkout, inside, link} {
		invoke(t, "", replyArgs(checkout, dir, "bo-abcdef012345", body)...).
			mustCode(t, 2).
			mustContain(t, "stderr", "drafts go outside the bus, because send needs its tree clean")
	}
	if got := draftsIn(t, inside); len(got) != 0 {
		t.Errorf("something was written inside the checkout: %v", got)
	}
}

// "An existing file at that path is a refusal, never an overwrite -- exit 1, naming the
// path."
//
// expected= `DRAFT REFUSED: a draft already exists at <path>; this tool never overwrites a
// draft`, exit 1, and the existing file unchanged.
func TestReplyNeverOverwritesAnExistingDraft(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	path := replyPath(drafts, "bo-abcdef012345")
	if err := os.WriteFile(path, []byte("somebody is editing this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := bodyFile(t, "Yes.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).
		mustCode(t, 1).
		mustContain(t, "stderr", "DRAFT REFUSED: a draft already exists at "+path+"; this tool never overwrites a draft")
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "somebody is editing this\n" {
		t.Errorf("the existing draft was touched: %v %q", err, raw)
	}
	if got := draftsIn(t, drafts); len(got) != 1 {
		t.Errorf("the refused run left %v behind", got)
	}
}

// "two nova-bus processes, not two goroutines, started together against two separate bus
// checkouts sharing one --draft-dir ... Exactly one exits 0; the other exits 1 with the
// existing-file line naming the path."
//
// expected= one exit 0, one exit 1 carrying `this tool never overwrites a draft`, one file
// at the composed path, and no `.tmp` beside it.
func TestTwoProcessesRacingOneDraftPathLeaveOneWinner(t *testing.T) {
	if testing.Short() {
		t.Skip("the race wants real processes")
	}
	t.Parallel()
	hermetic(t)
	first, bare := busDir(t)
	second := filepath.Join(t.TempDir(), "second")
	gitIn(t, filepath.Dir(second), "clone", "--quiet", bare, second)
	for _, c := range []string{first, second} {
		invoke(t, "", "inbox", "--bus", c, "--as", "Ada", "--receipt-max-words", "40",
			"--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	}
	drafts := t.TempDir()
	body := bodyFile(t, "Yes.\n")
	bin := buildNovaBus(t)
	for round := 0; round < 5; round++ {
		round := round
		dir := filepath.Join(drafts, fmt.Sprintf("round%d", round))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		codes := make(chan int, 2)
		outs := make(chan string, 2)
		for _, c := range []string{first, second} {
			go func(c string) {
				cmd := exec.Command(bin, "draft", "--bus", c, "--as", "Ada",
					"--reply-to", "bo-abcdef012345", "--body-file", body, "--draft-dir", dir,
					"--remote", "origin", "--branch", "main")
				out, _ := cmd.CombinedOutput()
				outs <- string(out)
				codes <- cmd.ProcessState.ExitCode()
			}(c)
		}
		a, b := <-codes, <-codes
		outA, outB := <-outs, <-outs
		if a+b != 1 {
			t.Fatalf("round %d: exit codes %d and %d; exactly one run wins and the other is refused\n%s\n%s", round, a, b, outA, outB)
		}
		loser := outB
		if a == 1 {
			loser = outA
		}
		if !strings.Contains(loser, "this tool never overwrites a draft") {
			t.Errorf("round %d: the loser said %q", round, loser)
		}
		files := draftsIn(t, dir)
		if len(files) != 1 {
			t.Fatalf("round %d: the directory holds %v; one name, one file, no temporary", round, files)
		}
		if strings.HasSuffix(files[0], ".tmp") {
			t.Errorf("round %d: a temporary survived: %v", round, files)
		}
	}
}

// buildNovaBus builds the binary the race test runs as two processes.
func buildNovaBus(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-bus")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// "No refusal writes a partial draft, and no publish replaces one." -- every row of the
// refusal table, against an empty --draft-dir.
//
// expected= an empty --draft-dir after every refusing invocation.
func TestNoPartialDraftOnAnyRefusal(t *testing.T) {
	t.Parallel()
	checkout, _, _ := replyBus(t)
	for _, tc := range refusalTable(t, checkout) {
		t.Run(tc.name, func(t *testing.T) {
			if tc.draftDir == "" {
				t.Skip("this row names its own draft directory")
			}
			invoke(t, "", tc.args...).mustCode(t, tc.code)
			mustEmptyDir(t, tc.draftDir)
		})
	}
}

// "the body is empty, or over `--max-body-bytes` | the budget and the size, read at
// budget+1 and no further | 1"
//
// expected= exit 0 at the budget; exit 1 `DRAFT REFUSED: --body-file ... is over
// --max-body-bytes 64` at budget+1.
func TestReplyBodyAtTheBudgetAndOneByteOver(t *testing.T) {
	t.Parallel()
	checkout, _, _ := replyBus(t)
	at := bodyFile(t, strings.Repeat("a", 63)+"\n")
	over := bodyFile(t, strings.Repeat("a", 64)+"\n")
	oneLine := bodyFile(t, strings.Repeat("b", 65))
	ok := t.TempDir()
	invoke(t, "", replyArgs(checkout, ok, "bo-abcdef012345", at, "--max-body-bytes", "64")...).mustCode(t, 0)
	for _, path := range []string{over, oneLine} {
		dir := t.TempDir()
		invoke(t, "", replyArgs(checkout, dir, "bo-abcdef012345", path, "--max-body-bytes", "64")...).
			mustCode(t, 1).
			mustContain(t, "stderr", "DRAFT REFUSED: --body-file "+path+" is over --max-body-bytes 64")
		mustEmptyDir(t, dir)
	}
	empty := bodyFile(t, "")
	dir := t.TempDir()
	invoke(t, "", replyArgs(checkout, dir, "bo-abcdef012345", empty)...).
		mustCode(t, 1).
		mustContain(t, "stderr", "DRAFT REFUSED: --body-file "+empty+" is empty; a reply with no body is not a reply")
	mustEmptyDir(t, dir)
}

// ------------------------------------------------------------- bounded and scannable

// "`DRAFT OK` is exactly one line, and it is the only thing this form puts on stdout."
//
// expected= one line on stdout, beginning `DRAFT OK path=`, over every success fixture.
func TestReplyReceiptIsExactlyOneLineOnStdout(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	body := bodyFile(t, "Yes.\n")
	for i, extra := range [][]string{nil, {"--to", "Bo"}, {"--cc", "Dana"}, {"--subject", "a subject of my own"}} {
		dir := filepath.Join(drafts, fmt.Sprintf("case%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		r := invoke(t, "", replyArgs(checkout, dir, "bo-abcdef012345", body, extra...)...).mustCode(t, 0)
		lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
		if len(lines) != 1 || !strings.HasPrefix(lines[0], "DRAFT OK path=") {
			t.Errorf("stdout is %d lines: %q", len(lines), r.stdout)
		}
	}
}

// "a reader carrying six hundred open notes gets the same receipt as a reader carrying two,
// and a test asserts it at six hundred rather than at two."
//
// expected= exactly one line on stdout, the same shape as at two notes.
func TestReplyReceiptStaysOneLineAtSixHundredOpenNotes(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	for i := 0; i < 600; i++ {
		writeFile(t, checkout, fmt.Sprintf("from-bo/2026-09-08T%02d%02dZ-bulk-%012d.md", i/60, i%60, i),
			fmt.Sprintf("From: Bo\nTo: Ada\nDate: Tue Sep  8 00:00:00 UTC 2026\nId: bo-%012d\nSubject: Bulk %d\n\nBulk.\n", i, i))
	}
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "six hundred notes")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)

	drafts := t.TempDir()
	body := bodyFile(t, "Yes.\n")
	r := invoke(t, "", replyArgs(checkout, drafts, "bo-000000000042", body)...).mustCode(t, 0)
	if lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n"); len(lines) != 1 {
		t.Errorf("stdout is %d lines at 600 carried notes:\n%s", len(lines), r.stdout)
	}
	if n := strings.Count(r.stderr, "DRAFT NOTE"); n > 5 {
		t.Errorf("stderr carries %d DRAFT NOTE lines; the set is finite and at most five", n)
	}
}

// "`to=`, `cc=` ... capped at the first 8 names with `+<k>` standing for the rest."
//
// expected= eight quoted names, then `;+12`, and the whole list in the file `path=` names.
func TestReplyRecipientFieldCapsAtEightNamesAndCounts(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	// A roster of twenty, and a group that names them all.
	var people, members []string
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("Friend%02d", i)
		people = append(people, fmt.Sprintf(`{"name": %q}`, name))
		members = append(members, fmt.Sprintf("%q", name))
	}
	people = append(people, `{"name": "Ada", "lane": "from-ada", "git_name": "Ada", "git_email": "ada@example.com"}`)
	people = append(people, `{"name": "Bo", "lane": "from-bo", "git_name": "Bo", "git_email": "bo@example.com"}`)
	writeFile(t, checkout, "participants.json", fmt.Sprintf(`{"participants": [%s], "groups": [{"name": "The twenty", "members": [%s]}]}`,
		strings.Join(people, ","), strings.Join(members, ",")))
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "a roster of twenty")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)

	drafts := t.TempDir()
	body := bodyFile(t, "Yes.\n")
	// Named one by one rather than through the group, so the FILE carries all twenty and
	// the receipt is the only thing that truncates: `+<k>` is a cap with a remedy on it.
	var all []string
	for i := 0; i < 20; i++ {
		all = append(all, fmt.Sprintf("Friend%02d", i))
	}
	r := invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body, "--to", strings.Join(all, ";"))...).mustCode(t, 0)
	if !strings.Contains(r.stdout, `;+12 `) {
		t.Errorf("the recipient field does not carry the overflow count: %s", r.stdout)
	}
	if n := strings.Count(r.stdout, `"Friend`); n != 8 {
		t.Errorf("the recipient field printed %d names, want 8: %s", n, r.stdout)
	}
	raw, err := os.ReadFile(replyPath(drafts, "bo-abcdef012345"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Friend19") {
		t.Errorf("the file the receipt names does not carry the whole list:\n%s", raw)
	}
}

// "`DRAFT REFUSED` prints every problem in one run, one line each."
//
// expected= three DRAFT REFUSED lines from one invocation with three mistakes in it.
func TestAReplyRefusalNamesEveryProblemInOneRun(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	body := bodyFile(t, "Yes.\n")
	r := invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body,
		"--to", "Nobody", "--subject", "one\ntwo", "--max-body-bytes", "0")...).mustCode(t, 2)
	if n := strings.Count(r.stderr, "DRAFT REFUSED: "); n != 3 {
		t.Errorf("three mistakes produced %d refusal lines:\n%s", n, r.stderr)
	}
	mustEmptyDir(t, drafts)
	// ACROSS THE GROUPS, and not within one of them. The first version of this verb checked
	// the flags in three passes and returned at the end of whichever one first found
	// something, so a bad --remote and a bad --subject were one line and two runs -- which
	// is the cost this rule exists to remove. A charset refusal, a roster refusal, a
	// one-line refusal, a budget refusal and an unreadable body are five different passes
	// and one run.
	five := t.TempDir()
	r = invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--reply-to", "bo-abcdef012345",
		"--body-file", filepath.Join(t.TempDir(), "no-such-body"), "--draft-dir", five,
		"--remote", "-not-a-remote", "--branch", "main",
		"--to", "Nobody", "--subject", "one\ntwo", "--max-body-bytes", "0").mustCode(t, 2)
	for _, want := range []string{
		"--remote \"-not-a-remote\": begins with a dash",
		`--max-body-bytes is a budget in bytes and is at least 1, got 0`,
		`--subject: a header line is one line`,
		`--to: Nobody names no one on this bus`,
		"--body-file ",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("five mistakes in one run did not name %q:\n%s", want, r.stderr)
		}
	}
	if n := strings.Count(r.stderr, "DRAFT REFUSED: "); n != 5 {
		t.Errorf("five mistakes produced %d refusal lines:\n%s", n, r.stderr)
	}
	mustEmptyDir(t, five)
}

// "exit 1 is the bus or the state saying NO, and exit 2 is an invocation that could not
// run" -- every row of the refusal table, asserted against its stated code.
func TestReplyExitCodesSeparateTheBusFromTheInvocation(t *testing.T) {
	t.Parallel()
	checkout, _, _ := replyBus(t)
	for _, tc := range refusalTable(t, checkout) {
		t.Run(tc.name, func(t *testing.T) {
			invoke(t, "", tc.args...).mustCode(t, tc.code).mustContain(t, "stderr", tc.want)
		})
	}
}

type refusalRow struct {
	name     string
	args     []string
	code     int
	want     string
	draftDir string
}

// refusalTable is the table in docs/SPEC-BUS-REPLY.md 389-409, "The refusals, with their
// exit codes". NINETEEN rows; thirteen are invocations and are here, and the six that need
// a fixture or a seam rather than an argument list are named here and asserted in their own
// tests, so that nothing in the table is asserted nowhere:
//
//	the checkout has diverged            TestADivergedCheckoutIsRefusedAndLosesNothing
//	not on this reader's live listing    TestTargetNotOnTheOpenListIsItsOwnRefusal (4 fixtures)
//	the target's sender is --as          TestReplyToYourOwnNoteNeedsAnExplicitTo
//	a file already exists at the path    TestReplyNeverOverwritesAnExistingDraft
//	no create-exclusive publish          TestAFilesystemWithNoCreateExclusivePublishIsRefused
//	another nova-bus holds the checkout  TestASecondReplyOnOneCheckoutWaitsAndThenRefuses
//
// TestTheRefusalTableIsAccountedForBelow holds that arithmetic, so a row that quietly left
// this file is a failure rather than a silence.
func refusalTable(t *testing.T, checkout string) []refusalRow {
	t.Helper()
	body := bodyFile(t, "Yes.\n")
	empty := bodyFile(t, "")
	dir := func() string { return t.TempDir() }
	rows := []refusalRow{}
	add := func(name string, code int, want string, draftDir string, args ...string) {
		rows = append(rows, refusalRow{name: name, args: args, code: code, want: want, draftDir: draftDir})
	}
	d1 := dir()
	add("--reply-to with --re", 2, "DRAFT REFUSED: --reply-to and --re both name a thread; name it once, because the two flags say different things", d1,
		append(replyArgs(checkout, d1, "bo-abcdef012345", body), "--re", "bo-111111111111")...)
	for _, missing := range []string{"--body-file", "--draft-dir", "--remote", "--branch"} {
		args := []string{"draft", "--bus", checkout, "--as", "Ada", "--reply-to", "bo-abcdef012345",
			"--body-file", body, "--draft-dir", "", "--remote", "origin", "--branch", "main"}
		out := []string{}
		for i := 0; i < len(args); i++ {
			if args[i] == missing {
				i++
				continue
			}
			out = append(out, args[i])
		}
		d := dir()
		for i, a := range out {
			if a == "" {
				out[i] = d
			}
		}
		add("without "+missing, 2, "DRAFT REFUSED: "+missing+" is required with --reply-to; refusing to guess", "", out...)
	}
	d2 := dir()
	add("--remote without --reply-to", 2, "DRAFT REFUSED: --remote belongs to --reply-to; without it draft runs no git and writes no file", d2,
		"draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--remote", "origin")
	add("--draft-dir inside the checkout", 2, "drafts go outside the bus, because send needs its tree clean", "",
		replyArgs(checkout, checkout, "bo-abcdef012345", body)...)
	add("--draft-dir that does not exist", 2, "; this tool creates no directories", "",
		replyArgs(checkout, filepath.Join(t.TempDir(), "nope"), "bo-abcdef012345", body)...)
	d3 := dir()
	add("an unreadable body file", 2, "DRAFT REFUSED: --body-file ", d3,
		replyArgs(checkout, d3, "bo-abcdef012345", filepath.Join(t.TempDir(), "no-such-body"))...)
	d4 := dir()
	// The existing refusal's own words, in THIS form's grammar: the reply form collects
	// every problem before it prints any, so the charset check returns its error here
	// rather than printing `nova-bus draft:` and returning on the spot.
	add("a remote failing the charset check", 2, `DRAFT REFUSED: --remote "-not-a-remote": begins with a dash, so git would read it as an option rather than a name`, d4,
		"draft", "--bus", checkout, "--as", "Ada", "--reply-to", "bo-abcdef012345",
		"--body-file", body, "--draft-dir", d4, "--branch", "main", "--remote", "-not-a-remote")
	d5 := dir()
	add("--as naming nobody", 2, `DRAFT REFUSED: --as "Nobody" names no one on this bus`, d5,
		"draft", "--bus", checkout, "--as", "Nobody", "--reply-to", "bo-abcdef012345",
		"--body-file", body, "--draft-dir", d5, "--remote", "origin", "--branch", "main")
	d6 := dir()
	add("a control character in --subject", 2, "DRAFT REFUSED: ", d6,
		replyArgs(checkout, d6, "bo-abcdef012345", body, "--subject", "one\ntwo")...)
	d7 := dir()
	add("--max-body-bytes zero", 2, `DRAFT REFUSED: --max-body-bytes is a budget in bytes and is at least 1, got 0; a budget of zero is not "unlimited"`, d7,
		replyArgs(checkout, d7, "bo-abcdef012345", body, "--max-body-bytes", "0")...)
	d8 := dir()
	add("a fetch that fails", 1, "DRAFT REFUSED: the fetch that would say whether anything has arrived on nowhere/main failed", d8,
		"draft", "--bus", checkout, "--as", "Ada", "--reply-to", "bo-abcdef012345",
		"--body-file", body, "--draft-dir", d8, "--remote", "nowhere", "--branch", "main")
	d9 := dir()
	add("a target nobody has", 1, `DRAFT REFUSED: --reply-to "bo-000000000000" is not an id on this bus`, d9,
		replyArgs(checkout, d9, "bo-000000000000", body)...)
	d10 := dir()
	add("an empty body", 1, "DRAFT REFUSED: --body-file "+empty+" is empty; a reply with no body is not a reply", d10,
		replyArgs(checkout, d10, "bo-abcdef012345", empty)...)
	d11 := dir()
	add("a body over the budget", 1, "is over --max-body-bytes 2", d11,
		replyArgs(checkout, d11, "bo-abcdef012345", body, "--max-body-bytes", "2")...)
	return rows
}

// ------------------------------------------------------------- a draft and nothing more

// "Drafting a reply does not close the note it answers, does not mark it heard, does not
// move the cursor and does not write to the bus."
//
// expected= `INBOX OPEN carrying=2 heard=0` before and after.
func TestDraftingAReplyClosesNothing(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	before := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").mustCode(t, 0)
	body := bodyFile(t, "Yes.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).mustCode(t, 0)
	after := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").mustCode(t, 0)
	if !strings.Contains(before.stdout, "INBOX OPEN carrying=2 heard=0") {
		t.Fatalf("the fixture is not what this test measures:\n%s", before.stdout)
	}
	if !strings.Contains(after.stdout, "INBOX OPEN carrying=2 heard=0") {
		t.Errorf("the draft changed the open list:\n%s", after.stdout)
	}
}

// "the end-to-end path against a disposable local bare remote: draft, `prepare`,
// `send --prepared`, and the target leaves the open list on the next read."
//
// expected= `SEND OK`, then `INBOX OPEN carrying=1 heard=0`.
func TestGeneratedReplySendsAndClosesItsTarget(t *testing.T) {
	t.Parallel()
	checkout, _, drafts := replyBus(t)
	body := bodyFile(t, "Yes, and on the merge queue too.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).mustCode(t, 0)
	path := replyPath(drafts, "bo-abcdef012345")
	artifact := invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", path).mustCode(t, 0).stdout
	invoke(t, artifact, "send", "--bus", checkout, "--as", "Ada", "--prepared-stdin",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).mustContain(t, "stdout", "SEND OK id=ada-")
	// Retrying the same artifact retains exactly one note.
	invoke(t, artifact, "send", "--bus", checkout, "--as", "Ada", "--prepared-stdin",
		"--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	if !strings.Contains(r.stdout, "INBOX OPEN carrying=1 heard=0") {
		t.Errorf("the target did not leave the open list:\n%s", r.stdout)
	}
}

// "another `nova-bus` holds this checkout | the existing lock refusal, unchanged | 1"
//
// expected= `DRAFT REFUSED: another nova-bus is running on this checkout`, exit 1.
func TestASecondReplyOnOneCheckoutWaitsAndThenRefuses(t *testing.T) {
	checkout, _, drafts := replyBus(t)
	release, err := bus.LockCheckout(checkout, checkoutLockWait)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	old := checkoutLockWait
	checkoutLockWait = 50 * 1000 * 1000 // 50ms
	defer func() { checkoutLockWait = old }()
	body := bodyFile(t, "Yes.\n")
	invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).
		mustCode(t, 1).mustContain(t, "stderr", "DRAFT REFUSED: ")
	mustEmptyDir(t, drafts)
}

// withoutFetch disables the refresh at the seam, which is what proves the fetch is
// load-bearing rather than incidental: the same invocation, against the same checkout,
// with the one step removed.
func withoutFetch(t *testing.T, fn func()) {
	t.Helper()
	old := refreshCheckout
	refreshCheckout = func(dir, remote, branch string) (bool, error) { return false, nil }
	defer func() { refreshCheckout = old }()
	fn()
}

// ------------------------------------------------------------- the repair commit's tests

// docs/SPEC-BUS-REPLY.md 254-259: "The test is the one `--bus` already makes: resolve both
// paths, follow symlinks on both sides, and refuse when the draft directory is the bus root
// or under it."
//
// The one `--bus` makes ABSOLUTIZES (internal/bus/git.go's resolved), and comparing two
// EvalSymlinks outputs without that leaves a relative `--draft-dir` inside the checkout
// looking like a path outside it -- which writes the draft into the bus and defers the
// refusal to `send`, after the turns are spent, which is the failure this row exists to
// move to the start of the job.
//
// expected= `DRAFT REFUSED: --draft-dir scratch is the bus checkout at <root>, or inside
// it; drafts go outside the bus, because send needs its tree clean`, exit 2, nothing
// written.
func TestARelativeDraftDirInsideTheCheckoutIsRefused(t *testing.T) {
	checkout, _, _ := replyBus(t)
	body := bodyFile(t, "Yes.\n")
	inside := filepath.Join(checkout, "scratch")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(checkout)
	for _, tc := range []struct{ name, bus, draftDir string }{
		{"a relative draft dir under a relative bus", ".", "scratch"},
		{"a relative draft dir under an absolute bus", checkout, "scratch"},
		{"the relative bus root itself", ".", "."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invoke(t, "", "draft", "--bus", tc.bus, "--as", "Ada", "--reply-to", "bo-abcdef012345",
				"--body-file", body, "--draft-dir", tc.draftDir, "--remote", "origin", "--branch", "main").
				mustCode(t, 2).
				mustContain(t, "stderr", "drafts go outside the bus, because send needs its tree clean")
		})
	}
	mustEmptyDir(t, inside)
}

// docs/SPEC-BUS-REPLY.md 408: "the draft directory's filesystem offers no create-exclusive
// publish | the directory, the call tried, what it said, and to name a `--draft-dir` on a
// filesystem that has one | 2"
//
// The row is asserted here rather than in refusalTable because no invocation can reach it:
// it is a property of the filesystem under `--draft-dir`, so the publish is taken out at
// the seam, which is the same thing TestReplyRefreshesBeforeItResolves does to the fetch.
//
// expected= `DRAFT REFUSED: <dir>: link said "operation not supported" ...: this filesystem
// offers no create-exclusive publish; name a --draft-dir on a filesystem that has a
// create-exclusive publish`, exit 2, nothing written.
func TestAFilesystemWithNoCreateExclusivePublishIsRefused(t *testing.T) {
	checkout, _, drafts := replyBus(t)
	body := bodyFile(t, "Yes.\n")
	old := publishDraft
	publishDraft = func(dir, name string, content []byte) (string, error) {
		return "", fmt.Errorf("%s: link said %q and %s said %q: %w", dir, "operation not supported",
			"the no-replace rename", "not supported", bus.ErrNoExclusivePublish)
	}
	defer func() { publishDraft = old }()
	r := invoke(t, "", replyArgs(checkout, drafts, "bo-abcdef012345", body)...).mustCode(t, 2)
	r.mustContain(t, "stderr", "DRAFT REFUSED: "+drafts+`: link said "operation not supported"`)
	r.mustContain(t, "stderr", "this filesystem offers no create-exclusive publish; name a --draft-dir on a filesystem that has a create-exclusive publish")
	mustEmptyDir(t, drafts)
}

// docs/SPEC-BUS-REPLY.md 174-180: the refusal "says which of the reasons it is", and there
// are four. The case that used to reach a FIFTH sentence is a note whose `Id:` line the open
// list cannot carry: `openEntryFor` blanks an id that fails `ValidOpenID`, so the entry is
// named by its PATH, while this verb was looking for it by its header id and missing.
//
// It is on the listing, so it is a legal target, and the name the draft writes is the name
// the listing uses for it.
//
// expected= exit 0 and `re=from-bo/2026-09-08T0800Z-an-id-nobody-can-carry.md`, with the
// draft's filename the derived `legacy-<12 hex>` and holding no path separator.
func TestAReplyToANoteWhoseIdTheOpenListCannotCarryIsResolvedByPath(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	const path = "from-bo/2026-09-08T0800Z-an-id-nobody-can-carry.md"
	writeFile(t, checkout, path,
		"From: Bo\nTo: Ada\nDate: Tue Sep  8 08:00:00 UTC 2026\nId: not an id this tool would ever write\nSubject: An id nobody can carry\n\nAsking.\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "an id the open list cannot carry")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)

	drafts := t.TempDir()
	body := bodyFile(t, "Yes.\n")
	invoke(t, "", replyArgs(checkout, drafts, path, body)...).
		mustCode(t, 0).mustContain(t, "stdout", "re="+path)
	names := draftsIn(t, drafts)
	if len(names) != 1 || !strings.Contains(names[0], "-re-legacy-") || strings.ContainsAny(names[0], "/\\") {
		t.Fatalf("the draft is named %v; a name the open list cannot carry is the derived one, and it is one path segment", names)
	}
	raw, err := os.ReadFile(filepath.Join(drafts, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Re: "+path+"\n") {
		t.Errorf("the Re line is not the name the listing uses for this note:\n%s", raw)
	}
}

// The count the comment on refusalTable claims, asserted rather than trusted, so that a row
// which quietly left this file is a failure and not a silence.
//
// Thirteen of the nineteen rows at docs/SPEC-BUS-REPLY.md 389-409 are driven from an
// argument list here, in seventeen fixtures -- the missing-flag row is four flags and the
// body row is empty AND over-budget -- and the other six have their own test, named in the
// comment on refusalTable above.
//
// expected= 17 fixtures over 13 rows, and 13 + 6 = 19.
func TestTheRefusalTableIsAccountedForBelow(t *testing.T) {
	t.Parallel()
	checkout, _, _ := replyBus(t)
	const (
		rowsDrivenHere    = 13
		rowsOwnTests      = 6
		fixturesOverThem  = 17
		rowsInTheDocument = 19
	)
	if got := len(refusalTable(t, checkout)); got != fixturesOverThem {
		t.Errorf("refusalTable drives %d fixtures, want %d over %d rows", got, fixturesOverThem, rowsDrivenHere)
	}
	if rowsDrivenHere+rowsOwnTests != rowsInTheDocument {
		t.Errorf("%d rows are accounted for, and docs/SPEC-BUS-REPLY.md 389-409 is %d", rowsDrivenHere+rowsOwnTests, rowsInTheDocument)
	}
	// The six are named, not merely counted: each has to exist in this package.
	for _, name := range []string{
		"TestADivergedCheckoutIsRefusedAndLosesNothing",
		"TestTargetNotOnTheOpenListIsItsOwnRefusal",
		"TestReplyToYourOwnNoteNeedsAnExplicitTo",
		"TestReplyNeverOverwritesAnExistingDraft",
		"TestAFilesystemWithNoCreateExclusivePublishIsRefused",
		"TestASecondReplyOnOneCheckoutWaitsAndThenRefuses",
	} {
		raw, err := os.ReadFile("reply_test.go")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "func "+name+"(") {
			t.Errorf("refusalTable's comment names %s and this package does not define it", name)
		}
	}
}
