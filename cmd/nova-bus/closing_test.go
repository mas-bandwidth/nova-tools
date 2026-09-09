package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CLOSING, at the binary.
//
// The bug these cover is one line's, and it is not a bug in any single command. He read his
// inbox, answered every note by hand, and watched `carrying=` climb to seventy-four --
// because a hand-written reply carries no `Re:` line, the answered rule IS a Re line, and
// nothing anywhere told him. Every note he had ever answered was still open, for ever.
//
// So: a Re line can be written by the tool (`draft --re`), it can name the SUBJECT a line
// answering by hand actually has in front of it, and a draft that looks like a reply and
// names nothing is told so. None of the three is a refusal; all three are notices.

// openBoth advances Ada's cursor over the fixture so she is carrying its two notes, which
// is what every test below needs before it can name one of them by subject.
func openBoth(t *testing.T, checkout string) {
	t.Helper()
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2")
}

// The id is the one thing a line answering a note does not have in front of it. --re takes
// the subject instead and the skeleton comes back carrying the id.
func TestDraftReTakesTheSubjectOfAnOpenNoteAndWritesTheID(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	openBoth(t, checkout)

	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo",
		"--re", "A question about the gate", "--subject", "Yes, on the merge queue too").
		mustCode(t, 0).
		mustContain(t, "stdout", "Re: bo-abcdef012345").
		mustContain(t, "stderr", `DRAFT NOTE --re named the subject "A question about the gate" rather than an id; the skeleton names the open note bo-abcdef012345 from Bo`)
	if strings.Contains(r.stdout, "A question about the gate") {
		t.Fatalf("the skeleton carried the subject where the id belongs:\n%s", r.stdout)
	}

	// A subject already written as a reply names the same note: `Re: ` comes off both sides
	// before they are compared, which is the shape a second turn of a thread arrives in.
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo",
		"--re", "Re: A question about the gate", "--subject", "s").
		mustCode(t, 0).mustContain(t, "stdout", "Re: bo-abcdef012345")

	// An id still means an id, and nothing that resolved before resolves differently.
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo",
		"--re", "bo-111111111111", "--subject", "s").
		mustCode(t, 0).mustContain(t, "stdout", "Re: bo-111111111111")

	// And a subject that names nothing is the refusal it always was, now saying all three
	// of the things a Re line may be.
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo",
		"--re", "a subject nobody wrote", "--subject", "s").
		mustCode(t, 2).
		mustContain(t, "stderr", `--re "a subject nobody wrote" is not an id on this bus, not a note that exists, and not the subject of a note on your open list`)

	// The match is case-sensitive: two notes on a busy lane differ by a capital, and a tool
	// that folded them would close the wrong one and say it had closed the right one.
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo",
		"--re", "a question about the gate", "--subject", "s").
		mustCode(t, 2).mustContain(t, "stderr", "not the subject of a note on your open list")
}

// The same thing at send, where a draft written by hand actually arrives. The Re line names
// a subject; the note that lands names the id; and the note it answers comes off the list.
func TestSendResolvesAReSubjectToTheOpenNoteAndClosesIt(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	openBoth(t, checkout)

	draft := "From: Ada\nTo: Bo\nRe: A question about the gate\nSubject: Re: A question about the gate\n\nYes, on the merge queue too.\n"
	r := invoke(t, draft, "send", "--bus", checkout, "--stdin",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", `SEND NOTE Re named the subject "A question about the gate" rather than an id; it is the open note bo-abcdef012345 from Bo, and this note closes it`)
	// The bytes on the bus carry the ID, because the id is what every reader resolves.
	path := field(t, r.stdout, "path=")
	stored := readFile(t, checkout, path)
	if !strings.Contains(stored, "Re: bo-abcdef012345") {
		t.Fatalf("the note that landed does not name the id it answers:\n%s", stored)
	}
	if strings.Contains(stored, "\nRe: A question about the gate\n") {
		t.Fatalf("the note that landed still carries the subject on its Re line:\n%s", stored)
	}
	// And a note that answers something is not told it answers nothing.
	if strings.Contains(r.stdout, "answers nothing") {
		t.Fatalf("a note with a Re line was told it answers nothing:\n%s", r.stdout)
	}

	// The whole point: it CLOSED. Ada was carrying two and is carrying one.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=1")
}

// Two open notes with one subject is a thread somebody re-raised. The newest is the turn
// being answered; the tool closes it, says which, and says how to be exact.
func TestAReSubjectMatchingTwoNotesClosesTheNewestAndSaysSo(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	for i, id := range []string{"bo-aaaa11112222", "bo-bbbb33334444"} {
		writeFile(t, checkout, fmt.Sprintf("from-bo/2026-09-08T0%d00Z-twice-%s.md", i+1, id),
			fmt.Sprintf("From: Bo\nTo: Ada\nDate: Tue Sep  8 0%d:00:00 UTC 2026\nId: %s\nSubject: The gate, again\n\nRaised a second time.\n", i+1, id))
		appendFile(t, checkout, "from-bo/INDEX",
			fmt.Sprintf("%s\tfrom-bo/2026-09-08T0%d00Z-twice-%s.md\t2026-09-08T0%d:00:00Z\tAda\t-\n", id, i+1, id, i+1))
	}
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "twice")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=4")

	draft := "From: Ada\nTo: Bo\nRe: The gate, again\nSubject: Re: The gate, again\n\nAnswered.\n"
	r := invoke(t, draft, "send", "--bus", checkout, "--stdin",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND NOTE Re: subject matched 2 notes; closed the newest bo-bbbb33334444; name the id to be exact")
	stored := readFile(t, checkout, field(t, r.stdout, "path="))
	if !strings.Contains(stored, "Re: bo-bbbb33334444") {
		t.Fatalf("the newest of the two was not the one named:\n%s", stored)
	}
	// One closed, not both: the older is still Ada's, and is named by id if she means it.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=3")
}

// THE NOTICE THAT WOULD HAVE SAVED SEVENTY-FOUR NOTES. A draft that reads like a reply and
// names nothing is told so -- and sent anyway, because a note that answers nothing is the
// commonest thing on the bus and refusing one would be absurd.
func TestSendSaysWhenADraftThatLooksLikeAReplyAnswersNothing(t *testing.T) {
	const notice = "SEND NOTE this note answers nothing (no Re: line); if it is a reply, name the note: Re: <id>"
	cases := []struct {
		name, draft string
		want        bool
	}{
		{
			// The subject says it is a reply and the header does not.
			name:  "a subject written as a reply",
			draft: "From: Ada\nTo: Bo\nSubject: Re: A question about the gate\n\nYes.\n",
			want:  true,
		},
		{
			// No Re: in the subject either -- but there is exactly one recipient, and she
			// is holding two open notes from him. This is the shape a hand-written answer
			// takes when the writer does not use the convention at all.
			name:  "one recipient who is waiting on you",
			draft: "From: Ada\nTo: Bo\nSubject: The gate runs on the queue now\n\nDone.\n",
			want:  true,
		},
		{
			name:  "a subject in the shape a small model writes it",
			draft: "From: Ada\nTo: Bo\nSubject: RE: anything at all\n\nSure.\n",
			want:  true,
		},
		{
			// It names a note, so there is nothing to say.
			name:  "a draft that names the note it answers",
			draft: "From: Ada\nTo: Bo\nRe: bo-abcdef012345\nSubject: Re: A question about the gate\n\nYes.\n",
			want:  false,
		},
		{
			// A broadcast says nothing about any one of its recipients, so neither does
			// this: the guess is deliberately the narrow one.
			name:  "a note to everybody",
			draft: "From: Ada\nTo: Everybody on the bus\nSubject: A finding, for the record\n\nHere it is.\n",
			want:  false,
		},
		{
			// One recipient, and nothing of his is open. Nothing suggests a reply.
			name:  "one recipient with nothing open",
			draft: "From: Ada\nTo: Dana\nSubject: A question of my own\n\nAsking.\n",
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermetic(t)
			checkout, _ := busDir(t)
			openBoth(t, checkout)
			r := invoke(t, tc.draft, "send", "--bus", checkout, "--stdin",
				"--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
				mustCode(t, 0)
			if got := strings.Contains(r.stdout, notice); got != tc.want {
				t.Fatalf("the answers-nothing notice was %v, want %v:\n%s", got, tc.want, r.stdout)
			}
		})
	}
}

// A reader with no open list at all -- a line that has never advanced a cursor -- gets the
// refusals it always got and no notice about anything. Nothing here may make a send that
// worked stop working.
func TestAReSubjectOnALaneWithNoOpenListIsTheRefusalItAlwaysWas(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "From: Ada\nTo: Bo\nRe: A question about the gate\nSubject: s\n\nbody\n",
		"send", "--bus", checkout, "--stdin",
		"--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 1).
		mustContain(t, "stderr", "not the subject of a note on your open list")
}

// readFile is one file out of a checkout, for a test that asserts on the BYTES a send left
// rather than on the line it printed. Both halves matter: a notice about a rewrite that did
// not happen and a rewrite nobody was told about are the two ways this could be wrong.
func readFile(t *testing.T, root, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
