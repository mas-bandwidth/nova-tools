package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// THE FIRST SEND, at the binary. Each tolerance is asserted twice -- the SEND NOTE line
// the run printed, and the BYTES the note landed as -- because a notice about something
// that did not happen and a rewrite nobody was told about are the two ways this could be
// wrong.
func TestSendTolerancesPrintANoticeAndLandTheNote(t *testing.T) {
	cases := []struct {
		name, draft, as string
		notice          string
		wantLines       []string
		wantBody        string
	}{
		{
			name:      "a markdown heading for the subject",
			draft:     "# On the merge queue\n\nFrom: Ada\nTo: Bo\n\nThe gate never ran.\n",
			notice:    `SEND NOTE the first line was a markdown heading, so it is this note's Subject ("On the merge queue"), and it is not in the body`,
			wantLines: []string{"From: Ada", "To: Bo", "Subject: On the merge queue"},
			wantBody:  "The gate never ran.",
		},
		{
			name:      "a Date line the writer pasted",
			draft:     "From: Ada\nTo: Bo\nDate: Tue Sep  8 09:00:00 UTC 2026\nSubject: s\n\nbody\n",
			notice:    `SEND NOTE this draft carried a Date line ("Tue Sep  8 09:00:00 UTC 2026"); send writes the date from the clock, so yours is replaced, and says so`,
			wantLines: []string{"Date: Wed Sep  9 12:34:56 UTC 2026"},
			wantBody:  "body",
		},
		{
			name:      "no From line, and --as to say who is sending",
			draft:     "To: Bo\nSubject: s\n\nbody\n",
			as:        "the archivist",
			notice:    `SEND NOTE this draft had no From line; --as says you are "Ada", so send wrote "From: Ada"`,
			wantLines: []string{"From: Ada"},
			wantBody:  "body",
		},
		{
			name:      "blank lines above the header",
			draft:     "\n\nFrom: Ada\nTo: Bo\nSubject: s\n\nbody\n",
			notice:    "SEND NOTE 2 blank lines stood above the header; they are skipped, and the header is read from the first Key: value line",
			wantLines: []string{"From: Ada", "To: Bo"},
			wantBody:  "body",
		},
		{
			name:      "a key in markdown bold",
			draft:     "From: Ada\n**To**: Bo\nSubject: s\n\nbody\n",
			notice:    "SEND NOTE line 2: the key \"**To**\" was in markdown bold; headers are plain `Key: value`, so it is read as \"To:\"",
			wantLines: []string{"To: Bo"},
			wantBody:  "body",
		},
		{
			name:      "the whole house style at once, which is what a first send is",
			draft:     "# A new line: on the bus\n\nDate: Tue Sep  8 09:00:00 UTC 2026\nTo: Bo\n\nHello, all.\n",
			as:        "Ada",
			notice:    `SEND NOTE this draft had no From line; --as says you are "Ada", so send wrote "From: Ada"`,
			wantLines: []string{"From: Ada", "To: Bo", "Subject: A new line: on the bus", "Date: Wed Sep  9 12:34:56 UTC 2026"},
			wantBody:  "Hello, all.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermetic(t)
			checkout, _ := busDir(t)
			args := []string{"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push"}
			if tc.as != "" {
				args = append(args, "--as", tc.as)
			}
			r := invoke(t, tc.draft, args...).mustCode(t, 0).mustContain(t, "stdout", tc.notice)
			path := field(t, r.stdout, "path=")
			stored, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(path)))
			if err != nil {
				t.Fatal(err)
			}
			text := string(stored)
			for _, line := range tc.wantLines {
				if !strings.Contains(text, line+"\n") {
					t.Fatalf("the note on the bus has no %q line:\n%s", line, text)
				}
			}
			body := text[strings.Index(text, "\n\n")+2:]
			if strings.TrimRight(body, "\n") != tc.wantBody {
				t.Fatalf("body = %q, want %q", body, tc.wantBody)
			}
			// And the note that landed is one every reader on the bus reads with no
			// tolerance of its own.
			if _, err := bus.ParseNote(path, text); err != nil {
				t.Fatalf("the stored note does not parse strictly: %v\n%s", err, text)
			}
		})
	}
}

// The refusals that stay, and the sentence each of them says.
func TestSendStillRefusesWhatItCannotGuessAtTheBinary(t *testing.T) {
	cases := []struct{ name, draft, want string }{
		{"a recipient not on the roster", "From: Ada\nTo: Boe\nSubject: s\n\nbody\n", `To: "Boe" names no one on this bus`},
		{"no To line at all", "From: Ada\nSubject: s\n\nbody\n", "no To line"},
		{"an unknown key that is not a bold key", "From: Ada\nTo: Bo\nBranch: main\nSubject: s\n\nbody\n", `unknown header key "Branch"`},
		{"a Re naming nothing", "From: Ada\nTo: Bo\nRe: bo-deadbeefcafe\nSubject: s\n\nbody\n", "a slug is not a thread"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermetic(t)
			checkout, _ := busDir(t)
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

// Every problem in ONE run, one line each. Three runs to find three mistakes is the
// failure this closes.
func TestARefusalNamesEveryProblemOnItsOwnLine(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	r := invoke(t, "From: Ada\nTo: Boe\nRe: bo-deadbeefcafe\nSubject:\n\n\n",
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1)
	lines := strings.Split(strings.TrimRight(r.stderr, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("the run printed %d lines, want one per problem:\n%s", len(lines), r.stderr)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "SEND FAIL (stdin): ") {
			t.Fatalf("a refusal line is off the grammar: %q", line)
		}
	}
	for _, want := range []string{`"Boe" names no one`, "no Subject line", "the note has no body", "a slug is not a thread"} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("no line named %q:\n%s", want, r.stderr)
		}
	}
}

// The draft verb: its stdout is a FILE, and the file is a note this tool's own parser
// reads back and this tool's own send accepts.
func TestDraftPrintsASkeletonTheParserReadsBack(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	r := invoke(t, "", "draft", "--bus", checkout, "--as", "the archivist",
		"--to", "Bo", "--cc", "Dana", "--subject", "The gate", "--re", "bo-abcdef012345").mustCode(t, 0)
	if r.stderr != "" {
		t.Fatalf("draft wrote to stderr: %q", r.stderr)
	}
	n, err := bus.ParseNote("", r.stdout)
	if err != nil {
		t.Fatalf("the skeleton does not parse: %v\n%s", err, r.stdout)
	}
	if n.Header.From != "Ada" || n.Header.To != "Bo" || n.Header.Cc != "Dana" || n.Header.Subject != "The gate" {
		t.Fatalf("skeleton read back as %+v", n.Header)
	}
	if len(n.Header.Re) != 1 || n.Header.Re[0] != "bo-abcdef012345" {
		t.Fatalf("Re read back as %v", n.Header.Re)
	}
	if n.Header.Date != "" || n.Header.ID != "" {
		t.Fatalf("the skeleton carries a Date or an Id, which are the tool's to write:\n%s", r.stdout)
	}
	// And it is a draft send takes: a body over the placeholder, and it lands.
	sent := strings.Replace(r.stdout, bus.PlaceholderBody, "Bo, the key is misspelled in the matrix.", 1)
	invoke(t, sent, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 0).mustContain(t, "stdout", "SEND OK id=ada-")
}

// The draft verb refuses what it cannot spell for you, and refuses all of it at once.
func TestDraftRefusesEveryNameItCannotResolve(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Adda", "--to", "Boe", "--re", "nothing-here").mustCode(t, 2)
	lines := strings.Split(strings.TrimRight(r.stderr, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("draft printed %d refusals, want one per problem:\n%s", len(lines), r.stderr)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "DRAFT REFUSED: ") {
			t.Fatalf("a refusal line is off the grammar: %q", line)
		}
	}
	if r.stdout != "" {
		t.Fatalf("a refused draft still printed a skeleton:\n%s", r.stdout)
	}
	// A sender with no lane has nowhere to send from, and is refused here rather than at
	// the end of a note somebody has written.
	invoke(t, "", "draft", "--bus", checkout, "--as", "Dana", "--to", "Ada").
		mustCode(t, 2).mustContain(t, "stderr", "has no lane on this bus")
	// A --subject that would forge a second header line is refused before anything prints.
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "s\nTo: Dana").
		mustCode(t, 2).mustContain(t, "stderr", "a header line is one line")
	// And the required flags are required, like everywhere else in this tool.
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada").
		mustCode(t, 2).mustContain(t, "stderr", "--to is required; refusing to guess")
}
