package bus

import (
	"strings"
	"testing"
)

// Each tolerance, by the two things that can be checked about it: the BYTES the note is
// stored as, and the NOTICE the run printed. A tolerance whose notice nobody asserts is a
// tool quietly rewriting what a person wrote.
func TestSendTolerancesStoreTheNoteAndSayWhatTheyDid(t *testing.T) {
	cases := []struct {
		name, text, as string
		wantHeader     []string // lines the stored note must carry, in Render's order
		wantBody       string
		wantNotice     string
		wantNoNotice   string
	}{
		{
			name:       "a markdown heading becomes the Subject",
			text:       "# On the merge queue\n\nFrom: Ada\nTo: Bo\n\nThe gate never ran at all.\n",
			wantHeader: []string{"From: Ada", "To: Bo", "Subject: On the merge queue"},
			wantBody:   "The gate never ran at all.",
			wantNotice: `the first line was a markdown heading, so it is this note's Subject ("On the merge queue"), and it is not in the body`,
		},
		{
			name:         "a heading over a draft that has its own Subject is dropped, and said so",
			text:         "# A title somebody pasted\n\nFrom: Ada\nTo: Bo\nSubject: The real one\n\nbody\n",
			wantHeader:   []string{"Subject: The real one"},
			wantBody:     "body",
			wantNotice:   `the first line was the markdown heading "A title somebody pasted" and this draft has its own Subject line; the heading is not in the note`,
			wantNoNotice: "it is not in the body",
		},
		{
			name:       "a Date line is replaced, and the tool says so",
			text:       "From: Ada\nTo: Bo\nDate: Tue Sep  8 09:00:00 UTC 2026\nSubject: s\n\nbody\n",
			wantHeader: []string{"Date: Wed Sep  9 12:34:56 UTC 2026"},
			wantBody:   "body",
			wantNotice: `this draft carried a Date line ("Tue Sep  8 09:00:00 UTC 2026"); send writes the date from the clock, so yours is replaced, and says so`,
		},
		{
			name:       "--as writes the From line a draft has not got",
			text:       "To: Bo\nSubject: s\n\nbody\n",
			as:         "the archivist",
			wantHeader: []string{"From: Ada"},
			wantBody:   "body",
			wantNotice: `this draft had no From line; --as says you are "Ada", so send wrote "From: Ada"`,
		},
		{
			name:       "blank lines above the header are skipped",
			text:       "\n\nFrom: Ada\nTo: Bo\nSubject: s\n\nbody\n",
			wantHeader: []string{"From: Ada"},
			wantBody:   "body",
			wantNotice: "2 blank lines stood above the header; they are skipped, and the header is read from the first Key: value line",
		},
		{
			name:       "one blank line above the header is skipped, in the singular",
			text:       "\nFrom: Ada\nTo: Bo\nSubject: s\n\nbody\n",
			wantHeader: []string{"From: Ada"},
			wantBody:   "body",
			wantNotice: "a blank line stood above the header; it is skipped, and the header is read from the first Key: value line",
		},
		{
			name:       "a key in markdown bold loses its asterisks",
			text:       "From: Ada\n**To**: Bo\n**Subject**: s\n\nbody\n",
			wantHeader: []string{"To: Bo", "Subject: s"},
			wantBody:   "body",
			wantNotice: "line 2: the key \"**To**\" was in markdown bold; headers are plain `Key: value`, so it is read as \"To:\"",
		},
		{
			name: "the whole house style at once, which is what a first send is",
			text: "# Freddy: on the bus\n\nDate: Tue Sep  8 09:00:00 UTC 2026\nTo: Bo\n\nHello, all.\n",
			as:   "Ada",
			wantHeader: []string{
				"From: Ada", "To: Bo",
				"Date: Wed Sep  9 12:34:56 UTC 2026",
				"Subject: Freddy: on the bus",
			},
			wantBody:   "Hello, all.",
			wantNotice: `this draft had no From line; --as says you are "Ada", so send wrote "From: Ada"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := loadBus(t, writeBus(t, fixture()))
			p, err := PrepareDraft(tab, tc.text, at("2026-09-09T12:34:56Z"), "", tc.as)
			if err != nil {
				t.Fatalf("the draft was refused: %v", err)
			}
			stored := p.Note.Render()
			for _, line := range tc.wantHeader {
				if !strings.Contains(stored, line+"\n") {
					t.Fatalf("the stored note has no %q line:\n%s", line, stored)
				}
			}
			body := stored[strings.Index(stored, "\n\n")+2:]
			if strings.TrimRight(body, "\n") != tc.wantBody {
				t.Fatalf("body = %q, want %q", strings.TrimRight(body, "\n"), tc.wantBody)
			}
			if strings.Contains(body, "# ") {
				t.Fatalf("the heading is still in the body:\n%s", body)
			}
			notices := strings.Join(p.Notices, "\n")
			if !strings.Contains(notices, tc.wantNotice) {
				t.Fatalf("no notice said %q; the run said:\n%s", tc.wantNotice, notices)
			}
			if tc.wantNoNotice != "" && strings.Contains(notices, tc.wantNoNotice) {
				t.Fatalf("a notice said %q, which is not what happened:\n%s", tc.wantNoNotice, notices)
			}
		})
	}
}

// A tolerated draft is a note like any other: the reader that walks the bus reads back
// exactly what send stored, with no tolerance of its own needed.
func TestAToleratedNoteParsesStrictly(t *testing.T) {
	tab := loadBus(t, writeBus(t, fixture()))
	p, err := PrepareDraft(tab, "# The subject\n\nDate: whenever\n**To**: Bo\n\nbody\n", at("2026-09-09T12:34:56Z"), "", "Ada")
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	n, err := ParseNote(p.Path, p.Note.Render())
	if err != nil {
		t.Fatalf("the note send stored will not parse: %v\n%s", err, p.Note.Render())
	}
	if n.Header.Subject != "The subject" || n.Header.From != "Ada" || n.Header.To != "Bo" {
		t.Fatalf("read back as From=%q To=%q Subject=%q", n.Header.From, n.Header.To, n.Header.Subject)
	}
	if n.Header.Date == "whenever" {
		t.Fatal("the author's Date line survived; send writes the date")
	}
}

// The refusals that stay, one per thing this tool cannot work out without guessing.
func TestSendStillRefusesWhatItCannotGuess(t *testing.T) {
	cases := []struct{ name, text, as, want string }{
		{"a recipient the roster does not know", "From: Ada\nTo: Boe\nSubject: s\n\nbody\n", "", `"Boe" names no one on this bus`},
		{"no To line at all", "From: Ada\nSubject: s\n\nbody\n", "", "no To line"},
		{"an unknown key that is not a bold key", "From: Ada\nTo: Bo\nBranch: main\nSubject: s\n\nbody\n", "", `unknown header key "Branch"`},
		{"a bold key that is still unknown once unbolded", "From: Ada\nTo: Bo\n**Branch**: main\nSubject: s\n\nbody\n", "", `unknown header key "Branch"`},
		{"a Re naming nothing", "From: Ada\nTo: Bo\nRe: bo-deadbeefcafe\nSubject: s\n\nbody\n", "", "a slug is not a thread"},
		{"no From line and no --as", "To: Bo\nSubject: s\n\nbody\n", "", "no From line: write one, or pass --as <name>"},
		{"an --as that names somebody else than the draft does", "From: Ada\nTo: Bo\nSubject: s\n\nbody\n", "Bo", "send does not send one line's note as another"},
		{"an Id the author wrote", "From: Ada\nTo: Bo\nId: ada-000000000000\nSubject: s\n\nbody\n", "", "already carries an Id line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := loadBus(t, writeBus(t, fixture()))
			_, err := PrepareDraft(tab, tc.text, at("2026-09-09T12:34:56Z"), "", tc.as)
			if err == nil {
				t.Fatal("want a refusal, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

// Every problem in one run, not the first. The failure this closes is a person running
// the tool three times to be told three things it knew the first time.
func TestARefusalReportsEveryProblemInTheDraft(t *testing.T) {
	tab := loadBus(t, writeBus(t, fixture()))
	_, err := PrepareDraft(tab, "From: Ada\nTo: Boe\nRe: bo-deadbeefcafe\nSubject:\n\n\n", at("2026-09-09T12:34:56Z"), "", "")
	if err == nil {
		t.Fatal("want a refusal, got none")
	}
	reasons := Reasons(err)
	if len(reasons) != 4 {
		t.Fatalf("the run reported %d problems, want 4:\n%v", len(reasons), reasons)
	}
	for _, want := range []string{`"Boe" names no one`, "no Subject line", "the note has no body", "a slug is not a thread"} {
		found := false
		for _, r := range reasons {
			found = found || strings.Contains(r.Error(), want)
		}
		if !found {
			t.Fatalf("no reason named %q:\n%v", want, reasons)
		}
	}
	// One error carrying many is still one error to anything that only prints it.
	if !strings.Contains(err.Error(), "no Subject line") {
		t.Fatalf("Error() drops a reason: %q", err)
	}
}

// A refusal about a header line names the line of the FILE THE PERSON WROTE, not the line
// of what was left after the tolerances dropped a Date line and two blanks.
func TestALineNumberInARefusalIsTheWritersOwnLine(t *testing.T) {
	tab := loadBus(t, writeBus(t, fixture()))
	//        1        2   3          4              5             6            7
	text := "\n\n# Title\nFrom: Ada\nDate: whenever\nBranch: main\nTo: Bo\n\nbody\n"
	_, err := PrepareDraft(tab, text, at("2026-09-09T12:34:56Z"), "", "")
	if err == nil {
		t.Fatal("want a refusal, got none")
	}
	if !strings.Contains(err.Error(), "line 6: unknown header key") {
		t.Fatalf("the refusal names the wrong line: %q", err)
	}
}

// The skeleton the draft verb prints is a draft this tool sends: it parses, and it goes
// through Prepare once a body is written into it.
func TestTheSkeletonIsADraftThisToolSends(t *testing.T) {
	s := Skeleton{From: "Ada", To: "Bo", Cc: "Dana", Re: []string{"bo-abcdef012345"}, Subject: "The gate"}.Render()
	n, err := ParseNote("", s)
	if err != nil {
		t.Fatalf("the skeleton does not parse: %v\n%s", err, s)
	}
	if n.Header.From != "Ada" || n.Header.To != "Bo" || n.Header.Cc != "Dana" || n.Header.Subject != "The gate" {
		t.Fatalf("read back wrong: %+v", n.Header)
	}
	if len(n.Header.Re) != 1 || n.Header.Re[0] != "bo-abcdef012345" {
		t.Fatalf("Re read back as %v", n.Header.Re)
	}
	if n.Header.Date != "" || n.Header.ID != "" {
		t.Fatal("the skeleton carries a Date or an Id; those are the tool's to write")
	}
	if strings.TrimSpace(n.Body) != PlaceholderBody {
		t.Fatalf("body = %q, want the placeholder", n.Body)
	}
	tab := loadBus(t, writeBus(t, fixture()))
	if _, err := Prepare(tab, s, at("2026-09-09T12:34:56Z"), ""); err != nil {
		t.Fatalf("the skeleton was refused by send: %v", err)
	}
	// With no subject given, the placeholder is what stands there, and it is visibly a
	// placeholder rather than a plausible subject somebody would send by accident.
	bare := Skeleton{From: "Ada", To: "Bo"}.Render()
	if !strings.Contains(bare, "Subject: "+PlaceholderSubject) {
		t.Fatalf("a skeleton with no subject:\n%s", bare)
	}
}

// A header value is one line, whatever a caller passes --subject.
func TestOneLineRefusesAValueThatWouldForgeAHeaderLine(t *testing.T) {
	for _, bad := range []string{"a\nTo: somebody", "a\u2028b", "a\rb"} {
		if err := OneLine("--subject", bad); err == nil {
			t.Fatalf("%q was accepted as a header value", bad)
		}
	}
	if err := OneLine("--subject", "a normal subject, with punctuation: and a tab\there"); err != nil {
		t.Fatalf("a one-line subject was refused: %v", err)
	}
}
