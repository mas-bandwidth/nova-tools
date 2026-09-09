package messagebus

import (
	"strings"
	"testing"
)

const goodNote = `From: Rowan (bud, the Studio, the mas account)
To: Stella
Cc: Glenn
Date: Mon Sep  7 00:02:12 UTC 2026
Id: rowan-0123456789ab
Re: stella-fedcba987654
Re: from-stella/2026-09-06-studio-packet-void-pr624.md
Subject: Cold read of #624

Stella,

The body.
`

func TestParseNoteReadsTheHeaderAndStopsAtTheBlankLine(t *testing.T) {
	n, err := ParseNote("from-rowan/x.md", goodNote)
	if err != nil {
		t.Fatal(err)
	}
	h := n.Header
	if h.From != "Rowan (bud, the Studio, the mas account)" {
		t.Fatalf("From = %q", h.From)
	}
	if h.To != "Stella" || h.Cc != "Glenn" {
		t.Fatalf("To = %q Cc = %q", h.To, h.Cc)
	}
	if h.ID != "rowan-0123456789ab" {
		t.Fatalf("Id = %q", h.ID)
	}
	if len(h.Re) != 2 || h.Re[0] != "stella-fedcba987654" {
		t.Fatalf("Re = %v; both an id and a legacy path are Re lines", h.Re)
	}
	if h.Subject != "Cold read of #624" {
		t.Fatalf("Subject = %q", h.Subject)
	}
	if !strings.HasPrefix(n.Body, "Stella,") {
		t.Fatalf("Body = %q; the body is everything after the first blank line", n.Body)
	}
	if strings.Contains(n.Body, "Subject:") {
		t.Fatal("a header line leaked into the body")
	}
	if n.Lane != "from-rowan" {
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
		{"a line that is not Key: value", "From: Rowan\nthis is prose\n\nbody\n", "not a header line"},
		{"a leading blank line", "\nFrom: Rowan\n\nbody\n", "no header"},
		{"a misspelled key", "From: Rowan\nSbuject: hi\n\nbody\n", `unknown header key "Sbuject"`},
		{"two Subject lines", "From: R\nSubject: a\nSubject: b\n\nbody\n", "a second Subject line"},
		{"two From lines", "From: R\nFrom: S\nSubject: a\n\nbody\n", "a second From line"},
		{"an empty Re line", "From: R\nRe:\nSubject: a\n\nbody\n", "empty Re line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNote("from-rowan/x.md", tc.text)
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
	a, err := ParseNote("from-rowan/x.md", goodNote)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseNote("from-rowan/x.md", crlf)
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
		n, err := ParseNote("from-rowan/x.md", text)
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
		{"no From", "To: Stella\nSubject: a\n\nbody\n", "no From line"},
		{"an unknown From", "From: Nobody\nTo: Stella\nSubject: a\n\nbody\n", "names no one at this table"},
		{"no To", "From: Rowan\nSubject: a\n\nbody\n", "no To line"},
		{"an unknown To", "From: Rowan\nTo: Stela\nSubject: a\n\nbody\n", `"Stela"`},
		{"an unknown Cc", "From: Rowan\nTo: Stella\nCc: Glen\nSubject: a\n\nbody\n", `"Glen"`},
		{"an empty Subject", "From: Rowan\nTo: Stella\nSubject:   \n\nbody\n", "no Subject line, or an empty one"},
		{"a bad Kind", "From: Rowan\nTo: Stella\nKind: maybe\nSubject: a\n\nbody\n", "is neither"},
		{"a bad Id", "From: Rowan\nTo: Stella\nId: nope\nSubject: a\n\nbody\n", "is not <sender>"},
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
	for _, good := range []string{"rowan-0123456789ab", "self-talk-ffffffffffff", "a-000000000000"} {
		if err := ValidID(good); err != nil {
			t.Fatalf("ValidID(%q) = %v, want nil", good, err)
		}
	}
	for _, bad := range []string{"", "rowan", "rowan-0123456789", "rowan-0123456789AB", "rowan-0123456789zz", "rowan0123456789ab", "Rowan-0123456789ab", "-0123456789ab"} {
		if err := ValidID(bad); err == nil {
			t.Fatalf("ValidID(%q) = nil, want a refusal", bad)
		}
	}
	if got := SlugOfID("rowan-0123456789ab"); got != "rowan" {
		t.Fatalf("SlugOfID = %q, want rowan", got)
	}
}

// The id's collision-proofness, stated as the three properties it rests on.
func TestIDIsDeterministicNamespacedAndSensitiveToEveryFieldThatMakesANoteDifferent(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	rowan := mustParticipant(t, c, "Rowan")
	stella := mustParticipant(t, c, "Stella")
	base := Header{From: "Rowan", To: "Stella", Subject: "A finding"}
	date := "Mon Sep  7 00:02:12 UTC 2026"
	body := "The body.\n"

	id, err := AssignID(c, rowan, base, body, date)
	if err != nil {
		t.Fatal(err)
	}
	again, err := AssignID(c, rowan, base, body, date)
	if err != nil {
		t.Fatal(err)
	}
	if id != again {
		t.Fatalf("the id is not deterministic: %q then %q", id, again)
	}
	if !strings.HasPrefix(id, "rowan-") {
		t.Fatalf("id %q is not namespaced by the sender's lane slug", id)
	}
	if err := ValidID(id); err != nil {
		t.Fatalf("assigned id %q is not a valid id: %v", id, err)
	}

	// Same everything, a different sender: a different id, because the namespace differs.
	// This is the property that makes two lines racing unable to collide at all.
	other, err := AssignID(c, stella, Header{From: "Stella", To: "Rowan", Subject: "A finding"}, body, date)
	if err != nil {
		t.Fatal(err)
	}
	if SlugOfID(other) != "stella" {
		t.Fatalf("id %q is not in Stella's namespace", other)
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
		{"a different subject", Header{From: "Rowan", To: "Stella", Subject: "Another finding"}, body, date},
		{"a different recipient", Header{From: "Rowan", To: "Glenn", Subject: "A finding"}, body, date},
		{"a cc", Header{From: "Rowan", To: "Stella", Cc: "Glenn", Subject: "A finding"}, body, date},
		{"a Re line", Header{From: "Rowan", To: "Stella", Subject: "A finding", Re: []string{"stella-000000000000"}}, body, date},
		{"a Kind line", Header{From: "Rowan", To: "Stella", Subject: "A finding", Kind: KindReceipt}, body, date},
		{"a different body", base, "Another body.\n", date},
	}
	for _, v := range vary {
		got, err := AssignID(c, rowan, v.h, v.body, v.date)
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
	rowan := mustParticipant(t, c, "Rowan")
	h := Header{From: "Rowan", To: "Stella", Subject: "A finding"}
	date := "Mon Sep  7 00:02:12 UTC 2026"
	want, err := AssignID(c, rowan, h, "one\ntwo\n", date)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"one\ntwo\n", "one\r\ntwo\r\n", "one  \ntwo\t\n", "one\ntwo\n\n\n", "one\ntwo"} {
		got, err := AssignID(c, rowan, h, body, date)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("body %q changed the id: %q, want %q", body, got, want)
		}
	}
}

// The To line's SPELLING must not change the id: the recipients are resolved first, so
// "Rowan Claude" and "Rowan a1b2c3d4" are one recipient at the id layer as they are to
// every reader.
func TestIDUsesResolvedRecipients(t *testing.T) {
	c, err := LoadConfig(writeTable(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	stella := mustParticipant(t, c, "Stella")
	date := "Mon Sep  7 00:02:12 UTC 2026"
	a, err := AssignID(c, stella, Header{From: "Stella", To: "Rowan Claude", Subject: "s"}, "b", date)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AssignID(c, stella, Header{From: "Stella", To: "Rowan a1b2c3d4 (active bud)", Subject: "s"}, "b", date)
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
	glenn := mustParticipant(t, c, "Glenn")
	if _, err := AssignID(c, glenn, Header{From: "Glenn", To: "Rowan", Subject: "s"}, "b", "d"); err == nil {
		t.Fatal("a participant with no lane was assigned an id")
	}
}

func TestFileNameCarriesTheMinuteTheSlugAndTheIDsHashHalf(t *testing.T) {
	got := FileName(at("2026-09-09T12:34:56Z"), "cold-read", "rowan-0123456789ab")
	want := "2026-09-09T1234Z-cold-read-0123456789ab.md"
	if got != want {
		t.Fatalf("FileName = %q, want %q", got, want)
	}
	// The failure this closes: one sender writing twice inside one minute collided on the
	// filename and overwrote their own note.
	other := FileName(at("2026-09-09T12:34:59Z"), "cold-read", "rowan-ba9876543210")
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
	n, err := ParseNote("from-rowan/x.md", goodNote)
	if err != nil {
		t.Fatal(err)
	}
	out := n.Render()
	reparsed, err := ParseNote("from-rowan/x.md", out)
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
			text:        "# Thanks for the warm welcome\n\nFrom: Stella Codex\nTo: Rowan\nSubject: Thanks\n\nThank you for the welcome.\n",
			wantFrom:    "Stella Codex",
			wantSubject: "Thanks",
			wantBody:    "Thank you for the welcome.",
			// Line numbers count from the top of the FILE, not from the header.
			wantFromLine: 3,
		},
		{
			name:         "a heading with no blank line under it",
			text:         "# Thanks\nFrom: Stella\nTo: Rowan\nSubject: Thanks\n\nbody\n",
			wantFrom:     "Stella",
			wantSubject:  "Thanks",
			wantBody:     "body",
			wantFromLine: 2,
		},
		{
			name:         "bullet header lines",
			text:         "- From: Stella Codex\n- To: Rowan, Glenn\n- Cc: Glenn\n- Subject: The C pass\n\nThe pass is green.\n",
			wantFrom:     "Stella Codex",
			wantSubject:  "The C pass",
			wantBody:     "The pass is green.",
			wantFromLine: 1,
		},
		{
			name:         "both at once, which is how they actually arrive",
			text:         "# 2026-09-09: On the C pass\n\n- From: Stella Codex\n- To: Rowan\n- Subject: The C pass\n\nThe pass is green.\n",
			wantFrom:     "Stella Codex",
			wantSubject:  "The C pass",
			wantBody:     "The pass is green.",
			wantFromLine: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := ParseNote("from-stella/x.md", tc.text)
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
	long := "Rowan, the graph checkpoint is pushed at 15649542 on PR #707 and the whole suite passed: 138,751 mutations, zero divergence"
	_, err := ParseNote("from-stella/x.md", long+"\n\nbody\n")
	if err == nil {
		t.Fatal("a note whose first line is prose parsed")
	}
	if !strings.Contains(err.Error(), "unknown header key") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
	if len(err.Error()) > 120 {
		t.Fatalf("the refusal is %d characters; it pastes the paragraph back:\n%v", len(err.Error()), err)
	}
	if !strings.Contains(err.Error(), "...") {
		t.Fatalf("the refusal does not mark that it shortened the key: %v", err)
	}
	if strings.Contains(err.Error(), "138,751") {
		t.Fatalf("the whole paragraph is in the refusal: %v", err)
	}
	// A key short enough to read is quoted whole, so the common case is unchanged.
	_, err = ParseNote("from-stella/x.md", "From: Rowan\nSbuject: s\n\nbody\n")
	if err == nil || !strings.Contains(err.Error(), `unknown header key "Sbuject"`) {
		t.Fatalf("a short key was not quoted whole: %v", err)
	}
}
