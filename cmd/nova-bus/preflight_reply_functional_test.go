//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/require"
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
	require.FailNowf(t, "assertion failed", "no note in %s on HEAD", lane)
	return ""
}

func writeDraftFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "draft.md")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
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
		mustContain(t, "stderr", "SEND REFUSED: the tool mints the Id; delete the Id: header from "+draft)
	{
		got := headSHA(t, checkout)
		require.Falsef(t, got != before, "a refused draft committed: HEAD moved from %s to %s", before, got)
	}
	{
		entries, err := os.ReadDir(filepath.Join(checkout, "from-ada"))
		require.Falsef(t, err == nil && len(entries) > 0, "a refused draft left %d files in the lane", len(entries))
	}
}

func TestSendRefusesTwoIdsInRe(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nRe: bo-111111111111, bo-222222222222\nSubject: s\n\nbody\n")
	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).
		mustContain(t, "stderr", "SEND REFUSED: Re: names one thread; name one id in "+draft)
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
	require.Containsf(t, content, "Date: "+now().UTC().Format(bus.DateLayout), "the committed note does not carry the fake clock's date:\n%s", content)
	require.NotContainsf(t, content, "2001", "the draft's own Date line survived:\n%s", content)
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
	require.Containsf(t, r.stdout, "subject=dry", "the SEND DRAFT line does not name the subject: %s", r.stdout)
	{
		got := headSHA(t, checkout)
		require.Falsef(t, got != before, "--dry-run committed: HEAD moved from %s to %s", before, got)
	}
	{
		entries, err := os.ReadDir(filepath.Join(checkout, "from-ada"))
		require.Falsef(t, err == nil && len(entries) > 0, "--dry-run left %d files in the lane", len(entries))
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
		require.Containsf(t, content, want, "the committed reply does not carry %q:\n%s", want, content)
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
		mustContain(t, "stderr", "REPLY REFUSED: --re bo-999999999999 names no note on this bus; name one from your open list; run: nova-bus inbox --bus ")
	{
		got := headSHA(t, checkout)
		require.Falsef(t, got != before, "a refused reply committed: HEAD moved from %s to %s", before, got)
	}
	{
		entries, err := os.ReadDir(filepath.Join(checkout, "from-ada"))
		require.Falsef(t, err == nil && len(entries) > 0, "a refused reply left %d files in the lane", len(entries))
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
		mustContain(t, "stderr", "REPLY REFUSED: reply fills From, To, Re and Subject; delete the To: line from "+draft)
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
	require.Falsef(t, !note || !cursor, "one commit must hold both the reply and the cursor, got note=%t cursor=%t in %v", note, cursor, files)
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
		mustContain(t, "stderr", "REPLY REFUSED: --advance moves the cursor and --dry-run writes nothing; drop one")
	{
		got := headSHA(t, checkout)
		require.Falsef(t, got != before, "a refused reply committed: HEAD moved from %s to %s", before, got)
	}
	{
		entries, err := os.ReadDir(filepath.Join(checkout, "from-ada"))
		require.Falsef(t, err == nil && len(entries) > 0, "a refused reply left %d files in the lane", len(entries))
	}
}
