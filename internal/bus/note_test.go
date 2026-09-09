package bus

import (
	"strings"
	"testing"
)

const goodNote = `From: Ada (day shift, the west host, the shared account)
To: Bo
Cc: Dana
Date: Mon Sep  7 00:02:12 UTC 2026
Id: ada-0123456789ab
Re: bo-fedcba987654
Re: from-bo/2026-09-06-packet-void-pr624.md
Subject: Cold read of #624

Bo,

The body.
`

func TestParseNoteReadsTheHeaderAndStopsAtTheBlankLine(t *testing.T) {
	n, err := ParseNote("from-ada/x.md", goodNote)
	if err != nil {
		t.Fatal(err)
	}
	h := n.Header
	if h.From != "Ada (day shift, the west host, the shared account)" {
		t.Fatalf("From = %q", h.From)
	}
	if h.To != "Bo" || h.Cc != "Dana" {
		t.Fatalf("To = %q Cc = %q", h.To, h.Cc)
	}
	if h.ID != "ada-0123456789ab" {
		t.Fatalf("Id = %q", h.ID)
	}
	if len(h.Re) != 2 || h.Re[0] != "bo-fedcba987654" {
		t.Fatalf("Re = %v; both an id and a legacy path are Re lines", h.Re)
	}
	if h.Subject != "Cold read of #624" {
		t.Fatalf("Subject = %q", h.Subject)
	}
	if !strings.HasPrefix(n.Body, "Bo,") {
		t.Fatalf("Body = %q; the body is everything after the first blank line", n.Body)
	}
	if strings.Contains(n.Body, "Subject:") {
		t.Fatal("a header line leaked into the body")
	}
	if n.Lane != "from-ada" {
		t.Fatalf("Lane = %q", n.Lane)
	}
	if got := h.LineOf(KeySubject); got != 8 {
		t.Fatalf("LineOf(Subject) = %d, want 8", got)
	}
}

func TestParseNoteRefuses(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"a line that is not Key: value", "From: Ada\nthis is prose\n\nbody\n", "not a header line"},
		{"a leading blank line", "\nFrom: Ada\n\nbody\n", "no header"},
		{"a misspelled key", "From: Ada\nSbuject: hi\n\nbody\n", `unknown header key "Sbuject"`},
		{"two Subject lines", "From: R\nSubject: a\nSubject: b\n\nbody\n", "a second Subject line"},
		{"two From lines", "From: R\nFrom: S\nSubject: a\n\nbody\n", "a second From line"},
		{"an empty Re line", "From: R\nRe:\nSubject: a\n\nbody\n", "empty Re line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNote("from-ada/x.md", tc.text)
			if err == nil {
				t.Fatal("want a refusal, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

// CRLF and a byte order mark are what an editor on another platform adds. Neither is a
// parse failure and neither may change an id.
func TestParseNoteToleratesCRLFAndABOM(t *testing.T) {
	crlf := "\ufeff" + strings.ReplaceAll(goodNote, "\n", "\r\n")
	a, err := ParseNote("from-ada/x.md", goodNote)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseNote("from-ada/x.md", crlf)
	if err != nil {
		t.Fatal(err)
	}
	if a.Header.Subject != b.Header.Subject {
		t.Fatalf("subjects differ: %q vs %q", a.Header.Subject, b.Header.Subject)
	}
	if NormalizeBody(a.Body) != NormalizeBody(b.Body) {
		t.Fatalf("bodies differ after normalization:\n%q\n%q", a.Body, b.Body)
	}
}

func TestHeaderValidate(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	ok := func(text string) Header {
		t.Helper()
		n, err := ParseNote("from-ada/x.md", text)
		if err != nil {
			t.Fatal(err)
		}
		return n.Header
	}
	if err := ok(goodNote).Validate(c); err != nil {
		t.Fatalf("a good header was refused: %v", err)
	}
	cases := []struct {
		name, text, want string
	}{
		{"no From", "To: Bo\nSubject: a\n\nbody\n", "no From line"},
		{"an unknown From", "From: Nobody\nTo: Bo\nSubject: a\n\nbody\n", "names no one at this table"},
		{"no To", "From: Ada\nSubject: a\n\nbody\n", "no To line"},
		{"an unknown To", "From: Ada\nTo: Boe\nSubject: a\n\nbody\n", `"Boe"`},
		{"an unknown Cc", "From: Ada\nTo: Bo\nCc: Glen\nSubject: a\n\nbody\n", `"Glen"`},
		{"an empty Subject", "From: Ada\nTo: Bo\nSubject:   \n\nbody\n", "no Subject line, or an empty one"},
		{"a bad Kind", "From: Ada\nTo: Bo\nKind: maybe\nSubject: a\n\nbody\n", "is neither"},
		{"a bad Id", "From: Ada\nTo: Bo\nId: nope\nSubject: a\n\nbody\n", "is not <sender>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ok(tc.text).Validate(c)
			if err == nil {
				t.Fatal("want a refusal, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestValidID(t *testing.T) {
	for _, good := range []string{"ada-0123456789ab", "self-talk-ffffffffffff", "a-000000000000"} {
		if err := ValidID(good); err != nil {
			t.Fatalf("ValidID(%q) = %v, want nil", good, err)
		}
	}
	for _, bad := range []string{"", "ada", "ada-0123456789", "ada-0123456789AB", "ada-0123456789zz", "ada0123456789ab", "Ada-0123456789ab", "-0123456789ab"} {
		if err := ValidID(bad); err == nil {
			t.Fatalf("ValidID(%q) = nil, want a refusal", bad)
		}
	}
	if got := SlugOfID("ada-0123456789ab"); got != "ada" {
		t.Fatalf("SlugOfID = %q, want ada", got)
	}
}

// The id's collision-proofness, stated as the three properties it rests on.
func TestIDIsDeterministicNamespacedAndSensitiveToEveryFieldThatMakesANoteDifferent(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	ada := mustParticipant(t, c, "Ada")
	bo := mustParticipant(t, c, "Bo")
	base := Header{From: "Ada", To: "Bo", Subject: "A finding"}
	date := "Mon Sep  7 00:02:12 UTC 2026"
	body := "The body.\n"

	id, err := AssignID(c, ada, base, body, date)
	if err != nil {
		t.Fatal(err)
	}
	again, err := AssignID(c, ada, base, body, date)
	if err != nil {
		t.Fatal(err)
	}
	if id != again {
		t.Fatalf("the id is not deterministic: %q then %q", id, again)
	}
	if !strings.HasPrefix(id, "ada-") {
		t.Fatalf("id %q is not namespaced by the sender's lane slug", id)
	}
	if err := ValidID(id); err != nil {
		t.Fatalf("assigned id %q is not a valid id: %v", id, err)
	}

	// Same everything, a different sender: a different id, because the namespace differs.
	// This is the property that makes two lines racing unable to collide at all.
	other, err := AssignID(c, bo, Header{From: "Bo", To: "Ada", Subject: "A finding"}, body, date)
	if err != nil {
		t.Fatal(err)
	}
	if SlugOfID(other) != "bo" {
		t.Fatalf("id %q is not in Bo's namespace", other)
	}

	// Every field that makes a note a different note changes the id.
	seen := map[string]string{id: "the base note"}
	vary := []struct {
		name string
		h    Header
		body string
		date string
	}{
		{"a different second", base, body, "Mon Sep  7 00:02:13 UTC 2026"},
		{"a different subject", Header{From: "Ada", To: "Bo", Subject: "Another finding"}, body, date},
		{"a different recipient", Header{From: "Ada", To: "Dana", Subject: "A finding"}, body, date},
		{"a cc", Header{From: "Ada", To: "Bo", Cc: "Dana", Subject: "A finding"}, body, date},
		{"a Re line", Header{From: "Ada", To: "Bo", Subject: "A finding", Re: []string{"bo-000000000000"}}, body, date},
		{"a Kind line", Header{From: "Ada", To: "Bo", Subject: "A finding", Kind: KindReceipt}, body, date},
		{"a different body", base, "Another body.\n", date},
	}
	for _, v := range vary {
		got, err := AssignID(c, ada, v.h, v.body, v.date)
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := seen[got]; dup {
			t.Fatalf("%s produced the same id as %s: %q", v.name, prev, got)
		}
		seen[got] = v.name
	}
}

// The id must survive what an editor does to a file on save, or it is not an id.
func TestIDIsUnchangedByTrailingWhitespaceAndCRLF(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	ada := mustParticipant(t, c, "Ada")
	h := Header{From: "Ada", To: "Bo", Subject: "A finding"}
	date := "Mon Sep  7 00:02:12 UTC 2026"
	want, err := AssignID(c, ada, h, "one\ntwo\n", date)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"one\ntwo\n", "one\r\ntwo\r\n", "one  \ntwo\t\n", "one\ntwo\n\n\n", "one\ntwo"} {
		got, err := AssignID(c, ada, h, body, date)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("body %q changed the id: %q, want %q", body, got, want)
		}
	}
}

// The To line's SPELLING must not change the id: the recipients are resolved first, so
// "Ada Vale" and "Ada a1b2c3d4" are one recipient at the id layer as they are to
// every reader.
func TestIDUsesResolvedRecipients(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	bo := mustParticipant(t, c, "Bo")
	date := "Mon Sep  7 00:02:12 UTC 2026"
	a, err := AssignID(c, bo, Header{From: "Bo", To: "Ada Vale", Subject: "s"}, "b", date)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AssignID(c, bo, Header{From: "Bo", To: "Ada a1b2c3d4 (active line)", Subject: "s"}, "b", date)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("two spellings of one recipient gave two ids: %q and %q", a, b)
	}
}

func TestAssignIDRefusesASenderWithNoLane(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	dana := mustParticipant(t, c, "Dana")
	if _, err := AssignID(c, dana, Header{From: "Dana", To: "Ada", Subject: "s"}, "b", "d"); err == nil {
		t.Fatal("a participant with no lane was assigned an id")
	}
}

func TestFileNameCarriesTheMinuteTheSlugAndTheIDsHashHalf(t *testing.T) {
	got := FileName(at("2026-09-09T12:34:56Z"), "cold-read", "ada-0123456789ab")
	want := "2026-09-09T1234Z-cold-read-0123456789ab.md"
	if got != want {
		t.Fatalf("FileName = %q, want %q", got, want)
	}
	// The failure this closes: one sender writing twice inside one minute collided on the
	// filename and overwrote their own note.
	other := FileName(at("2026-09-09T12:34:59Z"), "cold-read", "ada-ba9876543210")
	if got == other {
		t.Fatal("two notes in one minute produced one filename")
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Cold read of #624: sound code", "cold-read-of-624-sound-code"},
		{"   ", "note"},
		{"—", "note"},
		{"Héllo wörld", "h-llo-w-rld"},
		{strings.Repeat("long ", 40), "long-long-long-long-long-long-long-long-long-long-long-long"},
	}
	for _, tc := range cases {
		if got := Slugify(tc.in, SlugMax); got != tc.want {
			t.Fatalf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The receipt heuristic, and the Kind line that overrides it in both directions.
func TestIsReceipt(t *testing.T) {
	const maxWords = 40
	cases := []struct {
		name string
		kind string
		body string
		want bool
	}{
		{"a bare acknowledgement", "", "Heard, thank you. The review is under way.", true},
		{"received", "", "Received.", true},
		{"ack", "", "Ack.", true},
		{"a question is never a receipt", "", "Heard. Should I take the second half?", false},
		{"a long note that says heard", "", "Heard. " + strings.Repeat("word ", 60), false},
		{"a finding", "", "The gate in the workflow never runs, because the matrix key is misspelled.", false},
		{"a word that merely contains one", "", "Unreceived mail is piling up.", false},
		{"Kind: receipt wins over a question", KindReceipt, "Heard. Should I take the second half?", true},
		{"Kind: note wins over the heuristic", KindNote, "Heard, thank you.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := Note{Header: Header{Kind: tc.kind}, Body: tc.body}
			if got := IsReceipt(n, maxWords); got != tc.want {
				t.Fatalf("IsReceipt = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRenderPutsTheHeaderInCanonicalOrderAndKeepsTheAuthorsWords(t *testing.T) {
	n, err := ParseNote("from-ada/x.md", goodNote)
	if err != nil {
		t.Fatal(err)
	}
	out := n.Render()
	reparsed, err := ParseNote("from-ada/x.md", out)
	if err != nil {
		t.Fatalf("a rendered note did not parse: %v\n%s", err, out)
	}
	if reparsed.Header.From != n.Header.From || reparsed.Header.To != n.Header.To || reparsed.Header.Subject != n.Header.Subject {
		t.Fatal("Render did not round-trip the author's own lines")
	}
	if len(reparsed.Header.Re) != len(n.Header.Re) {
		t.Fatalf("Re lines: %v, want %v", reparsed.Header.Re, n.Header.Re)
	}
	lines := strings.Split(out, "\n")
	wantOrder := []string{"From: ", "To: ", "Cc: ", "Date: ", "Id: ", "Re: ", "Re: ", "Subject: "}
	for i, prefix := range wantOrder {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Fatalf("line %d = %q, want it to start with %q", i+1, lines[i], prefix)
		}
	}
	if lines[len(wantOrder)] != "" {
		t.Fatalf("the header is not closed by a blank line: %q", lines[len(wantOrder)])
	}
}

// The two shapes a real table writes that a strict reader loses. Both are real: a table
// whose notes are also read in a browser grows markdown headings, and one writer's notes
// are markdown lists. Between them they were most of the files inbox used to drop in
// silence.
func TestParseNoteAcceptsAHeadingAndBulletHeaders(t *testing.T) {
	cases := []struct {
		name, text, wantFrom, wantSubject, wantBody string
		wantFromLine                                int
	}{
		{
			name:        "a markdown heading above the header",
			text:        "# Thanks for the warm welcome\n\nFrom: Bo Quill\nTo: Ada\nSubject: Thanks\n\nThank you for the welcome.\n",
			wantFrom:    "Bo Quill",
			wantSubject: "Thanks",
			wantBody:    "Thank you for the welcome.",
			// Line numbers count from the top of the FILE, not from the header.
			wantFromLine: 3,
		},
		{
			name:         "a heading with no blank line under it",
			text:         "# Thanks\nFrom: Bo\nTo: Ada\nSubject: Thanks\n\nbody\n",
			wantFrom:     "Bo",
			wantSubject:  "Thanks",
			wantBody:     "body",
			wantFromLine: 2,
		},
		{
			name:         "bullet header lines",
			text:         "- From: Bo Quill\n- To: Ada, Dana\n- Cc: Dana\n- Subject: The C pass\n\nThe pass is green.\n",
			wantFrom:     "Bo Quill",
			wantSubject:  "The C pass",
			wantBody:     "The pass is green.",
			wantFromLine: 1,
		},
		{
			name:         "both at once, which is how they actually arrive",
			text:         "# 2026-09-09: On the C pass\n\n- From: Bo Quill\n- To: Ada\n- Subject: The C pass\n\nThe pass is green.\n",
			wantFrom:     "Bo Quill",
			wantSubject:  "The C pass",
			wantBody:     "The pass is green.",
			wantFromLine: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := ParseNote("from-bo/x.md", tc.text)
			if err != nil {
				t.Fatalf("ParseNote: %v", err)
			}
			if n.Header.From != tc.wantFrom {
				t.Errorf("From = %q, want %q", n.Header.From, tc.wantFrom)
			}
			if n.Header.Subject != tc.wantSubject {
				t.Errorf("Subject = %q, want %q", n.Header.Subject, tc.wantSubject)
			}
			if got := NormalizeBody(n.Body); got != tc.wantBody {
				t.Errorf("Body = %q, want %q", got, tc.wantBody)
			}
			if got := n.Header.LineOf(KeyFrom); got != tc.wantFromLine {
				t.Errorf("the From line is reported at line %d, want %d: a refusal must name the line a person opens to", got, tc.wantFromLine)
			}
		})
	}
}

// A prose first line still fails -- there is no honest way to tell a From line from a
// sentence with a colon in it -- but the refusal must be READABLE. A paragraph up to its
// first colon is not a header key, and quoting the whole of it back would be one
// unreadable line per note in a check over a table of them.
func TestAProseFirstLineFailsWithAShortQuotedKey(t *testing.T) {
	long := "Ada, the graph checkpoint is pushed at 15649542 on PR #707 and the whole suite passed: 138,751 mutations, zero divergence"
	_, err := ParseNote("from-bo/x.md", long+"\n\nbody\n")
	if err == nil {
		t.Fatal("a note whose first line is prose parsed")
	}
	if !strings.Contains(err.Error(), "not a header key") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
	if len(err.Error()) > 200 {
		t.Fatalf("the refusal is %d characters; it pastes the paragraph back:\n%v", len(err.Error()), err)
	}
	if !strings.Contains(err.Error(), "...") {
		t.Fatalf("the refusal does not mark that it shortened the key: %v", err)
	}
	if strings.Contains(err.Error(), "138,751") {
		t.Fatalf("the whole paragraph is in the refusal: %v", err)
	}
	// A key short enough to read is quoted whole, so the common case is unchanged.
	_, err = ParseNote("from-bo/x.md", "From: Ada\nSbuject: s\n\nbody\n")
	if err == nil || !strings.Contains(err.Error(), `unknown header key "Sbuject"`) {
		t.Fatalf("a short key was not quoted whole: %v", err)
	}
}

// THE THREE SHAPES A READ OF THE REAL TABLE FOUND, and what each refusal now has to say.
// A note that will not parse is a note its reader is shown as UNREADABLE and can do
// nothing about unless the line says which of the three it is: a key in markdown bold, a
// key nobody knows, or a body sentence standing where the header goes. The failure is
// unchanged in all three -- only the sentence after it.
func TestAnUnreadableNoteSaysWhatToDoAboutIt(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{
			name: "a key in markdown bold",
			text: "From: Bo\n**To**: Ada\nSubject: s\n\nbody\n",
			want: []string{"line 2", `"**To**"`, "headers are plain `Key: value`, not markdown bold", `"To:"`},
		},
		{
			name: "a key nobody knows",
			text: "From: Bo\nTo: Ada\nBranch: main\nSubject: s\n\nbody\n",
			want: []string{"line 3", `unknown header key "Branch"`, "the keys are From, To, Cc, Date, Id, Re, Subject, Kind"},
		},
		{
			name: "a body sentence where the header goes",
			text: "From: Bo\nTo: Ada\nSubject: s\nAda, our notes crossed: yours arrived while I was writing mine.\n\nbody\n",
			want: []string{"line 4", "not a header key", "the header ends at the first blank line; put a blank line after the last header"},
		},
		{
			name: "a body sentence with no colon in it at all",
			text: "From: Bo\nTo: Ada\nSubject: s\nAda, our notes crossed again\n\nbody\n",
			want: []string{"line 4", "not a header line", "the header ends at the first blank line; put a blank line after the last header"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNote("from-bo/x.md", tc.text)
			if err == nil {
				t.Fatal("the note parsed; these four shapes are still failures")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the refusal does not say %q:\n%v", want, err)
				}
			}
		})
	}
	// And the tolerances are untouched: a bullet in front of a bold key is still read as a
	// bullet, so the refusal is about the bold and not about the bullet.
	_, err := ParseNote("from-bo/x.md", "- From: Bo\n- **To**: Ada\n- Subject: s\n\nbody\n")
	if err == nil || !strings.Contains(err.Error(), "not markdown bold") {
		t.Fatalf("a bulleted bold key: %v", err)
	}
}
