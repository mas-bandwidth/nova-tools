package main

// The read half of docs/SPEC-BUS-REPLY.md, draft 8: one opt-in flag, two limits, one
// counted frame, one receipt line, one continuation input and one cursor rule.
//
// Every test here is named by that document's `The tests, by name` section and asserts the
// `expected=` observable it carries, verbatim. The fixtures are built to the byte counts
// those strings quote -- a body is 204 bytes where the document says `bytes=612` for three
// of them -- so the assertion is the document's sentence and not a number this file chose.
//
// The continuation half of the same section is in continuation_test.go.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- fixtures and readers

// settled is the package's two-note bus, read once with --advance, so that every commit
// added after it is NEW against a real cursor rather than first-run adoption history.
func settledBus(t *testing.T) string {
	t.Helper()
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	return checkout
}

// busFile is one file and its whole content, for a fixture that lands several in one commit.
type busFile struct{ path, content string }

// noteFrom is one note from Bo to Ada with an exact body. `Kind: note` is explicit because
// the receipt heuristic reclassifies a short body that happens to hold an acknowledgement
// word, and a fixture whose byte count is the assertion may not depend on its prose.
func noteFrom(id, subject, body string) string {
	return "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: " + id +
		"\nSubject: " + subject + "\nKind: note\n\n" + body
}

// legacyNoteFrom is a note written before ids existed: no Id line at all.
func legacyNoteFrom(subject, body string) string {
	return "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: " + subject +
		"\nKind: note\n\n" + body
}

// commitFiles writes every file and lands them in ONE commit, which is what makes a
// two-notes-in-one-commit fixture a fixture rather than two commits in a row.
func commitFiles(t *testing.T, checkout, message string, files ...busFile) {
	t.Helper()
	paths := make([]string, 0, len(files))
	for _, f := range files {
		writeFile(t, checkout, f.path, f.content)
		paths = append(paths, f.path)
	}
	gitIn(t, checkout, append([]string{"add", "--"}, paths...)...)
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", message)
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

// fill is exactly n bytes of body, ending in a newline, holding no question mark and no
// acknowledgement word.
func fill(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + (i % 26))
		if i%64 == 63 {
			b[i] = '\n'
		}
	}
	b[n-1] = '\n'
	return string(b)
}

// frame is one delivered body, as the spec's reader recovers it.
type frame struct {
	id   string
	body string
}

// readFrames is the reader docs/SPEC-BUS-REPLY.md's R4 describes, written out rather than
// summarised: OUTSIDE a frame it reads event lines; on `INBOX BODY` it consumes exactly
// `bytes=<n>` bytes; it then consumes one separator byte IF AND ONLY IF n is 0 or the last
// byte consumed was not a newline, and asserts that byte is `\n`; then it asserts the next
// line is the closing line for the same id. It never searches for the closing line, so a
// body holding one ends nothing.
func readFrames(t *testing.T, out string) []frame {
	t.Helper()
	var frames []frame
	rest := out
	for len(rest) > 0 {
		line, tail, ok := strings.Cut(rest, "\n")
		if !ok {
			break
		}
		rest = tail
		if !strings.HasPrefix(line, "INBOX BODY id=") {
			continue
		}
		var id string
		var n int
		if _, err := fmt.Sscanf(line, "INBOX BODY id=%s bytes=%d", &id, &n); err != nil {
			t.Fatalf("opening line %q does not carry an id and a byte count: %v", line, err)
		}
		if n < 0 || n > len(rest) {
			t.Fatalf("frame for %s claims %d bytes and only %d remain", id, n, len(rest))
		}
		body := rest[:n]
		rest = rest[n:]
		if n == 0 || body[n-1] != '\n' {
			if len(rest) == 0 || rest[0] != '\n' {
				t.Fatalf("frame for %s did not supply its one separator newline", id)
			}
			rest = rest[1:]
		}
		closing, tail, ok := strings.Cut(rest, "\n")
		if !ok {
			t.Fatalf("frame for %s has no closing line", id)
		}
		rest = tail
		if closing != "INBOX BODY END id="+id {
			t.Fatalf("frame for %s ended with %q, which is a defect in the writer", id, closing)
		}
		frames = append(frames, frame{id: id, body: body})
	}
	return frames
}

// testToken is the one continuation schema, decoded. Tokens are asserted by decoding this
// schema and never by inventing abbreviated commit strings.
type tokenItem struct {
	Commit string `json:"c"`
	Path   string `json:"p"`
	Offset int    `json:"o"`
}

type testToken struct {
	Version  int        `json:"v"`
	Base     string     `json:"b"`
	Head     string     `json:"h"`
	Reader   string     `json:"r"`
	Selector string     `json:"s"`
	Last     *tokenItem `json:"l"`
	Gap      *tokenItem `json:"g"`
	Gaps     int        `json:"n"`
	Frontier string     `json:"f"`
	Expected string     `json:"e"`
}

func decodeToken(t *testing.T, token string) testToken {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("continuation is not URL-safe base64: %v", err)
	}
	if len(raw) > 8<<10 {
		t.Fatalf("continuation is %d bytes, over the 8 KiB bound", len(raw))
	}
	var out testToken
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("continuation is not the one schema: %v", err)
	}
	if out.Version != 1 {
		t.Fatalf("continuation version is %d, want 1", out.Version)
	}
	return out
}

// retoken re-encodes a decoded token's raw JSON after one textual substitution, which is
// how the validation test produces a token that is wrong in exactly one way.
func retoken(t *testing.T, token, old, new string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), old) {
		t.Fatalf("token does not carry %q: %s", old, raw)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), old, new, 1)))
}

// ------------------------------------------------------------------ the frame and the bounds

// TestBodiesWithinBudgetPrintsEveryNewNoteAndSaysComplete: three bodies, one per commit,
// inside both limits.
func TestBodiesWithinBudgetPrintsEveryNewNoteAndSaysComplete(t *testing.T) {
	checkout := settledBus(t)
	bodies := []string{fill(204), fill(204), fill(204)}
	for i, body := range bodies {
		commitFiles(t, checkout, fmt.Sprintf("body %d", i+1),
			busFile{fmt.Sprintf("from-bo/w%d.md", i+1), noteFrom(fmt.Sprintf("bo-w%012d", i+1), fmt.Sprintf("w%d", i+1), body)})
	}
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies").mustCode(t, 0)

	frames := readFrames(t, r.stdout)
	if len(frames) != 3 {
		t.Fatalf("want three frames, got %d:\n%s", len(frames), r.stdout)
	}
	for i, f := range frames {
		if f.body != bodies[i] {
			t.Fatalf("frame %d is not the body byte for byte", i+1)
		}
		note := "INBOX NOTE id=" + f.id
		body := "INBOX BODY id=" + f.id + fmt.Sprintf(" bytes=%d\n", len(bodies[i]))
		if !strings.Contains(r.stdout, note) || !strings.Contains(r.stdout, body) {
			t.Fatalf("summary line and its frame did not print together for %s:\n%s", f.id, r.stdout)
		}
		if strings.Index(r.stdout, note) > strings.Index(r.stdout, body) {
			t.Fatalf("the frame for %s printed before its INBOX NOTE line", f.id)
		}
	}
	const expected = "INBOX BODIES printed=3 bytes=612 oversize=0 gaps=0 drained=true complete=true next=-"
	if !strings.Contains(r.stdout, expected+"\n") {
		t.Fatalf("receipt is not %q:\n%s", expected, r.stdout)
	}
	if strings.Count(r.stdout, "INBOX BODIES ") != 1 {
		t.Fatalf("a return carries exactly one receipt:\n%s", r.stdout)
	}
}

// TestBodiesOverBudgetStopPrintingWholeNotesAndSayCompleteFalse: exercise both limits and
// the invalid zero and over-ceiling values; no partial frame anywhere.
func TestBodiesOverBudgetStopPrintingWholeNotesAndSayCompleteFalse(t *testing.T) {
	checkout := settledBus(t)
	for i := 1; i <= 3; i++ {
		commitFiles(t, checkout, fmt.Sprintf("over %d", i),
			busFile{fmt.Sprintf("from-bo/o%d.md", i), noteFrom(fmt.Sprintf("bo-o%012d", i), fmt.Sprintf("o%d", i), fill(204))})
	}
	const partial = "INBOX BODIES printed=2 bytes=408 oversize=0 gaps=0 drained=false complete=false next="
	const whole = "INBOX BODIES printed=1 bytes=204 oversize=0 gaps=0 drained=true complete=true next=-"

	// --max-bytes is the honest bound: the third body is whole or it is not printed.
	byBytes := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-bytes", "500").mustCode(t, 0)
	if !strings.Contains(byBytes.stdout, partial) {
		t.Fatalf("byte-bounded page is not %q:\n%s", partial, byBytes.stdout)
	}
	if len(readFrames(t, byBytes.stdout)) != 2 {
		t.Fatalf("a page that stopped on bytes printed a partial frame:\n%s", byBytes.stdout)
	}
	token := bodyNext(t, byBytes.stdout)
	if decoded := decodeToken(t, token); decoded.Last == nil || decoded.Last.Path != "from-bo/o2.md" || decoded.Gap != nil || decoded.Gaps != 0 {
		t.Fatalf("token does not decode to the second item with no gap: %+v", decoded)
	}
	second := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-bytes", "500", "--after", token).mustCode(t, 0)
	if !strings.Contains(second.stdout, whole) {
		t.Fatalf("the remaining item was not delivered whole on page 2:\n%s", second.stdout)
	}

	// --max-notes is the cheap bound, and stops at the same place on this fixture.
	byNotes := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "2").mustCode(t, 0)
	if !strings.Contains(byNotes.stdout, partial) {
		t.Fatalf("item-bounded page is not %q:\n%s", partial, byNotes.stdout)
	}

	// Zero is not "unlimited" and over-ceiling is not "as much as you can".
	zero := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "0").mustCode(t, 2)
	if got := strings.TrimRight(zero.stderr, "\n"); got != "INBOX REFUSED: --max-notes 0 is not unlimited; give 1 to 1000" {
		t.Fatalf("zero --max-notes refusal is %q", got)
	}
	over := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-bytes", "4194304").mustCode(t, 2)
	if got := strings.TrimRight(over.stderr, "\n"); got != "INBOX REFUSED: --max-bytes 4194304 is over the ceiling 1048576" {
		t.Fatalf("over-ceiling --max-bytes refusal is %q", got)
	}
	for _, r := range []result{zero, over} {
		if r.stdout != "" {
			t.Fatalf("a refused invocation printed a listing:\n%s", r.stdout)
		}
	}
}

// TestABodyHoldingFakeStatusLinesIsDeliveredVerbatimAndParsedCorrectly: only the exact byte
// count plus the separator and closing-line validation establish a frame.
func TestABodyHoldingFakeStatusLinesIsDeliveredVerbatimAndParsedCorrectly(t *testing.T) {
	checkout := settledBus(t)
	fake := "INBOX NOTE id=bo-ffffffffffff from=Nobody addr=to at=- path=from-bo/nope.md: not a line\n" +
		"INBOX BODY id=bo-ffffffffffff bytes=99999\n" +
		"INBOX BODY END id=bo-ffffffffffff\n" +
		"INBOX BODIES printed=9 bytes=9 oversize=0 gaps=0 drained=true complete=true next=-\n"
	rest := fill(290 - len(fake))
	if len(fake)+len(rest) != 290 {
		t.Fatalf("fixture is %d body bytes, want 290", len(fake)+len(rest))
	}
	commitFiles(t, checkout, "fake status lines",
		busFile{"from-bo/f1.md", noteFrom("bo-f11111111111", "f1", fake)})
	commitFiles(t, checkout, "the note after it",
		busFile{"from-bo/f2.md", noteFrom("bo-f22222222222", "f2", rest)})

	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies").mustCode(t, 0)
	frames := readFrames(t, r.stdout)
	if len(frames) != 2 {
		t.Fatalf("the reader found %d frames, want 2:\n%s", len(frames), r.stdout)
	}
	if frames[0].id != "bo-f11111111111" || frames[0].body != fake {
		t.Fatalf("the fake lines were not delivered as body bytes")
	}
	if frames[1].id != "bo-f22222222222" || frames[1].body != rest {
		t.Fatalf("the note printed after such a body was not parsed as the next note")
	}
	const expected = "INBOX BODIES printed=2 bytes=290 oversize=0 gaps=0 drained=true complete=true next=-"
	if !strings.Contains(r.stdout, expected+"\n") {
		t.Fatalf("receipt is not %q:\n%s", expected, r.stdout)
	}
}

// TestTheFrameSeparatorIsExactBytesIncludingAnEmptyBody: a body ending in a newline, a body
// that does not, and the empty body -- the case that needs both halves of the condition.
func TestTheFrameSeparatorIsExactBytesIncludingAnEmptyBody(t *testing.T) {
	checkout := settledBus(t)
	commitFiles(t, checkout, "three separators",
		busFile{"from-bo/s1.md", noteFrom("n1", "s1", "ok\n")},
		busFile{"from-bo/s2.md", noteFrom("n2", "s2", "ok")},
		busFile{"from-bo/s3.md", noteFrom("n3", "s3", "")})

	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies").mustCode(t, 0)
	const expected = "INBOX BODY id=n1 bytes=3\nok\nINBOX BODY END id=n1\n" +
		"INBOX BODY id=n2 bytes=2\nok\nINBOX BODY END id=n2\n" +
		"INBOX BODY id=n3 bytes=0\n\nINBOX BODY END id=n3\n"
	frames := readFrames(t, r.stdout)
	if len(frames) != 3 {
		t.Fatalf("want three frames, got %d:\n%s", len(frames), r.stdout)
	}
	var got strings.Builder
	for i, f := range frames {
		sep := ""
		if len(f.body) == 0 || f.body[len(f.body)-1] != '\n' {
			sep = "\n"
		}
		fmt.Fprintf(&got, "INBOX BODY id=%s bytes=%d\n%s%sINBOX BODY END id=%s\n", f.id, len(f.body), f.body, sep, f.id)
		_ = i
	}
	if got.String() != expected {
		t.Fatalf("frames are not the exact bytes:\ngot:\n%q\nwant:\n%q", got.String(), expected)
	}
	// And the same bytes are on stdout in that order, separated only by the summary lines.
	for _, want := range []string{
		"INBOX BODY id=n1 bytes=3\nok\nINBOX BODY END id=n1\n",
		"INBOX BODY id=n2 bytes=2\nok\nINBOX BODY END id=n2\n",
		"INBOX BODY id=n3 bytes=0\n\nINBOX BODY END id=n3\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("stdout does not carry %q:\n%s", want, r.stdout)
		}
	}
	const receipt = "INBOX BODIES printed=3 bytes=5 oversize=0 gaps=0 drained=true complete=true next=-"
	if !strings.Contains(r.stdout, receipt+"\n") {
		t.Fatalf("the receipt counts body bytes only; want %q:\n%s", receipt, r.stdout)
	}
}

// TestBodiesModeCapsTheNewSummaryLinesToo: R3 -- the cap is the NEW half, not the bodies
// alone, and the same fixture without the flag prints every line it prints today.
func TestBodiesModeCapsTheNewSummaryLinesToo(t *testing.T) {
	checkout := settledBus(t)
	files := make([]busFile, 0, 50)
	for i := 1; i <= 50; i++ {
		files = append(files, busFile{
			fmt.Sprintf("from-bo/c%02d.md", i),
			noteFrom(fmt.Sprintf("bo-c%011d", i), fmt.Sprintf("c%02d", i), fill(204)),
		})
	}
	commitFiles(t, checkout, "fifty eligible items", files...)

	capped := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "5").mustCode(t, 0)
	const expected = "INBOX BODIES printed=5 bytes=1020 oversize=0 gaps=0 drained=false complete=false next="
	if !strings.Contains(capped.stdout, expected) {
		t.Fatalf("capped receipt is not %q:\n%s", expected, capped.stdout)
	}
	if n := strings.Count(capped.stdout, "INBOX NOTE id="); n != 5 {
		t.Fatalf("bodies mode printed %d NEW summary lines under --max-notes 5, want 5", n)
	}
	for _, token := range []string{"INBOX HEARD id=", "INBOX RECEIPT id=", "INBOX BODY OVERSIZE"} {
		if strings.Contains(capped.stdout, token) {
			t.Fatalf("this fixture has no %s line, and one printed:\n%s", token, capped.stdout)
		}
	}
	if n := len(readFrames(t, capped.stdout)); n != 5 {
		t.Fatalf("capped page carried %d frames, want 5", n)
	}

	// The same fixture without --bodies prints all fifty lines and no INBOX BODIES line.
	plain := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").mustCode(t, 0)
	if n := strings.Count(plain.stdout, "INBOX NOTE id="); n != 50 {
		t.Fatalf("without --bodies the NEW half is unbounded; %d lines printed, want 50", n)
	}
	if strings.Contains(plain.stdout, "INBOX BODIES") || strings.Contains(plain.stdout, "INBOX BODY") {
		t.Fatalf("a run without --bodies printed a bodies line:\n%s", plain.stdout)
	}

	// Mixed pages: a heard item and a receipt-only item count against the item cap as
	// summaries, and neither opens a frame.
	mixed := settledBus(t)
	commitFiles(t, mixed, "a receipt-shaped note and a note",
		busFile{"from-bo/m1.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: bo-m11111111111\nSubject: m1\nKind: receipt\n\nreceived"},
		busFile{"from-bo/m2.md", noteFrom("bo-m22222222222", "m2", fill(204))})
	commitFiles(t, mixed, "heard",
		busFile{"from-bo/m3.md", noteFrom("bo-m33333333333", "m3", fill(204))},
		busFile{"from-ada/RECEIPTS", "2026-09-09T12:34:56Z bo-m33333333333\n"})
	page := invoke(t, "", "inbox", "--bus", mixed, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "2").mustCode(t, 0)
	if !strings.Contains(page.stdout, "INBOX RECEIPT id=bo-m11111111111") {
		t.Fatalf("the receipt-only item lost its summary line:\n%s", page.stdout)
	}
	if strings.Contains(page.stdout, "INBOX BODY id=bo-m11111111111") {
		t.Fatalf("a receipt-only item opened a frame:\n%s", page.stdout)
	}
	if !strings.Contains(page.stdout, "INBOX BODIES printed=1 bytes=204 oversize=0 gaps=0 drained=false complete=false next=") {
		t.Fatalf("a summary-only item did not count against the item cap:\n%s", page.stdout)
	}
	if strings.Contains(page.stdout, "id=bo-m33333333333") {
		t.Fatalf("the third item printed past --max-notes 2:\n%s", page.stdout)
	}
}

// ------------------------------------------------------------- the released tool, untouched

// todayGolden compares one return with the recorded output of today's binary, byte for
// byte, after replacing the two values a run cannot repeat -- the temporary bus path and a
// wait's measured elapsed time. Record with NOVA_BUS_RECORD_TODAY=1 against the tip this
// branch was cut from; every file under testdata/today was recorded there.
func todayGolden(t *testing.T, name, checkout string, r result) {
	t.Helper()
	normal := func(s string) string {
		s = strings.ReplaceAll(s, checkout, "<bus>")
		var b strings.Builder
		for {
			i := strings.Index(s, " after=")
			if i < 0 {
				b.WriteString(s)
				return b.String()
			}
			b.WriteString(s[:i])
			b.WriteString(" after=<elapsed>")
			rest := s[i+len(" after="):]
			j := strings.IndexAny(rest, " \n")
			if j < 0 {
				return b.String()
			}
			s = rest[j:]
		}
	}
	got := fmt.Sprintf("exit=%d\n--- stdout\n%s--- stderr\n%s", r.code, normal(r.stdout), normal(r.stderr))
	path := filepath.Join("testdata", "today", name)
	if os.Getenv("NOVA_BUS_RECORD_TODAY") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s is not byte-identical to today's binary:\ngot:\n%s\nwant:\n%s", name, got, want)
	}
	if strings.Contains(got, "INBOX BODIES") || strings.Contains(got, "INBOX BODY") {
		t.Fatalf("%s carries a bodies line and the flag was absent:\n%s", name, got)
	}
}

// TestInboxAndWaitWithoutBodiesAreByteIdenticalToTodays: the existing fixtures over both
// verbs with the flag absent -- stdout, stderr and exit code unchanged, including at 600
// carried items and with --open, --open-max and --full.
func TestInboxAndWaitWithoutBodiesAreByteIdenticalToTodays(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	base := []string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40"}
	todayGolden(t, "inbox-full.txt", checkout, invoke(t, "", append(append([]string{}, base...), "--full")...))
	todayGolden(t, "inbox-full-open.txt", checkout, invoke(t, "", append(append([]string{}, base...), "--full", "--open")...))
	todayGolden(t, "inbox-full-open-max-1.txt", checkout, invoke(t, "", append(append([]string{}, base...), "--full", "--open", "--open-max", "1")...))
	todayGolden(t, "wait.txt", checkout, invoke(t, "", "wait", "--bus", checkout, "--as", "Ada",
		"--receipt-max-words", "40", "--timeout", "2s", "--interval", "1s", "--remote", "origin", "--branch", "main"))

	// The state where an unbounded listing shows: 600 carried items, in one commit.
	big, _ := busDir(t)
	files := make([]busFile, 0, 600)
	for i := 0; i < 600; i++ {
		files = append(files, busFile{
			fmt.Sprintf("from-bo/big-%03d.md", i),
			noteFrom(fmt.Sprintf("bo-b%011d", i), fmt.Sprintf("big %03d", i), "a carried note, one of six hundred\n"),
		})
	}
	commitFiles(t, big, "six hundred", files...)
	bigBase := []string{"inbox", "--bus", big, "--as", "Ada", "--receipt-max-words", "40"}
	todayGolden(t, "inbox-600-full-open.txt", big, invoke(t, "", append(append([]string{}, bigBase...), "--full", "--open")...))
	todayGolden(t, "inbox-600-full-open-max-1000.txt", big, invoke(t, "", append(append([]string{}, bigBase...), "--full", "--open", "--open-max", "1000")...))
	todayGolden(t, "inbox-600-full.txt", big, invoke(t, "", append(append([]string{}, bigBase...), "--full")...))
}
