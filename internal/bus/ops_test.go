package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const draft = `From: Ada (day shift, the west host, the shared account)
To: Bo
Cc: Dana
Subject: The gate in the workflow never runs

Bo,

The matrix key is misspelled, so the step is skipped.
`

func TestPrepareAssignsTheDateTheIDAndThePath(t *testing.T) {
	t.Parallel()
	tab := loadBus(t, writeBus(t, fixture()))
	p, err := Prepare(tab, draft, at("2026-09-09T12:34:56Z"), "")
	require.NoError(t, err)
	require.Equal(t, "Wed Sep  9 12:34:56 UTC 2026", p.Note.Header.Date, "Date = %q; it is pasted from the clock in UTC", p.Note.Header.Date)
	require.NoError(t, ValidID(p.Note.Header.ID), "Id = %q", p.Note.Header.ID)
	require.Equal(t, "ada", SlugOfID(p.Note.Header.ID), "Id = %q, want it in Ada's namespace", p.Note.Header.ID)
	wantPath := "from-ada/2026-09-09T1234Z-the-gate-in-the-workflow-never-runs-" + strings.TrimPrefix(p.Note.Header.ID, "ada-") + ".md"
	require.Equal(t, wantPath, p.Path, "Path = %q, want %q", p.Path, wantPath)
	require.Equal(t, "ada: The gate in the workflow never runs", p.Message, "commit message = %q", p.Message)
	require.Equal(t, "Ada", p.Sender.Name, "sender = %q", p.Sender.Name)
	// --slug replaces only the human half.
	p2, err := Prepare(tab, draft, at("2026-09-09T12:34:56Z"), "ci-gate")
	require.NoError(t, err)
	require.Contains(t, p2.Path, "-ci-gate-", "Path = %q, want the given slug", p2.Path)
	require.Equal(t, p.Note.Header.ID, p2.Note.Header.ID, "the slug changed the id; the id must not depend on the filename")
}

func TestPrepareRefuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, text, want string
	}{
		{"an Id the author wrote", "From: Ada\nTo: Bo\nId: ada-000000000000\nSubject: s\n\nbody\n", "already carries an Id line"},
		{"an unknown recipient", "From: Ada\nTo: Boe\nSubject: s\n\nbody\n", `"Boe"`},
		{"a sender with no lane", "From: Dana\nTo: Ada\nSubject: s\n\nbody\n", "has no lane"},
		{"an unknown sender", "From: Nobody\nTo: Ada\nSubject: s\n\nbody\n", "names no one on this bus"},
		{"no subject", "From: Ada\nTo: Bo\nSubject:\n\nbody\n", "no Subject line"},
		{"no body", "From: Ada\nTo: Bo\nSubject: s\n\n\n", "no body"},
		{"a Re naming nothing", "From: Ada\nTo: Bo\nRe: bo-deadbeefcafe\nSubject: s\n\nbody\n", "a slug is not a thread"},
		{"a Re naming a path that does not exist", "From: Ada\nTo: Bo\nRe: from-bo/gone.md\nSubject: s\n\nbody\n", "a slug is not a thread"},
	}
	tab := loadBus(t, writeBus(t, fixture()))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Prepare(tab, tc.text, at("2026-09-09T12:34:56Z"), "")
			require.Error(t, err, "want a refusal, got none")
			if !strings.Contains(err.Error(), tc.want) {
				require.Contains(t, err.Error(), tc.want, "refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestPrepareAcceptsAReByIDAndByLegacyPath(t *testing.T) {
	t.Parallel()
	tab := loadBus(t, writeBus(t, fixture()))
	for _, re := range []string{"bo-abcdef012345", "from-bo/2026-09-06-legacy-note.md", "new"} {
		text := "From: Ada\nTo: Bo\nRe: " + re + "\nSubject: s\n\nbody\n"
		{
			_, err := Prepare(tab, text, at("2026-09-09T12:34:56Z"), "")
			require.NoError(t, err, "Re: %s was refused: %v", re, err)
		}
	}
}

// Sending the same draft in the same second twice is one note, and the second is refused
// by name rather than overwriting the first.
func TestPrepareRefusesAnIDAlreadyOnTheBus(t *testing.T) {
	t.Parallel()
	root := writeBus(t, fixture())
	tab := loadBus(t, root)
	now := at("2026-09-09T12:34:56Z")
	p, err := Prepare(tab, draft, now, "")
	require.NoError(t, err)
	require.NoError(t, p.Save(root))
	tab = loadBus(t, root)
	_, err = Prepare(tab, draft, now, "")
	require.Error(t, err, "the same note sent twice in one second was accepted twice")
	require.Contains(t, err.Error(), "already on this bus", "refusal %q", err)
	// A second later it is a different note and goes through, which is the reason the
	// date is in the id's preimage at all.
	if _, err := Prepare(loadBus(t, root), draft, at("2026-09-09T12:34:57Z"), ""); err != nil {
		require.NoError(t, err, "the same words a second later were refused: %v", err)
	}
}

func TestWriteRefusesToOverwrite(t *testing.T) {
	t.Parallel()
	root := writeBus(t, fixture())
	p, err := Prepare(loadBus(t, root), draft, at("2026-09-09T12:34:56Z"), "")
	require.NoError(t, err)
	require.NoError(t, p.Save(root))
	{
		err := p.Save(root)
		require.Error(t, err, "a note once written was rewritten; the bus's rule is that it is not")
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.Path)))
	require.NoError(t, err)
	back, err := ParseNote(p.Path, string(raw))
	require.NoError(t, err, "what was written does not parse: %v", err)
	require.False(t, back.Header.ID != p.Note.Header.ID || back.Header.Date != p.Note.Header.Date, "the written note lost its Id or Date")
	if back.Header.From != "Ada (day shift, the west host, the shared account)" {
		require.Equal(t, "Ada (day shift, the west host, the shared account)", back.Header.From, "the author's own From line was rewritten to %q", back.Header.From)
	}
}

// A note sent by the tool passes check, which is the only interesting round trip here.
func TestASentNotePassesCheck(t *testing.T) {
	t.Parallel()
	root := writeBus(t, fixture())
	p, err := Prepare(loadBus(t, root), draft, at("2026-09-09T12:34:56Z"), "")
	require.NoError(t, err)
	require.NoError(t, p.Save(root))
	{
		ps := loadBus(t, root).Check()
		require.Equal(t, 0, len(ps), "a note this tool wrote failed check: %+v", ps)
	}
}

func TestPlanReceipts(t *testing.T) {
	t.Parallel()
	root := writeBus(t, fixture())
	tab := loadBus(t, root)
	ada := mustParticipant(t, tab.Config, "Ada")
	now := at("2026-09-09T12:34:56Z")

	plan, err := PlanReceipts(tab, ada, []string{"bo-abcdef012345", "from-bo/2026-09-06-legacy-note.md"}, now)
	require.NoError(t, err)
	if plan.Path != "from-ada/RECEIPTS" {
		require.Equal(t, "from-ada/RECEIPTS", plan.Path, "Path = %q", plan.Path)
	}
	// A note with an id is recorded BY id; a legacy note by the only name it has.
	want := []string{"bo-abcdef012345", "from-bo/2026-09-06-legacy-note.md"}
	if strings.Join(plan.Record, "|") != strings.Join(want, "|") {
		require.False(t, strings.Join(plan.Record, "|") != strings.Join(want, "|"), "Record = %v, want %v", plan.Record, want)
	}
	require.NoError(t, plan.Append(root))
	raw, err := os.ReadFile(filepath.Join(root, "from-ada", ReceiptsName))
	require.NoError(t, err)
	require.Contains(t, string(raw), "2026-09-09T12:34:56Z bo-abcdef012345\n", "RECEIPTS holds %q", raw)

	// inbox honours it: the receipted notes are HEARD. They stay in the listing, because
	// heard is not answered and a note I acknowledged and never replied to is the state
	// this bus loses most often -- but they are marked, and the caller counts them out
	// of what is still open.
	tab = loadBus(t, root)
	heard := 0
	for _, it := range tab.Inbox(ada, 40) {
		if it.Note.Header.ID != "bo-abcdef012345" && it.Note.Path != "from-bo/2026-09-06-legacy-note.md" {
			continue
		}
		if !it.Heard {
			require.True(t, it.Heard, "a receipted note is reported as unheard: %s", it.Note.Path)
		}
		heard++
	}
	require.Equal(t, 2, heard, "%d of the two receipted notes are in the listing; a receipt must not make a note vanish", heard)

	// Recording the same note again is reported, not written twice.
	plan2, err := PlanReceipts(tab, ada, []string{"bo-abcdef012345"}, now)
	require.NoError(t, err)
	if len(plan2.Record) != 0 || len(plan2.Already) != 1 {
		require.False(t, len(plan2.Record) != 0 || len(plan2.Already) != 1, "a second receipt: record=%v already=%v", plan2.Record, plan2.Already)
	}
	// And a note recorded by id is not recordable again under its path.
	plan3, err := PlanReceipts(tab, ada, []string{"from-bo/2026-09-07T0001Z-a-question-abcdef012345.md"}, now)
	require.NoError(t, err)
	if len(plan3.Record) != 0 {
		require.Equal(t, 0, len(plan3.Record), "the same note was recorded twice under its two names: %v", plan3.Record)
	}
}

func TestPlanReceiptsRefuses(t *testing.T) {
	t.Parallel()
	root := writeBus(t, fixture())
	tab := loadBus(t, root)
	ada := mustParticipant(t, tab.Config, "Ada")
	bo := mustParticipant(t, tab.Config, "Bo")
	dana := mustParticipant(t, tab.Config, "Dana")
	now := at("2026-09-09T12:34:56Z")

	{
		_, err := PlanReceipts(tab, ada, []string{"bo-deadbeefcafe"}, now)
		require.Error(t, err, "a receipt for a note that does not exist was accepted")
	}
	{
		_, err := PlanReceipts(tab, ada, nil, now)
		require.Error(t, err, "a receipt for nothing was accepted")
	}
	{
		_, err := PlanReceipts(tab, bo, []string{"bo-abcdef012345"}, now)
		require.Error(t, err, "a receipt for one's own note was accepted")
	}
	{
		_, err := PlanReceipts(tab, dana, []string{"bo-abcdef012345"}, now)
		require.Error(t, err, "a participant with no lane recorded a receipt")
	}
}

// --slug is the one piece of a note's PATH a caller supplies, and it was written into the
// filename unchecked. "../../x" walks out of the lane and out of the bus; "a/b" invents a
// directory; a newline forges a second line in anything that lists the path. Every one of
// them is a refusal, and the refusal happens in Prepare, before the bus is touched.
func TestPrepareRefusesASlugThatIsNotASlug(t *testing.T) {
	t.Parallel()
	tab := loadBus(t, writeBus(t, fixture()))
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
				require.Error(t, err, "--slug %q was accepted and wrote %q", slug, p.Path)
			}
			require.Contains(t, err.Error(), "--slug", "the refusal does not name the flag: %v", err)
		})
	}
	// An EMPTY --slug is not an override at all: it is the flag not given, and the slug
	// comes from the subject as it always did.
	p, err := Prepare(tab, draft, when, "")
	require.NoError(t, err)
	if !strings.Contains(p.Path, "-the-gate-in-the-workflow-never-runs-") {
		require.Contains(t, p.Path, "-the-gate-in-the-workflow-never-runs-", "an empty --slug did not fall back to the subject: %q", p.Path)
	}
	// And the shapes a person actually types are still accepted.
	for _, slug := range []string{"ci-gate", "gate2", "a"} {
		{
			_, err := Prepare(tab, draft, when, slug)
			require.NoError(t, err, "--slug %q is a slug and was refused: %v", slug, err)
		}
	}
}

// The last wall, behind the flag check: whatever built the path, Save will not write
// outside the bus. This constructs the Prepared by hand precisely because Prepare would
// not produce it -- the assertion is the point, and an assertion nothing can reach today
// is one nobody has to remember tomorrow.
func TestSaveRefusesToWriteOutsideTheBus(t *testing.T) {
	t.Parallel()
	root := writeBus(t, fixture())
	tab := loadBus(t, root)
	p, err := Prepare(tab, draft, at("2026-09-09T12:34:56Z"), "")
	require.NoError(t, err)
	p.Path = "../escaped.md"
	if err := p.Save(root); err == nil {
		require.FailNow(t, "Save wrote outside the bus root")
	} else if !strings.Contains(err.Error(), "not inside the bus") {
		require.FailNowf(t, "assertion failed", "the refusal does not say why: %v", err)
	}
	{
		_, err := os.Stat(filepath.Join(filepath.Dir(root), "escaped.md"))
		require.Error(t, err, "a file was written outside the bus root")
	}
}
