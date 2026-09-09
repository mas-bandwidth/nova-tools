package messagebus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const draft = `From: Rowan (bud, the Studio, the mas account)
To: Stella
Cc: Glenn
Subject: The gate in the workflow never runs

Stella,

The matrix key is misspelled, so the step is skipped.
`

func TestPrepareAssignsTheDateTheIDAndThePath(t *testing.T) {
	tab := loadTable(t, writeTable(t, fixture()))
	p, err := Prepare(tab, draft, at("2026-09-09T12:34:56Z"), "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Note.Header.Date != "Wed Sep  9 12:34:56 UTC 2026" {
		t.Fatalf("Date = %q; it is pasted from the clock in UTC", p.Note.Header.Date)
	}
	if err := ValidID(p.Note.Header.ID); err != nil {
		t.Fatalf("Id = %q: %v", p.Note.Header.ID, err)
	}
	if SlugOfID(p.Note.Header.ID) != "rowan" {
		t.Fatalf("Id = %q, want it in Rowan's namespace", p.Note.Header.ID)
	}
	wantPath := "from-rowan/2026-09-09T1234Z-the-gate-in-the-workflow-never-runs-" + strings.TrimPrefix(p.Note.Header.ID, "rowan-") + ".md"
	if p.Path != wantPath {
		t.Fatalf("Path = %q, want %q", p.Path, wantPath)
	}
	if p.Message != "rowan: The gate in the workflow never runs" {
		t.Fatalf("commit message = %q", p.Message)
	}
	if p.Sender.Name != "Rowan" {
		t.Fatalf("sender = %q", p.Sender.Name)
	}
	// --slug replaces only the human half.
	p2, err := Prepare(tab, draft, at("2026-09-09T12:34:56Z"), "ci-gate")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p2.Path, "-ci-gate-") {
		t.Fatalf("Path = %q, want the given slug", p2.Path)
	}
	if p2.Note.Header.ID != p.Note.Header.ID {
		t.Fatal("the slug changed the id; the id must not depend on the filename")
	}
}

func TestPrepareRefuses(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"an Id the author wrote", "From: Rowan\nTo: Stella\nId: rowan-000000000000\nSubject: s\n\nbody\n", "already carries an Id line"},
		{"a Date the author wrote", "From: Rowan\nTo: Stella\nDate: whenever\nSubject: s\n\nbody\n", "will not quietly replace yours"},
		{"an unknown recipient", "From: Rowan\nTo: Stela\nSubject: s\n\nbody\n", `"Stela"`},
		{"a sender with no lane", "From: Glenn\nTo: Rowan\nSubject: s\n\nbody\n", "has no lane"},
		{"an unknown sender", "From: Nobody\nTo: Rowan\nSubject: s\n\nbody\n", "names no one at this table"},
		{"no subject", "From: Rowan\nTo: Stella\nSubject:\n\nbody\n", "no Subject line"},
		{"no body", "From: Rowan\nTo: Stella\nSubject: s\n\n\n", "no body"},
		{"a Re naming nothing", "From: Rowan\nTo: Stella\nRe: stella-deadbeefcafe\nSubject: s\n\nbody\n", "a slug is not a thread"},
		{"a Re naming a path that does not exist", "From: Rowan\nTo: Stella\nRe: from-stella/gone.md\nSubject: s\n\nbody\n", "a slug is not a thread"},
	}
	tab := loadTable(t, writeTable(t, fixture()))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Prepare(tab, tc.text, at("2026-09-09T12:34:56Z"), "")
			if err == nil {
				t.Fatal("want a refusal, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestPrepareAcceptsAReByIDAndByLegacyPath(t *testing.T) {
	tab := loadTable(t, writeTable(t, fixture()))
	for _, re := range []string{"stella-abcdef012345", "from-stella/2026-09-06-legacy-note.md", "new"} {
		text := "From: Rowan\nTo: Stella\nRe: " + re + "\nSubject: s\n\nbody\n"
		if _, err := Prepare(tab, text, at("2026-09-09T12:34:56Z"), ""); err != nil {
			t.Fatalf("Re: %s was refused: %v", re, err)
		}
	}
}

// Sending the same draft in the same second twice is one note, and the second is refused
// by name rather than overwriting the first.
func TestPrepareRefusesAnIDAlreadyOnTheTable(t *testing.T) {
	root := writeTable(t, fixture())
	tab := loadTable(t, root)
	now := at("2026-09-09T12:34:56Z")
	p, err := Prepare(tab, draft, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Save(root); err != nil {
		t.Fatal(err)
	}
	tab = loadTable(t, root)
	_, err = Prepare(tab, draft, now, "")
	if err == nil {
		t.Fatal("the same note sent twice in one second was accepted twice")
	}
	if !strings.Contains(err.Error(), "already on this table") {
		t.Fatalf("refusal %q", err)
	}
	// A second later it is a different note and goes through, which is the reason the
	// date is in the id's preimage at all.
	if _, err := Prepare(loadTable(t, root), draft, at("2026-09-09T12:34:57Z"), ""); err != nil {
		t.Fatalf("the same words a second later were refused: %v", err)
	}
}

func TestWriteRefusesToOverwrite(t *testing.T) {
	root := writeTable(t, fixture())
	p, err := Prepare(loadTable(t, root), draft, at("2026-09-09T12:34:56Z"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Save(root); err != nil {
		t.Fatal(err)
	}
	if err := p.Save(root); err == nil {
		t.Fatal("a note once written was rewritten; the table's rule is that it is not")
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.Path)))
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseNote(p.Path, string(raw))
	if err != nil {
		t.Fatalf("what was written does not parse: %v", err)
	}
	if back.Header.ID != p.Note.Header.ID || back.Header.Date != p.Note.Header.Date {
		t.Fatal("the written note lost its Id or Date")
	}
	if back.Header.From != "Rowan (bud, the Studio, the mas account)" {
		t.Fatalf("the author's own From line was rewritten to %q", back.Header.From)
	}
}

// A note sent by the tool passes check, which is the only interesting round trip here.
func TestASentNotePassesCheck(t *testing.T) {
	root := writeTable(t, fixture())
	p, err := Prepare(loadTable(t, root), draft, at("2026-09-09T12:34:56Z"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Save(root); err != nil {
		t.Fatal(err)
	}
	if ps := loadTable(t, root).Check(); len(ps) != 0 {
		t.Fatalf("a note this tool wrote failed check: %+v", ps)
	}
}

func TestPlanReceipts(t *testing.T) {
	root := writeTable(t, fixture())
	tab := loadTable(t, root)
	rowan := mustParticipant(t, tab.Config, "Rowan")
	now := at("2026-09-09T12:34:56Z")

	plan, err := PlanReceipts(tab, rowan, []string{"stella-abcdef012345", "from-stella/2026-09-06-legacy-note.md"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Path != "from-rowan/RECEIPTS" {
		t.Fatalf("Path = %q", plan.Path)
	}
	// A note with an id is recorded BY id; a legacy note by the only name it has.
	want := []string{"stella-abcdef012345", "from-stella/2026-09-06-legacy-note.md"}
	if strings.Join(plan.Record, "|") != strings.Join(want, "|") {
		t.Fatalf("Record = %v, want %v", plan.Record, want)
	}
	if err := plan.Append(root); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "from-rowan", ReceiptsName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "2026-09-09T12:34:56Z stella-abcdef012345\n") {
		t.Fatalf("RECEIPTS holds %q", raw)
	}

	// inbox honours it: the receipted notes are HEARD. They stay in the listing, because
	// heard is not answered and a note I acknowledged and never replied to is the state
	// this table loses most often -- but they are marked, and the caller counts them out
	// of what is still open.
	tab = loadTable(t, root)
	heard := 0
	for _, it := range tab.Inbox(rowan, 40) {
		if it.Note.Header.ID != "stella-abcdef012345" && it.Note.Path != "from-stella/2026-09-06-legacy-note.md" {
			continue
		}
		if !it.Heard {
			t.Fatalf("a receipted note is reported as unheard: %s", it.Note.Path)
		}
		heard++
	}
	if heard != 2 {
		t.Fatalf("%d of the two receipted notes are in the listing; a receipt must not make a note vanish", heard)
	}

	// Recording the same note again is reported, not written twice.
	plan2, err := PlanReceipts(tab, rowan, []string{"stella-abcdef012345"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan2.Record) != 0 || len(plan2.Already) != 1 {
		t.Fatalf("a second receipt: record=%v already=%v", plan2.Record, plan2.Already)
	}
	// And a note recorded by id is not recordable again under its path.
	plan3, err := PlanReceipts(tab, rowan, []string{"from-stella/2026-09-07T0001Z-a-question-abcdef012345.md"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan3.Record) != 0 {
		t.Fatalf("the same note was recorded twice under its two names: %v", plan3.Record)
	}
}

func TestPlanReceiptsRefuses(t *testing.T) {
	root := writeTable(t, fixture())
	tab := loadTable(t, root)
	rowan := mustParticipant(t, tab.Config, "Rowan")
	stella := mustParticipant(t, tab.Config, "Stella")
	glenn := mustParticipant(t, tab.Config, "Glenn")
	now := at("2026-09-09T12:34:56Z")

	if _, err := PlanReceipts(tab, rowan, []string{"stella-deadbeefcafe"}, now); err == nil {
		t.Fatal("a receipt for a note that does not exist was accepted")
	}
	if _, err := PlanReceipts(tab, rowan, nil, now); err == nil {
		t.Fatal("a receipt for nothing was accepted")
	}
	if _, err := PlanReceipts(tab, stella, []string{"stella-abcdef012345"}, now); err == nil {
		t.Fatal("a receipt for one's own note was accepted")
	}
	if _, err := PlanReceipts(tab, glenn, []string{"stella-abcdef012345"}, now); err == nil {
		t.Fatal("a participant with no lane recorded a receipt")
	}
}

// --slug is the one piece of a note's PATH a caller supplies, and it was written into the
// filename unchecked. "../../x" walks out of the lane and out of the table; "a/b" invents a
// directory; a newline forges a second line in anything that lists the path. Every one of
// them is a refusal, and the refusal happens in Prepare, before the table is touched.
func TestPrepareRefusesASlugThatIsNotASlug(t *testing.T) {
	tab := loadTable(t, writeTable(t, fixture()))
	when := at("2026-09-09T12:34:56Z")
	for _, slug := range []string{
		"../x",
		"../../etc/passwd",
		"a/b",
		"a\nb",
		"a b",
		" ",
		"Uppercase",
		"trailing-",
		"-leading",
		strings.Repeat("x", SlugMax+1),
	} {
		t.Run(slug, func(t *testing.T) {
			p, err := Prepare(tab, draft, when, slug)
			if err == nil {
				t.Fatalf("--slug %q was accepted and wrote %q", slug, p.Path)
			}
			if !strings.Contains(err.Error(), "--slug") {
				t.Fatalf("the refusal does not name the flag: %v", err)
			}
		})
	}
	// An EMPTY --slug is not an override at all: it is the flag not given, and the slug
	// comes from the subject as it always did.
	p, err := Prepare(tab, draft, when, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Path, "-the-gate-in-the-workflow-never-runs-") {
		t.Fatalf("an empty --slug did not fall back to the subject: %q", p.Path)
	}
	// And the shapes a person actually types are still accepted.
	for _, slug := range []string{"ci-gate", "gate2", "a"} {
		if _, err := Prepare(tab, draft, when, slug); err != nil {
			t.Fatalf("--slug %q is a slug and was refused: %v", slug, err)
		}
	}
}

// The last wall, behind the flag check: whatever built the path, Save will not write
// outside the table. This constructs the Prepared by hand precisely because Prepare would
// not produce it -- the assertion is the point, and an assertion nothing can reach today
// is one nobody has to remember tomorrow.
func TestSaveRefusesToWriteOutsideTheTable(t *testing.T) {
	root := writeTable(t, fixture())
	tab := loadTable(t, root)
	p, err := Prepare(tab, draft, at("2026-09-09T12:34:56Z"), "")
	if err != nil {
		t.Fatal(err)
	}
	p.Path = "../escaped.md"
	if err := p.Save(root); err == nil {
		t.Fatal("Save wrote outside the table root")
	} else if !strings.Contains(err.Error(), "not inside the table") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escaped.md")); err == nil {
		t.Fatal("a file was written outside the table root")
	}
}
