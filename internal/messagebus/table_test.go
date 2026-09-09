package messagebus

import (
	"strings"
	"testing"
)

// The fixture table. It carries a note with an id, a LEGACY note with none, a bare
// receipt, and a note to somebody else, because the answered rule and the inbox have to be
// right about all four.
func fixture() map[string]string {
	return map[string]string{
		"from-stella/2026-09-07T0001Z-a-question-abcdef012345.md": `From: Stella Codex
To: Rowan
Date: Mon Sep  7 00:01:00 UTC 2026
Id: stella-abcdef012345
Subject: A question about the gate

Should the gate run on the merge queue too?
`,
		"from-stella/2026-09-06-legacy-note.md": `From: Stella
To: Rowan
Date: Sun Sep  6 23:00:00 UTC 2026
Subject: The packet void, before ids existed

A finding, written when a Re line named a path and nothing else.
`,
		"from-stella/2026-09-07T0002Z-heard-111111111111.md": `From: Stella
To: Rowan
Date: Mon Sep  7 00:02:00 UTC 2026
Id: stella-111111111111
Subject: Heard

Heard, thank you.
`,
		"from-stella/2026-09-07T0003Z-for-glenn-222222222222.md": `From: Stella
To: Glenn
Cc: Rowan
Date: Mon Sep  7 00:03:00 UTC 2026
Id: stella-222222222222
Subject: A note to Glenn, copied to Rowan

The body.
`,
	}
}

func TestReadTableReadsEveryLane(t *testing.T) {
	tab := loadTable(t, writeTable(t, fixture()))
	if len(tab.Notes) != 4 {
		t.Fatalf("read %d notes, want 4", len(tab.Notes))
	}
	if n, ok := tab.NoteByID("stella-abcdef012345"); !ok || n.Header.Subject != "A question about the gate" {
		t.Fatalf("NoteByID = %v %v", n, ok)
	}
	if _, ok := tab.NoteByPath("from-stella/2026-09-06-legacy-note.md"); !ok {
		t.Fatal("a legacy note is not addressable by path")
	}
	if _, ok := tab.NoteByID(""); ok {
		t.Fatal("the empty id resolved; a legacy note has no id and must not answer to one")
	}
}

// The answered rule: an id on a Re line, a PATH on a Re line for a legacy note, and a
// receipt. All three, in my own lane and nowhere else.
func TestAnsweredRule(t *testing.T) {
	files := fixture()
	files["from-rowan/2026-09-07T0010Z-an-answer-999999999999.md"] = `From: Rowan
To: Stella
Date: Mon Sep  7 00:10:00 UTC 2026
Id: rowan-999999999999
Re: stella-abcdef012345
Subject: Yes, on the merge queue too

The answer.
`
	files["from-rowan/RECEIPTS"] = "# a comment line\n2026-09-07T00:11:00Z stella-111111111111\n"
	root := writeTable(t, files)
	tab := loadTable(t, root)

	byID, _ := tab.NoteByID("stella-abcdef012345")
	if by, ok := tab.AnsweredBy(byID, "from-rowan"); !ok || by != "from-rowan/2026-09-07T0010Z-an-answer-999999999999.md" {
		t.Fatalf("a note answered by id reads as %q %v", by, ok)
	}
	// The same note is NOT answered in Stella's own lane: the rule is per reader.
	if _, ok := tab.AnsweredBy(byID, "from-stella"); ok {
		t.Fatal("a note is answered in its own sender's lane; the rule is per reader")
	}
	legacy, _ := tab.NoteByPath("from-stella/2026-09-06-legacy-note.md")
	if _, ok := tab.AnsweredBy(legacy, "from-rowan"); ok {
		t.Fatal("the legacy note is answered before anything answers it")
	}
	receipted, _ := tab.NoteByID("stella-111111111111")
	if by, ok := tab.AnsweredBy(receipted, "from-rowan"); !ok || by != "from-rowan/RECEIPTS" {
		t.Fatalf("a receipt did not answer: %q %v", by, ok)
	}

	// Now answer the legacy note by its PATH, which is the only name it has.
	files["from-rowan/2026-09-07T0012Z-on-the-void-888888888888.md"] = `From: Rowan
To: Stella
Date: Mon Sep  7 00:12:00 UTC 2026
Id: rowan-888888888888
Re: from-stella/2026-09-06-legacy-note.md
Subject: On the packet void

The answer to a note that has no id.
`
	tab = loadTable(t, writeTable(t, files))
	legacy, _ = tab.NoteByPath("from-stella/2026-09-06-legacy-note.md")
	if by, ok := tab.AnsweredBy(legacy, "from-rowan"); !ok || !strings.Contains(by, "888888888888") {
		t.Fatalf("a legacy note answered by path reads as %q %v", by, ok)
	}
}

// A note carrying an id may ALSO be answered by its path, so an answer written by hand
// before the tool existed keeps working.
func TestANoteWithAnIDIsStillAnswerableByPath(t *testing.T) {
	files := fixture()
	files["from-rowan/2026-09-07T0010Z-by-path-777777777777.md"] = `From: Rowan
To: Stella
Date: Mon Sep  7 00:10:00 UTC 2026
Id: rowan-777777777777
Re: from-stella/2026-09-07T0001Z-a-question-abcdef012345.md
Subject: Answered by path

The answer.
`
	tab := loadTable(t, writeTable(t, files))
	n, _ := tab.NoteByID("stella-abcdef012345")
	if _, ok := tab.AnsweredBy(n, "from-rowan"); !ok {
		t.Fatal("an answer naming the path of a note that HAS an id did not count")
	}
}

func TestInboxSeparatesReceiptsFromNotesAndOrdersNewestFirst(t *testing.T) {
	tab := loadTable(t, writeTable(t, fixture()))
	rowan := mustParticipant(t, tab.Config, "Rowan")
	items := tab.Inbox(rowan, 40)
	if len(items) != 4 {
		t.Fatalf("inbox has %d items, want 4", len(items))
	}
	if !items[0].Note.When().After(items[len(items)-1].Note.When()) {
		t.Fatal("the inbox is not newest first")
	}
	var receipts, notes []string
	for _, it := range items {
		if it.Receipt {
			receipts = append(receipts, it.Note.Header.Subject)
		} else {
			notes = append(notes, it.Note.Header.Subject)
		}
	}
	if len(receipts) != 1 || receipts[0] != "Heard" {
		t.Fatalf("receipts = %v, want just the bare acknowledgement", receipts)
	}
	if len(notes) != 3 {
		t.Fatalf("notes = %v, want the other three", notes)
	}
	// The Cc'd note is in the inbox, marked as a Cc rather than a direct address.
	for _, it := range items {
		if it.Note.Header.ID == "stella-222222222222" && it.Address != "cc" {
			t.Fatalf("a Cc'd note reads as addr=%q", it.Address)
		}
	}
}

func TestInboxDropsWhatIsAnsweredAndWhatIsNotMine(t *testing.T) {
	files := fixture()
	files["from-rowan/2026-09-07T0010Z-an-answer-999999999999.md"] = `From: Rowan
To: Stella
Date: Mon Sep  7 00:10:00 UTC 2026
Id: rowan-999999999999
Re: stella-abcdef012345
Subject: Yes

The answer.
`
	tab := loadTable(t, writeTable(t, files))
	rowan := mustParticipant(t, tab.Config, "Rowan")
	for _, it := range tab.Inbox(rowan, 40) {
		if it.Note.Header.ID == "stella-abcdef012345" {
			t.Fatal("an answered note is still in the inbox")
		}
		if it.Note.Lane == "from-rowan" {
			t.Fatal("my own note is in my inbox")
		}
	}
	// Stella's inbox holds Rowan's answer and nothing of her own.
	stella := mustParticipant(t, tab.Config, "Stella")
	items := tab.Inbox(stella, 40)
	if len(items) != 1 || items[0].Note.Header.ID != "rowan-999999999999" {
		t.Fatalf("Stella's inbox = %d items, want just Rowan's answer", len(items))
	}
}

func TestCheckPassesACleanTable(t *testing.T) {
	files := fixture()
	files["from-rowan/RECEIPTS"] = "2026-09-07T00:11:00Z stella-111111111111\n"
	tab := loadTable(t, writeTable(t, files))
	if ps := tab.Check(); len(ps) != 0 {
		t.Fatalf("a clean table failed check: %+v", ps)
	}
}

// Every failure check can report, each proved failing on its own, because one fixture per
// condition alone would still go green if a regression collapsed every case into one
// spurious finding.
func TestCheckFailures(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			"a header that will not parse",
			map[string]string{"from-rowan/bad.md": "From: Rowan\nthis is prose\n\nbody\n"},
			"not a header line",
		},
		{
			"an unknown recipient",
			map[string]string{"from-rowan/x.md": "From: Rowan\nTo: Stela\nSubject: s\n\nbody\n"},
			`"Stela"`,
		},
		{
			"a note in the wrong lane",
			map[string]string{"from-rowan/x.md": "From: Stella\nTo: Rowan\nSubject: s\n\nbody\n"},
			`whose lane is "from-stella"`,
		},
		{
			"an id carrying another lane's slug",
			map[string]string{"from-rowan/x.md": "From: Rowan\nTo: Stella\nId: stella-000000000000\nSubject: s\n\nbody\n"},
			"carries the slug of lane",
		},
		{
			"a malformed id",
			map[string]string{"from-rowan/x.md": "From: Rowan\nTo: Stella\nId: rowan-nothex\nSubject: s\n\nbody\n"},
			"is not <sender>",
		},
		{
			"two notes with one id",
			map[string]string{
				"from-rowan/a.md": "From: Rowan\nTo: Stella\nId: rowan-000000000000\nSubject: a\n\nbody\n",
				"from-rowan/b.md": "From: Rowan\nTo: Stella\nId: rowan-000000000000\nSubject: b\n\nbody\n",
			},
			"is also the id of",
		},
		{
			"a Re that resolves to nothing",
			map[string]string{"from-rowan/x.md": "From: Rowan\nTo: Stella\nRe: stella-deadbeefcafe\nSubject: s\n\nbody\n"},
			"neither an id on this table nor a note that exists",
		},
		{
			"a Re naming a path that was renamed away",
			map[string]string{"from-rowan/x.md": "From: Rowan\nTo: Stella\nRe: from-stella/gone.md\nSubject: s\n\nbody\n"},
			"neither an id on this table nor a note that exists",
		},
		{
			"a lane nobody owns",
			map[string]string{"from-nobody/x.md": "From: Rowan\nTo: Stella\nSubject: s\n\nbody\n"},
			"owns this lane",
		},
		{
			"a receipt naming nothing",
			map[string]string{"from-rowan/RECEIPTS": "2026-09-07T00:11:00Z stella-deadbeefcafe\n"},
			"neither an id on this table nor a note that exists",
		},
		{
			"a receipt with no stamp",
			map[string]string{
				"from-rowan/RECEIPTS": "yesterday stella-abcdef012345\n",
				"from-stella/q.md":    "From: Stella\nTo: Rowan\nId: stella-abcdef012345\nSubject: s\n\nbody\n",
			},
			"is not a UTC stamp",
		},
		{
			"a receipt line with no target",
			map[string]string{"from-rowan/RECEIPTS": "2026-09-07T00:11:00Z\n"},
			"a receipt is",
		},
		{
			"something in a lane that is not a note",
			map[string]string{"from-rowan/notes.txt": "hello\n"},
			"and nothing else",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := loadTable(t, writeTable(t, tc.files))
			ps := tab.Check()
			if len(ps) == 0 {
				t.Fatal("check passed a table it should have failed")
			}
			var found bool
			for _, p := range ps {
				if strings.Contains(p.Reason, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no finding names %q; got %+v", tc.want, ps)
			}
		})
	}
}

// A Re of "new" is the table's own way of saying this starts a thread, and is not a
// dangling reference.
func TestCheckAcceptsReNew(t *testing.T) {
	tab := loadTable(t, writeTable(t, map[string]string{
		"from-rowan/x.md": "From: Rowan\nTo: Stella\nRe: new\nSubject: s\n\nbody\n",
	}))
	if ps := tab.Check(); len(ps) != 0 {
		t.Fatalf("Re: new was refused: %+v", ps)
	}
}

// One bad file must not blind the rest of the run: check names every failure it finds.
func TestCheckReportsEveryFailureNotTheFirst(t *testing.T) {
	tab := loadTable(t, writeTable(t, map[string]string{
		"from-rowan/a.md": "From: Rowan\nnot a header\n\nbody\n",
		"from-rowan/b.md": "From: Rowan\nTo: Stela\nSubject: s\n\nbody\n",
		"from-rowan/c.md": "From: Rowan\nTo: Stella\nRe: stella-deadbeefcafe\nSubject: s\n\nbody\n",
	}))
	if ps := tab.Check(); len(ps) < 3 {
		t.Fatalf("check reported %d findings over three broken files: %+v", len(ps), ps)
	}
}

// A note to a GROUP is a note to every member of it, and must be in each of their inboxes.
// The group is expanded at the address layer, so nothing downstream knows the difference --
// which is precisely the property worth pinning, because a regression there is a note that
// looks delivered and is read by nobody.
func TestAGroupAddressedNoteIsInEveryMembersInbox(t *testing.T) {
	files := fixture()
	files["from-stella/2026-09-07T0004Z-to-everyone-333333333333.md"] = `From: Stella
To: Everybody at the table
Date: Mon Sep  7 00:04:00 UTC 2026
Id: stella-333333333333
Subject: A note to the whole table

Something everyone needs, said once.
`
	tab := loadTable(t, writeTable(t, files))
	// Rowan is a member and has a lane, so the note is in his inbox. Stella wrote it, so
	// it is not in hers: a note in my own lane is never in my inbox, group or not.
	rowan := mustParticipant(t, tab.Config, "Rowan")
	found := false
	for _, item := range tab.Inbox(rowan, 40) {
		if item.Note.Header.ID == "stella-333333333333" {
			found = true
			if item.Address != "to" {
				t.Fatalf("addr = %q, want to", item.Address)
			}
		}
	}
	if !found {
		t.Fatal("a note addressed to a group Rowan belongs to is not in Rowan's inbox")
	}
	stella := mustParticipant(t, tab.Config, "Stella")
	for _, item := range tab.Inbox(stella, 40) {
		if item.Note.Header.ID == "stella-333333333333" {
			t.Fatal("Stella's own note is in Stella's inbox")
		}
	}
	// And the resolution itself names every member, including the one with no lane, who is
	// addressable and never a sender.
	to, unknown := tab.Config.ResolveList("Everybody at the table")
	if len(unknown) > 0 || strings.Join(to, "; ") != "Rowan; Stella; Glenn" {
		t.Fatalf("the group resolved to %v %v", to, unknown)
	}
}

// A file that will not parse is a note somebody wrote, on the table, that inbox used to
// step over in silence. It is now named, with its reason, on every run.
func TestUnreadableNotesAreNamedAndNotSilent(t *testing.T) {
	files := fixture()
	files["from-stella/2026-09-07T0005Z-prose.md"] = "Rowan, the checkpoint is pushed and the suite passed: zero divergence.\n\nMore prose.\n"
	files["from-rowan/2026-09-07T0006Z-mine.md"] = "not a header at all\n\nbody\n"
	tab := loadTable(t, writeTable(t, files))
	rowan := mustParticipant(t, tab.Config, "Rowan")

	bad := tab.Unreadable(rowan.Lane)
	if len(bad) != 1 || bad[0].Path != "from-stella/2026-09-07T0005Z-prose.md" {
		t.Fatalf("Unreadable = %v, want the one file outside my own lane", bad)
	}
	if bad[0].Parse == nil || bad[0].Parse.Err == nil {
		t.Fatal("an unreadable note carries no reason")
	}
	// It is NOT in the inbox listing: an unreadable note has no To line, so nothing can
	// honestly say it was addressed to me. It is reported separately, which is the whole
	// point.
	for _, item := range tab.Inbox(rowan, 40) {
		if item.Note.Path == bad[0].Path {
			t.Fatal("an unparseable note was reported as an open note addressed to me")
		}
	}
	// check still fails on both, including the one in my own lane.
	if n := len(tab.Check()); n < 2 {
		t.Fatalf("check found %d problems over two unreadable notes", n)
	}
}

// The adoption problem: a table written by hand for months, checked for the first time.
// Without a tolerance the first run is a wall of red nobody can act on, and the check gets
// turned off -- which is worse than not having it.
func TestLegacyBeforeWarnsOnOldNotesAndStillFailsOnNew(t *testing.T) {
	files := fixture()
	// Old and unreadable; old with a Re that names nothing; new and unreadable.
	files["from-stella/2026-09-01T0001Z-old-prose.md"] = "Rowan, this predates the tool entirely.\n\nbody\n"
	files["from-stella/2026-09-01T0002Z-old-dangling.md"] = `From: Stella
To: Rowan
Date: Tue Sep  1 00:02:00 UTC 2026
Subject: An answer to a note whose file was renamed

Re lines named filenames once, and a rename orphaned this one.
`
	// The Re has to be on the note itself, not in the body.
	files["from-stella/2026-09-01T0002Z-old-dangling.md"] = strings.Replace(
		files["from-stella/2026-09-01T0002Z-old-dangling.md"],
		"Subject: An answer",
		"Re: from-stella/renamed-away.md\nSubject: An answer", 1)
	files["from-stella/2026-09-08T0001Z-new-prose.md"] = "Rowan, this was written after the tool arrived.\n\nbody\n"
	tab := loadTable(t, writeTable(t, files))

	// Without the flag, all three FAIL.
	strict := tab.Check()
	for _, p := range strict {
		if p.Warn {
			t.Fatalf("Check() tolerated %s without being asked to: %s", p.Where, p.Reason)
		}
	}
	if len(strict) < 3 {
		t.Fatalf("Check() found %d problems, want at least the three planted: %+v", len(strict), strict)
	}

	// With it, the two old ones warn and the new one still fails.
	before := at("2026-09-05T00:00:00Z")
	got := tab.CheckWith(CheckOptions{LegacyBefore: before})
	warned, failed := map[string]bool{}, map[string]bool{}
	for _, p := range got {
		if p.Warn {
			warned[p.Where] = true
		} else {
			failed[p.Where] = true
		}
	}
	for _, where := range []string{
		"from-stella/2026-09-01T0001Z-old-prose.md",
		"from-stella/2026-09-01T0002Z-old-dangling.md:4",
	} {
		if !warned[where] {
			t.Fatalf("%s was not tolerated; warned=%v failed=%v", where, warned, failed)
		}
	}
	if !failed["from-stella/2026-09-08T0001Z-new-prose.md"] {
		t.Fatalf("a note written after the cutoff was tolerated; failed=%v", failed)
	}
	// The tolerance is NARROW. A finding that is not a parse failure or a dangling Re
	// fails at any date, because none of the others is a thing the table's history made
	// unavoidable.
	files2 := fixture()
	files2["from-stella/2026-09-01T0003Z-stranger.md"] = `From: Stella
To: Nobody At All
Date: Tue Sep  1 00:03:00 UTC 2026
Subject: A note to somebody the roster does not know

body
`
	tab2 := loadTable(t, writeTable(t, files2))
	for _, p := range tab2.CheckWith(CheckOptions{LegacyBefore: before}) {
		if p.Warn {
			t.Fatalf("an unknown recipient was tolerated as legacy: %s: %s", p.Where, p.Reason)
		}
	}
}

// A note that cannot say WHEN it was written cannot claim to predate anything.
func TestLegacyToleranceNeedsADateItCanRead(t *testing.T) {
	files := fixture()
	files["from-stella/undated-prose.md"] = "Rowan, no date line and no minute in the filename.\n\nbody\n"
	tab := loadTable(t, writeTable(t, files))
	for _, p := range tab.CheckWith(CheckOptions{LegacyBefore: at("2030-01-01T00:00:00Z")}) {
		if p.Where == "from-stella/undated-prose.md" && p.Warn {
			t.Fatal("a note with no readable date was tolerated as old")
		}
	}
}
