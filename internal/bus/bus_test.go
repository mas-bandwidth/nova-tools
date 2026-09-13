package bus

import (
	"fmt"
	"strings"
	"testing"
)

// The fixture bus. It carries a note with an id, a LEGACY note with none, a bare
// receipt, and a note to somebody else, because the answered rule and the inbox have to be
// right about all four.
func fixture() map[string]string {
	return map[string]string{
		"from-bo/2026-09-07T0001Z-a-question-abcdef012345.md": `From: Bo Quill
To: Ada
Date: Mon Sep  7 00:01:00 UTC 2026
Id: bo-abcdef012345
Subject: A question about the gate

Should the gate run on the merge queue too?
`,
		"from-bo/2026-09-06-legacy-note.md": `From: Bo
To: Ada
Date: Sun Sep  6 23:00:00 UTC 2026
Subject: The packet void, before ids existed

A finding, written when a Re line named a path and nothing else.
`,
		"from-bo/2026-09-07T0002Z-heard-111111111111.md": `From: Bo
To: Ada
Date: Mon Sep  7 00:02:00 UTC 2026
Id: bo-111111111111
Subject: Heard

Heard, thank you.
`,
		"from-bo/2026-09-07T0003Z-for-dana-222222222222.md": `From: Bo
To: Dana
Cc: Ada
Date: Mon Sep  7 00:03:00 UTC 2026
Id: bo-222222222222
Subject: A note to Dana, copied to Ada

The body.
`,
	}
}

func TestReadBusReadsEveryLane(t *testing.T) {
	t.Parallel()
	tab := loadBus(t, writeBus(t, fixture()))
	if len(tab.Notes) != 4 {
		t.Fatalf("read %d notes, want 4", len(tab.Notes))
	}
	if n, ok := tab.NoteByID("bo-abcdef012345"); !ok || n.Header.Subject != "A question about the gate" {
		t.Fatalf("NoteByID = %v %v", n, ok)
	}
	if _, ok := tab.NoteByPath("from-bo/2026-09-06-legacy-note.md"); !ok {
		t.Fatal("a legacy note is not addressable by path")
	}
	if _, ok := tab.NoteByID(""); ok {
		t.Fatal("the empty id resolved; a legacy note has no id and must not answer to one")
	}
}

// The answered rule: an id on a Re line, a PATH on a Re line for a legacy note, and a
// receipt. All three, in my own lane and nowhere else.
func TestAnsweredRule(t *testing.T) {
	t.Parallel()
	files := fixture()
	files["from-ada/2026-09-07T0010Z-an-answer-999999999999.md"] = `From: Ada
To: Bo
Date: Mon Sep  7 00:10:00 UTC 2026
Id: ada-999999999999
Re: bo-abcdef012345
Subject: Yes, on the merge queue too

The answer.
`
	files["from-ada/RECEIPTS"] = "# a comment line\n2026-09-07T00:11:00Z bo-111111111111\n"
	root := writeBus(t, files)
	tab := loadBus(t, root)

	byID, _ := tab.NoteByID("bo-abcdef012345")
	if by, ok := tab.AnsweredBy(byID, "from-ada"); !ok || by != "from-ada/2026-09-07T0010Z-an-answer-999999999999.md" {
		t.Fatalf("a note answered by id reads as %q %v", by, ok)
	}
	// The same note is NOT answered in Bo's own lane: the rule is per reader.
	if _, ok := tab.AnsweredBy(byID, "from-bo"); ok {
		t.Fatal("a note is answered in its own sender's lane; the rule is per reader")
	}
	legacy, _ := tab.NoteByPath("from-bo/2026-09-06-legacy-note.md")
	if _, ok := tab.AnsweredBy(legacy, "from-ada"); ok {
		t.Fatal("the legacy note is answered before anything answers it")
	}
	receipted, _ := tab.NoteByID("bo-111111111111")
	if by, ok := tab.AnsweredBy(receipted, "from-ada"); !ok || by != "from-ada/RECEIPTS" {
		t.Fatalf("a receipt did not answer: %q %v", by, ok)
	}

	// Now answer the legacy note by its PATH, which is the only name it has.
	files["from-ada/2026-09-07T0012Z-on-the-void-888888888888.md"] = `From: Ada
To: Bo
Date: Mon Sep  7 00:12:00 UTC 2026
Id: ada-888888888888
Re: from-bo/2026-09-06-legacy-note.md
Subject: On the packet void

The answer to a note that has no id.
`
	tab = loadBus(t, writeBus(t, files))
	legacy, _ = tab.NoteByPath("from-bo/2026-09-06-legacy-note.md")
	if by, ok := tab.AnsweredBy(legacy, "from-ada"); !ok || !strings.Contains(by, "888888888888") {
		t.Fatalf("a legacy note answered by path reads as %q %v", by, ok)
	}
}

// A note carrying an id may ALSO be answered by its path, so an answer written by hand
// before the tool existed keeps working.
func TestANoteWithAnIDIsStillAnswerableByPath(t *testing.T) {
	t.Parallel()
	files := fixture()
	files["from-ada/2026-09-07T0010Z-by-path-777777777777.md"] = `From: Ada
To: Bo
Date: Mon Sep  7 00:10:00 UTC 2026
Id: ada-777777777777
Re: from-bo/2026-09-07T0001Z-a-question-abcdef012345.md
Subject: Answered by path

The answer.
`
	tab := loadBus(t, writeBus(t, files))
	n, _ := tab.NoteByID("bo-abcdef012345")
	if _, ok := tab.AnsweredBy(n, "from-ada"); !ok {
		t.Fatal("an answer naming the path of a note that HAS an id did not count")
	}
}

func TestInboxSeparatesReceiptsFromNotesAndOrdersNewestFirst(t *testing.T) {
	t.Parallel()
	tab := loadBus(t, writeBus(t, fixture()))
	ada := mustParticipant(t, tab.Config, "Ada")
	items := tab.Inbox(ada, 40)
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
		if it.Note.Header.ID == "bo-222222222222" && it.Address != "cc" {
			t.Fatalf("a Cc'd note reads as addr=%q", it.Address)
		}
	}
}

func TestInboxDropsWhatIsAnsweredAndWhatIsNotMine(t *testing.T) {
	t.Parallel()
	files := fixture()
	files["from-ada/2026-09-07T0010Z-an-answer-999999999999.md"] = `From: Ada
To: Bo
Date: Mon Sep  7 00:10:00 UTC 2026
Id: ada-999999999999
Re: bo-abcdef012345
Subject: Yes

The answer.
`
	tab := loadBus(t, writeBus(t, files))
	ada := mustParticipant(t, tab.Config, "Ada")
	for _, it := range tab.Inbox(ada, 40) {
		if it.Note.Header.ID == "bo-abcdef012345" {
			t.Fatal("an answered note is still in the inbox")
		}
		if it.Note.Lane == "from-ada" {
			t.Fatal("my own note is in my inbox")
		}
	}
	// Bo's inbox holds Ada's answer and nothing of her own.
	bo := mustParticipant(t, tab.Config, "Bo")
	items := tab.Inbox(bo, 40)
	if len(items) != 1 || items[0].Note.Header.ID != "ada-999999999999" {
		t.Fatalf("Bo's inbox = %d items, want just Ada's answer", len(items))
	}
}

func TestCheckPassesACleanBus(t *testing.T) {
	t.Parallel()
	files := fixture()
	files["from-ada/RECEIPTS"] = "2026-09-07T00:11:00Z bo-111111111111\n"
	tab := loadBus(t, writeBus(t, files))
	if ps := tab.Check(); len(ps) != 0 {
		t.Fatalf("a clean bus failed check: %+v", ps)
	}
}

// Every failure check can report, each proved failing on its own, because one fixture per
// condition alone would still go green if a regression collapsed every case into one
// spurious finding.
func TestCheckFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			"a header that will not parse",
			map[string]string{"from-ada/bad.md": "From: Ada\nthis is prose\n\nbody\n"},
			"not a header line",
		},
		{
			"an unknown recipient",
			map[string]string{"from-ada/x.md": "From: Ada\nTo: Boe\nSubject: s\n\nbody\n"},
			`"Boe"`,
		},
		{
			"a note in the wrong lane",
			map[string]string{"from-ada/x.md": "From: Bo\nTo: Ada\nSubject: s\n\nbody\n"},
			`whose lane is "from-bo"`,
		},
		{
			"an id carrying another lane's slug",
			map[string]string{"from-ada/x.md": "From: Ada\nTo: Bo\nId: bo-000000000000\nSubject: s\n\nbody\n"},
			"carries the slug of lane",
		},
		{
			"a malformed id",
			map[string]string{"from-ada/x.md": "From: Ada\nTo: Bo\nId: ada-nothex\nSubject: s\n\nbody\n"},
			"is not <sender>",
		},
		{
			"two notes with one id",
			map[string]string{
				"from-ada/a.md": "From: Ada\nTo: Bo\nId: ada-000000000000\nSubject: a\n\nbody\n",
				"from-ada/b.md": "From: Ada\nTo: Bo\nId: ada-000000000000\nSubject: b\n\nbody\n",
			},
			"is also the id of",
		},
		{
			"a Re that resolves to nothing",
			map[string]string{"from-ada/x.md": "From: Ada\nTo: Bo\nRe: bo-deadbeefcafe\nSubject: s\n\nbody\n"},
			"neither an id on this bus nor a note that exists",
		},
		{
			"a Re naming a path that was renamed away",
			map[string]string{"from-ada/x.md": "From: Ada\nTo: Bo\nRe: from-bo/gone.md\nSubject: s\n\nbody\n"},
			"neither an id on this bus nor a note that exists",
		},
		{
			"a lane nobody owns",
			map[string]string{"from-nobody/x.md": "From: Ada\nTo: Bo\nSubject: s\n\nbody\n"},
			"owns this lane",
		},
		{
			"a receipt naming nothing",
			map[string]string{"from-ada/RECEIPTS": "2026-09-07T00:11:00Z bo-deadbeefcafe\n"},
			"neither an id on this bus nor a note that exists",
		},
		{
			"a receipt with no stamp",
			map[string]string{
				"from-ada/RECEIPTS": "yesterday bo-abcdef012345\n",
				"from-bo/q.md":      "From: Bo\nTo: Ada\nId: bo-abcdef012345\nSubject: s\n\nbody\n",
			},
			"is not a UTC stamp",
		},
		{
			"a receipt line with no target",
			map[string]string{"from-ada/RECEIPTS": "2026-09-07T00:11:00Z\n"},
			"a receipt is",
		},
		{
			"something in a lane that is not a note",
			map[string]string{"from-ada/notes.txt": "hello\n"},
			"and nothing else",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := loadBus(t, writeBus(t, tc.files))
			ps := tab.Check()
			if len(ps) == 0 {
				t.Fatal("check passed a bus it should have failed")
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

// A Re of "new" is the bus's own way of saying this starts a thread, and is not a
// dangling reference.
func TestCheckAcceptsReNew(t *testing.T) {
	t.Parallel()
	tab := loadBus(t, writeBus(t, map[string]string{
		"from-ada/x.md": "From: Ada\nTo: Bo\nRe: new\nSubject: s\n\nbody\n",
	}))
	if ps := tab.Check(); len(ps) != 0 {
		t.Fatalf("Re: new was refused: %+v", ps)
	}
}

// One bad file must not blind the rest of the run: check names every failure it finds.
func TestCheckReportsEveryFailureNotTheFirst(t *testing.T) {
	t.Parallel()
	tab := loadBus(t, writeBus(t, map[string]string{
		"from-ada/a.md": "From: Ada\nnot a header\n\nbody\n",
		"from-ada/b.md": "From: Ada\nTo: Boe\nSubject: s\n\nbody\n",
		"from-ada/c.md": "From: Ada\nTo: Bo\nRe: bo-deadbeefcafe\nSubject: s\n\nbody\n",
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
	t.Parallel()
	files := fixture()
	files["from-bo/2026-09-07T0004Z-to-everyone-333333333333.md"] = `From: Bo
To: Everybody on the bus
Date: Mon Sep  7 00:04:00 UTC 2026
Id: bo-333333333333
Subject: A note to the whole bus

Something everyone needs, said once.
`
	tab := loadBus(t, writeBus(t, files))
	// Ada is a member and has a lane, so the note is in his inbox. Bo wrote it, so
	// it is not in hers: a note in my own lane is never in my inbox, group or not.
	ada := mustParticipant(t, tab.Config, "Ada")
	found := false
	for _, item := range tab.Inbox(ada, 40) {
		if item.Note.Header.ID == "bo-333333333333" {
			found = true
			if item.Address != "to" {
				t.Fatalf("addr = %q, want to", item.Address)
			}
		}
	}
	if !found {
		t.Fatal("a note addressed to a group Ada belongs to is not in Ada's inbox")
	}
	bo := mustParticipant(t, tab.Config, "Bo")
	for _, item := range tab.Inbox(bo, 40) {
		if item.Note.Header.ID == "bo-333333333333" {
			t.Fatal("Bo's own note is in Bo's inbox")
		}
	}
	// And the resolution itself names every member, including the one with no lane, who is
	// addressable and never a sender.
	to, unknown := tab.Config.ResolveList("Everybody on the bus")
	if len(unknown) > 0 || strings.Join(to, "; ") != "Ada; Bo; Dana" {
		t.Fatalf("the group resolved to %v %v", to, unknown)
	}
}

// A file that will not parse is a note somebody wrote, on the bus, that inbox used to
// step over in silence. It is now named, with its reason, on every run.
func TestUnreadableNotesAreNamedAndNotSilent(t *testing.T) {
	t.Parallel()
	files := fixture()
	files["from-bo/2026-09-07T0005Z-prose.md"] = "Ada, the checkpoint is pushed and the suite passed: zero divergence.\n\nMore prose.\n"
	files["from-ada/2026-09-07T0006Z-mine.md"] = "not a header at all\n\nbody\n"
	tab := loadBus(t, writeBus(t, files))
	ada := mustParticipant(t, tab.Config, "Ada")

	bad := tab.Unreadable(ada.Lane)
	if len(bad) != 1 || bad[0].Path != "from-bo/2026-09-07T0005Z-prose.md" {
		t.Fatalf("Unreadable = %v, want the one file outside my own lane", bad)
	}
	if bad[0].Parse == nil || bad[0].Parse.Err == nil {
		t.Fatal("an unreadable note carries no reason")
	}
	// It is NOT in the inbox listing: an unreadable note has no To line, so nothing can
	// honestly say it was addressed to me. It is reported separately, which is the whole
	// point.
	for _, item := range tab.Inbox(ada, 40) {
		if item.Note.Path == bad[0].Path {
			t.Fatal("an unparseable note was reported as an open note addressed to me")
		}
	}
	// check still fails on both, including the one in my own lane.
	if n := len(tab.Check()); n < 2 {
		t.Fatalf("check found %d problems over two unreadable notes", n)
	}
}

// The adoption problem: a bus written by hand for months, checked for the first time.
// Without a tolerance the first run is a wall of red nobody can act on, and the check gets
// turned off -- which is worse than not having it.
func TestLegacyBeforeWarnsOnOldNotesAndStillFailsOnNew(t *testing.T) {
	t.Parallel()
	files := fixture()
	// Old and unreadable; old with a Re that names nothing; new and unreadable.
	files["from-bo/2026-09-01T0001Z-old-prose.md"] = "Ada, this predates the tool entirely.\n\nbody\n"
	files["from-bo/2026-09-01T0002Z-old-dangling.md"] = `From: Bo
To: Ada
Date: Tue Sep  1 00:02:00 UTC 2026
Subject: An answer to a note whose file was renamed

Re lines named filenames once, and a rename orphaned this one.
`
	// The Re has to be on the note itself, not in the body.
	files["from-bo/2026-09-01T0002Z-old-dangling.md"] = strings.Replace(
		files["from-bo/2026-09-01T0002Z-old-dangling.md"],
		"Subject: An answer",
		"Re: from-bo/renamed-away.md\nSubject: An answer", 1)
	files["from-bo/2026-09-08T0001Z-new-prose.md"] = "Ada, this was written after the tool arrived.\n\nbody\n"
	tab := loadBus(t, writeBus(t, files))

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
		"from-bo/2026-09-01T0001Z-old-prose.md",
		"from-bo/2026-09-01T0002Z-old-dangling.md:4",
	} {
		if !warned[where] {
			t.Fatalf("%s was not tolerated; warned=%v failed=%v", where, warned, failed)
		}
	}
	if !failed["from-bo/2026-09-08T0001Z-new-prose.md"] {
		t.Fatalf("a note written after the cutoff was tolerated; failed=%v", failed)
	}
}

// The four header findings a real bus failed 163 times on, with the line
// drawn at its adoption day: a missing Subject, and a To, a From and a Cc naming somebody
// the roster does not hold. Every one of them is a note written by hand before there was a
// roster to check against, so every one of them warns on the old side of the line and
// fails on the new side. The message text is unchanged in both directions -- only whether
// it fails.
func TestTheHeaderFindingsAreInsideTheLegacyTolerance(t *testing.T) {
	t.Parallel()
	const (
		old = "2026-09-01T0009Z"
		new = "2026-09-08T0009Z"
	)
	kinds := []struct {
		name, header, want string
	}{
		{"no subject", "From: Bo\nTo: Ada\nDate: %s\n", "no Subject line, or an empty one"},
		{"To names no one", "From: Bo\nTo: Nobody At All\nDate: %s\nSubject: s\n", `To: "Nobody At All" names no one on this bus`},
		{"From names no one", "From: Nobody At All\nTo: Ada\nDate: %s\nSubject: s\n", `From: "Nobody At All" names no one on this bus`},
		{"Cc names no one", "From: Bo\nTo: Ada\nCc: Nobody At All\nDate: %s\nSubject: s\n", `Cc: "Nobody At All" names no one on this bus`},
	}
	before := at("2026-09-05T00:00:00Z")
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			files := fixture()
			oldPath := "from-bo/" + old + "-" + Slugify(k.name, 40) + ".md"
			newPath := "from-bo/" + new + "-" + Slugify(k.name, 40) + ".md"
			files[oldPath] = fmt.Sprintf(k.header, "Tue Sep  1 00:09:00 UTC 2026") + "\nbody\n"
			files[newPath] = fmt.Sprintf(k.header, "Tue Sep  8 00:09:00 UTC 2026") + "\nbody\n"
			tab := loadBus(t, writeBus(t, files))

			ps := tab.CheckWith(CheckOptions{LegacyBefore: before})
			var oldP, newP *Problem
			for i := range ps {
				switch ps[i].Where {
				case oldPath:
					oldP = &ps[i]
				case newPath:
					newP = &ps[i]
				}
			}
			if oldP == nil || newP == nil {
				t.Fatalf("the planted findings are missing: old=%v new=%v", oldP, newP)
			}
			if !strings.Contains(oldP.Reason, k.want) || !strings.Contains(newP.Reason, k.want) {
				t.Fatalf("the message changed: old=%q new=%q, want both to name %q", oldP.Reason, newP.Reason, k.want)
			}
			if !oldP.Warn {
				t.Fatalf("a note dated before the line still FAILS on %s: %s", k.name, oldP.Reason)
			}
			if newP.Warn {
				t.Fatalf("a note dated after the line was tolerated on %s: %s", k.name, newP.Reason)
			}
			// And with no line at all, both fail.
			for _, p := range tab.Check() {
				if p.Warn {
					t.Fatalf("Check() tolerated %s without being asked to: %s", p.Where, p.Reason)
				}
			}
		})
	}
}

// The tolerance is still narrow where it was narrow. What it forgives is how a note was
// WRITTEN; where a file sits, and whether an id is one, are not things a bus's history
// made unavoidable, so they fail at any date.
func TestTheLegacyToleranceStillFailsOnWhatIsNotAHeader(t *testing.T) {
	t.Parallel()
	files := fixture()
	// A note in the wrong lane, a malformed id, and a stray file -- all dated well before
	// any line anybody would draw.
	files["from-ada/2026-09-01T0004Z-wrong-lane.md"] = `From: Bo
To: Ada
Date: Tue Sep  1 00:04:00 UTC 2026
Subject: A note of Bo's, sitting in Ada's lane

body
`
	files["from-bo/2026-09-01T0005Z-bad-id.md"] = `From: Bo
To: Ada
Date: Tue Sep  1 00:05:00 UTC 2026
Id: bo-NOTHEXATALL
Subject: An id that is not one

body
`
	files["from-bo/notes.txt"] = "a stray file\n"
	tab := loadBus(t, writeBus(t, files))
	failed := map[string]bool{}
	for _, p := range tab.CheckWith(CheckOptions{LegacyBefore: at("2030-01-01T00:00:00Z")}) {
		if !p.Warn {
			failed[p.Where] = true
		}
	}
	for _, where := range []string{
		"from-ada/2026-09-01T0004Z-wrong-lane.md:1",
		"from-bo/2026-09-01T0005Z-bad-id.md:4",
		"from-bo/notes.txt",
	} {
		if !failed[where] {
			t.Fatalf("%s was tolerated as legacy; failed=%v", where, failed)
		}
	}
}

// A note that cannot say WHEN it was written cannot claim to predate anything.
func TestLegacyToleranceNeedsADateItCanRead(t *testing.T) {
	t.Parallel()
	files := fixture()
	files["from-bo/undated-prose.md"] = "Ada, no date line and no minute in the filename.\n\nbody\n"
	tab := loadBus(t, writeBus(t, files))
	for _, p := range tab.CheckWith(CheckOptions{LegacyBefore: at("2030-01-01T00:00:00Z")}) {
		if p.Where == "from-bo/undated-prose.md" && p.Warn {
			t.Fatal("a note with no readable date was tolerated as old")
		}
	}
}

// ...but a note that says its DAY in its filename has said when it was written, whatever
// shape the rest of the name is in. A bus written by hand for months names its notes
// four ways and only one of them is the minute When parses; the other three still begin
// with the day, and a line drawn on a date needs nothing more than that. The line reads
// them; nothing else does.
func TestTheLegacyLineReadsTheDayAtTheFrontOfAFilename(t *testing.T) {
	t.Parallel()
	shapes := []string{
		"2026-09-01T0001Z-the-minute-this-tool-writes.md",
		"2026-09-01T000102Z-the-same-with-seconds.md",
		"2026-09-01T2026-09-01T000102Z-the-stamp-pasted-twice.md",
		"2026-09-01-just-the-day.md",
	}
	files := fixture()
	for _, name := range shapes {
		// No Date line at all, and no Subject either -- the finding under test.
		files["from-bo/"+name] = "From: Bo\nTo: Ada\n\nbody\n"
	}
	// The same note, dated after the line by its filename, still fails.
	files["from-bo/2026-09-08-after-the-line.md"] = "From: Bo\nTo: Ada\n\nbody\n"
	tab := loadBus(t, writeBus(t, files))
	warned, failed := map[string]bool{}, map[string]bool{}
	for _, p := range tab.CheckWith(CheckOptions{LegacyBefore: at("2026-09-05T00:00:00Z")}) {
		if p.Warn {
			warned[p.Where] = true
		} else {
			failed[p.Where] = true
		}
	}
	for _, name := range shapes {
		if !warned["from-bo/"+name] {
			t.Fatalf("%s names its day and was not tolerated; warned=%v failed=%v", name, warned, failed)
		}
	}
	if !failed["from-bo/2026-09-08-after-the-line.md"] {
		t.Fatalf("a note whose filename names a day AFTER the line was tolerated; failed=%v", failed)
	}
	// And the day is read for the line only. What the listing orders by is unchanged: a
	// note whose Date line cannot be read and whose filename is not the minute still has no
	// moment, so no catalogue line and no at= field is invented for it.
	n, ok := tab.NoteByPath("from-bo/2026-09-01-just-the-day.md")
	if !ok {
		t.Fatal("the note is not on the bus")
	}
	if !n.When().IsZero() {
		t.Fatalf("When() now reads a day it did not read before (%v); ordering, INDEX Date and at= would change with it", n.When())
	}
}

// A lane's dotfiles (such as .DS_Store or .gitkeep) are tolerated and ignored:
// they are not notes and not strays.
func TestLaneDotfilesAreToleratedAndIgnored(t *testing.T) {
	t.Parallel()
	m := fixture()
	m["from-bo/.DS_Store"] = "binary junk"
	m["from-bo/.gitkeep"] = ""
	tab := loadBus(t, writeBus(t, m))
	if len(tab.Notes) != 4 {
		t.Fatalf("read %d notes, want 4", len(tab.Notes))
	}
	if ps := tab.Check(); len(ps) != 0 {
		t.Fatalf("dotfiles in lane produced check findings: %+v", ps)
	}
}
