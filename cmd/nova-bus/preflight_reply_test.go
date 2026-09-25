package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// The red tests of docs/SPEC-BUS.md's `send --file` preflight and `reply` section. Each is
// written first, against the behavior the section promises, and each names one sentence.

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))
}

func laneNote(t *testing.T, checkout, lane string) string {
	t.Helper()
	for _, name := range strings.Fields(gitIn(t, checkout, "ls-tree", "-r", "--name-only", "HEAD")) {
		if strings.HasPrefix(name, lane+"/") && strings.HasSuffix(name, ".md") {
			return name
		}
	}
	t.Fatalf("no note in %s on HEAD", lane)
	return ""
}

func writeDraftFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSendRefusesAHandWrittenId(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nId: ada-000000000000\nSubject: s\n\nbody\n")
	before := headSHA(t, checkout)
	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).
		mustContain(t, "stderr", "nova-bus send: the tool mints the Id; delete the Id: header from "+draft)
	if got := headSHA(t, checkout); got != before {
		t.Fatalf("a refused draft committed: HEAD moved from %s to %s", before, got)
	}
	if entries, err := os.ReadDir(filepath.Join(checkout, "from-ada")); err == nil && len(entries) > 0 {
		t.Fatalf("a refused draft left %d files in the lane", len(entries))
	}
}

func TestSendRefusesTwoIdsInRe(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nRe: bo-111111111111, bo-222222222222\nSubject: s\n\nbody\n")
	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).
		mustContain(t, "stderr", "nova-bus send: Re: names one thread; name one id in "+draft)
}

func TestSendWarnsOnceOnADateItReplaces(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nDate: Mon Jan  1 00:00:00 UTC 2001\nSubject: the clock\n\nbody\n")
	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND NOTE")
	note := laneNote(t, checkout, "from-ada")
	content := gitIn(t, checkout, "show", "HEAD:"+note)
	if !strings.Contains(content, "Date: "+now().UTC().Format(bus.DateLayout)) {
		t.Fatalf("the committed note does not carry the fake clock's date:\n%s", content)
	}
	if strings.Contains(content, "2001") {
		t.Fatalf("the draft's own Date line survived:\n%s", content)
	}
}

func TestSendDryRunPrintsTheShapedNoteAndWritesNothing(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nSubject: dry run\n\nthe body of the note\n")
	before := headSHA(t, checkout)
	r := invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", "origin", "--branch", "main", "--attempts", "3", "--dry-run").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND DRAFT id=ada-").
		mustContain(t, "stdout", "SEND DRAFT END id=ada-").
		mustContain(t, "stdout", "the body of the note")
	if !strings.Contains(r.stdout, "subject=dry") {
		t.Fatalf("the SEND DRAFT line does not name the subject: %s", r.stdout)
	}
	if got := headSHA(t, checkout); got != before {
		t.Fatalf("--dry-run committed: HEAD moved from %s to %s", before, got)
	}
	if entries, err := os.ReadDir(filepath.Join(checkout, "from-ada")); err == nil && len(entries) > 0 {
		t.Fatalf("--dry-run left %d files in the lane", len(entries))
	}
}

func TestReplyFillsFromToReSubjectFromTheOriginal(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "Green on all three platforms.\n")
	invoke(t, "", "reply", "--bus", checkout, "--as", "Ada", "--re", "bo-abcdef012345", "--file", draft,
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "REPLY OK id=ada-").
		mustContain(t, "stdout", "re=bo-abcdef012345")
	content := gitIn(t, checkout, "show", "HEAD:"+laneNote(t, checkout, "from-ada"))
	for _, want := range []string{
		"From: Ada\n",
		"To: Bo Quill\n",
		"Re: bo-abcdef012345\n",
		"Subject: Re: A question about the gate\n",
		"Green on all three platforms.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("the committed reply does not carry %q:\n%s", want, content)
		}
	}
}

func TestReplyRefusesAnUnknownRe(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "body\n")
	before := headSHA(t, checkout)
	invoke(t, "", "reply", "--bus", checkout, "--as", "Ada", "--re", "bo-999999999999", "--file", draft,
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).
		mustContain(t, "stderr", "nova-bus reply: --re bo-999999999999 names no note; run nova-bus inbox --open and name one")
	if got := headSHA(t, checkout); got != before {
		t.Fatalf("a refused reply committed: HEAD moved from %s to %s", before, got)
	}
	if entries, err := os.ReadDir(filepath.Join(checkout, "from-ada")); err == nil && len(entries) > 0 {
		t.Fatalf("a refused reply left %d files in the lane", len(entries))
	}
}

func TestReplyRefusesAHandShapedHeader(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "To: Everyone\n\nbody\n")
	invoke(t, "", "reply", "--bus", checkout, "--as", "Ada", "--re", "bo-abcdef012345", "--file", draft,
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).
		mustContain(t, "stderr", "nova-bus reply: reply fills From, To, Re and Subject; delete the To: line from "+draft)
}

func TestReplyAdvanceMovesTheCursorInTheReplyCommit(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "answering, and moving on.\n")
	invoke(t, "", "reply", "--bus", checkout, "--as", "Ada", "--re", "bo-abcdef012345", "--file", draft,
		"--remote", "origin", "--branch", "main", "--attempts", "3", "--advance").
		mustCode(t, 0).
		mustContain(t, "stdout", "advanced=true")
	files := strings.Fields(gitIn(t, checkout, "ls-tree", "-r", "--name-only", "HEAD"))
	note, cursor := false, false
	for _, name := range files {
		if strings.HasPrefix(name, "from-ada/") && strings.HasSuffix(name, ".md") {
			note = true
		}
		if name == "from-ada/CURSOR" {
			cursor = true
		}
	}
	if !note || !cursor {
		t.Fatalf("one commit must hold both the reply and the cursor, got note=%t cursor=%t in %v", note, cursor, files)
	}
}

func TestReplyAdvanceWithDryRunIsRefused(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "body\n")
	before := headSHA(t, checkout)
	invoke(t, "", "reply", "--bus", checkout, "--as", "Ada", "--re", "bo-abcdef012345", "--file", draft,
		"--remote", "origin", "--branch", "main", "--attempts", "3", "--advance", "--dry-run").
		mustCode(t, 2).
		mustContain(t, "stderr", "nova-bus reply: --advance moves the cursor and --dry-run writes nothing; drop one")
	if got := headSHA(t, checkout); got != before {
		t.Fatalf("a refused reply committed: HEAD moved from %s to %s", before, got)
	}
	if entries, err := os.ReadDir(filepath.Join(checkout, "from-ada")); err == nil && len(entries) > 0 {
		t.Fatalf("a refused reply left %d files in the lane", len(entries))
	}
}
