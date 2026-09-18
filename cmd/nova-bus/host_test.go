package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `--host` end to end. The bench it is for: the keeper on the Studio and the bud on the Air
// both post as Rowan, and until this flag existed they were told apart by a `[bud air]` in
// the subject, which spent the subject line on routing.

func TestSendHostWritesTheHostLineAndInboxShowsIt(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nSubject: from the air\n\nthe body of the note\n")
	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--host", "air",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-").
		mustContain(t, "stdout", "SEND NOTE this draft had no Host line; --host says you are posting from \"air\"")
	if !strings.Contains(laneNoteText(t, checkout, "from-ada"), "From: Ada\nHost: air\nTo: Bo\n") {
		t.Fatalf("the note on the bus carries no Host line:\n%s", laneNoteText(t, checkout, "from-ada"))
	}
	// And Bo's listing says which machine it came from, beside the sender.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Bo", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "from=Ada host=air addr=to")
}

// The defaults file is how a bench sets it once. It is the same key=value file
// receipt-max-words is read from, and the flag still wins.
func TestHostComesFromTheDefaultsFileWhenTheFlagIsAbsent(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	writeBusFile(t, checkout, ".nova-bus/defaults", "# this bench\nreceipt-max-words=40\nhost=studio\n")
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nSubject: from the defaults\n\nthe body of the note\n")
	invoke(t, "", "send", "--bus", checkout, "--file", draft,
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0)
	if !strings.Contains(laneNoteText(t, checkout, "from-ada"), "Host: studio\n") {
		t.Fatalf("the defaults file's host did not reach the note:\n%s", laneNoteText(t, checkout, "from-ada"))
	}
}

func TestTheHostFlagBeatsTheDefaultsFile(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	writeBusFile(t, checkout, ".nova-bus/defaults", "host=studio\n")
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nSubject: the flag wins\n\nthe body of the note\n")
	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--host", "air",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0)
	note := laneNoteText(t, checkout, "from-ada")
	if !strings.Contains(note, "Host: air\n") || strings.Contains(note, "studio") {
		t.Fatalf("the flag did not beat the defaults file:\n%s", note)
	}
}

// A host that is given and unusable is a refusal by name, from the flag and from the
// defaults file alike: a host silently dropped is the subject convention all over again.
func TestAnUnusableHostIsRefusedByName(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nSubject: s\n\nthe body of the note\n")
	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--host", "The Air",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).
		mustContain(t, "stderr", "nova-bus send: --host").
		mustContain(t, "stderr", "one space-separated")

	writeBusFile(t, checkout, ".nova-bus/defaults", "host=The Air\n")
	invoke(t, "", "send", "--bus", checkout, "--file", draft,
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).
		mustContain(t, "stderr", "nova-bus send: --host")
}

func TestReplyHostWritesTheHostLine(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "Green on all three platforms.\n")
	invoke(t, "", "reply", "--bus", checkout, "--as", "Ada", "--re", "bo-abcdef012345", "--file", draft,
		"--host", "air", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "REPLY OK id=ada-")
	if !strings.Contains(laneNoteText(t, checkout, "from-ada"), "From: Ada\nHost: air\nTo: ") {
		t.Fatalf("the reply carries no Host line under From:\n%s", laneNoteText(t, checkout, "from-ada"))
	}
}

// THE COMPATIBILITY CLAIM at the command line: a send with no --host writes the note it
// always wrote, and the listing prints the line it always printed -- no `host=` field
// between `from=` and `addr=`, which is where every line parser on this bus looks.
func TestASendWithNoHostIsTheLineItAlwaysWas(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nSubject: no host here\n\nthe body of the note\n")
	invoke(t, "", "send", "--bus", checkout, "--file", draft,
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0)
	if strings.Contains(laneNoteText(t, checkout, "from-ada"), "Host:") {
		t.Fatalf("a send with no --host wrote a Host line:\n%s", laneNoteText(t, checkout, "from-ada"))
	}
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Bo", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "from=Ada addr=to")
	if strings.Contains(r.stdout, "host=") {
		t.Fatalf("a listing with no hosted note printed a host= field:\n%s", r.stdout)
	}
}

// laneNoteText is the bytes of the one note in a lane, read back off the checkout.
// laneNote (preflight_reply_test.go) answers the path; this answers what is in it.
func laneNoteText(t *testing.T, checkout, lane string) string {
	t.Helper()
	dir := filepath.Join(checkout, lane)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") && e.Name() != "README.md" {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			return string(raw)
		}
	}
	t.Fatalf("no note in %s", dir)
	return ""
}

// writeBusFile puts one of the tool's own per-clone files into the checkout. `.nova-bus/`
// is never a note and never a dirty checkout, which is why a defaults file can be written
// between a clone and a send with no hand step in between.
func writeBusFile(t *testing.T, checkout, rel, content string) {
	t.Helper()
	full := filepath.Join(checkout, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
