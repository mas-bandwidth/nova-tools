package friends

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

const oneUnit = `{"units":[
  {"id":"u1","title":"retire the child shell","owner":"Emma",
   "needs":["read the thirteen scripts","name the verb for each"],
   "acceptance":["scratch/notes.md holds one line per script","every row is adopt, verb or delete"],
   "branch":"rowan/lane-friends"}
]}`

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFindsTheUnitByID(t *testing.T) {
	ws, err := Load(write(t, "units.json", oneUnit), DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	u, err := ws.Unit("u1")
	if err != nil {
		t.Fatal(err)
	}
	if u.Title != "retire the child shell" || u.Owner != "Emma" || len(u.Needs) != 2 {
		t.Fatalf("unit read back wrong: %+v", u)
	}
	if _, err := ws.Unit("nope"); err == nil {
		t.Fatal("an absent unit must be refused by name")
	}
}

func TestLoadRefusesAFilePastTheByteCeilingWholeRatherThanTruncated(t *testing.T) {
	p := write(t, "units.json", oneUnit)
	if _, err := Load(p, 16); err == nil {
		t.Fatal("a file past --max-bytes must be refused")
	} else if !strings.Contains(err.Error(), "16") {
		t.Fatalf("the refusal must name the ceiling, got %q", err)
	}
}

func TestRenderCarriesTheHouseShape(t *testing.T) {
	ws, err := Load(write(t, "units.json", oneUnit), DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := ws.Unit("u1")
	deadline := time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC)
	note, _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: *u, Deadline: deadline, Branch: "rowan/lane-friends"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"From: Rowan\n",
		"To: Emma\n",
		"Subject: ask work: retire the child shell\n",
		"Unit: u1\n",
		"Needs:\n- read the thirteen scripts\n",
		"Acceptance:\n- scratch/notes.md holds one line per script\n",
		"Deadline: 2026-09-18T20:00:00Z\n",
		"Reply on branch: rowan/lane-friends\n",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not carry %q:\n%s", want, note)
		}
	}
}

func TestRenderRefusesASubjectThatWouldForgeAHeaderLine(t *testing.T) {
	u := Unit{ID: "u1", Title: "one\nTo: somebody-else"}
	if _, _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: u, Deadline: time.Now().UTC(), Branch: "b"}); err == nil {
		t.Fatal("a title with a newline must be refused, never written onto a header line")
	}
}

// Superseded by the dogfood run of 2026-09-18: a unit with no :acceptance is asked,
// with the title standing as the acceptance and a notice saying so. See
// TestRenderWithoutAcceptanceTakesTheTitleAndSaysSo. What stays refused is a unit
// with no TITLE, which would leave both the subject and the acceptance empty.
func TestRenderRefusesAUnitWithNoTitle(t *testing.T) {
	u := Unit{ID: "u1", Needs: []string{"n"}}
	if _, _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: u, Deadline: time.Now().UTC(), Branch: "b"}); err == nil {
		t.Fatal("a unit with no title says nothing in a subject and nothing in an acceptance")
	}
}

// fake is the send seam's stand-in: it records what it was handed and answers an id.
// No test in this package starts a process or reaches the network.
type fake struct {
	notes []string
	id    string
	err   error
}

func (f *fake) Send(note string) (string, error) {
	f.notes = append(f.notes, note)
	return f.id, f.err
}
func (f *fake) Where() string { return "(fake)" }

func TestRecordWritesTheAskBackOntoTheUnit(t *testing.T) {
	p := write(t, "units.json", oneUnit)
	ws, err := Load(p, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	a := Ask{ID: "2026-09-18T1200Z-ask-abcdef", Owner: "Emma", Kind: "work", Unit: "u1",
		Sent: now, Deadline: now.Add(2 * time.Hour), Branch: "rowan/lane-friends", By: "Rowan", Bus: "/bus"}
	if err := ws.Record("u1", a); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, ws); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := back.Unit("u1")
	if len(u.Asks) != 1 || u.Asks[0].ID != a.ID || !u.Asks[0].Deadline.Equal(a.Deadline) {
		t.Fatalf("the ask was not recorded back on the unit: %+v", u.Asks)
	}
}

func TestOpenAsksFlagTheOverdueOnesAndCarryAge(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	ws := &WorkSet{Units: []Unit{{
		ID: "u1", Asks: []Ask{
			{ID: "a1", Owner: "Emma", Kind: "work", Unit: "u1", Sent: now.Add(-30 * time.Minute), Deadline: now.Add(time.Hour)},
			{ID: "a2", Owner: "Stella", Kind: "read", Unit: "u1", Sent: now.Add(-3 * time.Hour), Deadline: now.Add(-time.Hour)},
			{ID: "a3", Owner: "Emma", Kind: "work", Unit: "u1", Sent: now.Add(-time.Hour), Deadline: now.Add(time.Hour), Answered: true},
		},
	}}}
	rows := ws.Open(now)
	if len(rows) != 2 {
		t.Fatalf("an answered ask is not open: got %d rows", len(rows))
	}
	// oldest first: the ask that has been out longest is the first row.
	if rows[0].ID != "a2" || rows[0].Age != 3*time.Hour || !rows[0].Overdue {
		t.Fatalf("row 0 wrong: %+v", rows[0])
	}
	if rows[1].ID != "a1" || rows[1].Age != 30*time.Minute || rows[1].Overdue {
		t.Fatalf("row 1 wrong: %+v", rows[1])
	}
}

func TestSenderSeamIsWhatCarriesTheNote(t *testing.T) {
	f := &fake{id: "2026-09-18T1200Z-ask-abcdef"}
	id, err := f.Send("From: Rowan\nTo: Emma\nSubject: ask work: x\n\nbody\n")
	if err != nil || id != f.id {
		t.Fatalf("the seam must answer the id it was given: %q %v", id, err)
	}
	if len(f.notes) != 1 {
		t.Fatalf("the seam saw %d notes, want 1", len(f.notes))
	}
	var s Sender = f
	if s.Where() == "" {
		t.Fatal("a sender must say where it sends, for the progress line")
	}
}

func TestSendOKLineYieldsTheID(t *testing.T) {
	id, err := parseSendOK("SEND NOTE something\nSEND OK id=2026-09-18T1200Z-ask-abcdef path=from-rowan/x.md commit=deadbeef pushed=true attempts=1 wakes=1\n")
	if err != nil {
		t.Fatal(err)
	}
	if id != "2026-09-18T1200Z-ask-abcdef" {
		t.Fatalf("id = %q", id)
	}
	if _, err := parseSendOK("SEND FAIL x: no\n"); err == nil {
		t.Fatal("a send that did not print SEND OK must be an error, never a silent success")
	}
}

// ---------------------------------------------------------------- the dogfood edges
// A non-author ran ask/asks on a real unit on 2026-09-18. Seven edges came back, and
// each one is a test here before it is a line of code.

func TestLoadReadsTheLispWorkSetACoordinatorActuallyWrites(t *testing.T) {
	ws, err := Load("testdata/work-set.lisp", DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Units) != 3 {
		t.Fatalf("read %d units, want 3", len(ws.Units))
	}
	u, err := ws.Unit("pull:queue")
	if err != nil {
		t.Fatal(err)
	}
	// edge 3: :lane, :owner, :needs and :deadline all survive the read
	if u.Owner != "Stella" {
		t.Errorf("owner = %q", u.Owner)
	}
	if u.Lane != "work" {
		t.Errorf("lane = %q, and a lane that is lost is a unit nobody can schedule", u.Lane)
	}
	if len(u.Needs) != 1 || u.Needs[0] != "promote:main" {
		t.Errorf("needs = %v", u.Needs)
	}
	// the file's stamps are short RFC3339 (no seconds), which is what a person writes
	want := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	if !u.Deadline.Equal(want) {
		t.Errorf("deadline = %v, want %v", u.Deadline, want)
	}
	if !strings.HasPrefix(u.Title, "the ready set in Redis") {
		t.Errorf("title = %q", u.Title)
	}
	// a unit with no :title is still a unit; it is Render that refuses it
	if _, err := ws.Unit("verb:hygiene"); err != nil {
		t.Errorf("a unit with no :title must still be found: %v", err)
	}
}

func TestLoadStillReadsTheJSONForm(t *testing.T) {
	ws, err := Load(write(t, "units.json", oneUnit), DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Units) != 1 {
		t.Fatalf("read %d units, want 1", len(ws.Units))
	}
}

func TestLaneAndDeadlineSurviveTheJSONForm(t *testing.T) {
	p := write(t, "units.json", `{"units":[{"id":"u1","title":"t","owner":"Emma","lane":"work","deadline":"2026-09-18T18:00Z","acceptance":["a"]}]}`)
	ws, err := Load(p, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := ws.Unit("u1")
	if u.Lane != "work" || u.Deadline.IsZero() {
		t.Fatalf("lane and deadline did not survive: %+v", u)
	}
	// and back out again, byte for byte enough to read once more
	if err := Save(p, ws); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := back.Unit("u1"); b.Lane != "work" || !b.Deadline.Equal(u.Deadline.Time) {
		t.Fatalf("lane or deadline lost on the round trip: %+v", b)
	}
}

func TestRenderWithoutAcceptanceTakesTheTitleAndSaysSo(t *testing.T) {
	u := Unit{ID: "pull:queue", Title: "the ready set in Redis", Owner: "Stella", Lane: "work"}
	note, notices, err := Render(AskSpec{From: "Rowan", Owner: "Stella", Kind: "work", Unit: u, Deadline: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatalf("a unit with no :acceptance must be asked, not refused: %v", err)
	}
	if !strings.Contains(note, "Acceptance: as titled\n") {
		t.Errorf("the note must say the acceptance is the title:\n%s", note)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "acceptance") {
		t.Errorf("one notice must say the unit carries no acceptance, got %v", notices)
	}
	// edge 3: the lane travels with the unit
	if !strings.Contains(note, "Lane: work\n") {
		t.Errorf("the note does not carry the lane:\n%s", note)
	}
}

func TestRenderOmitsTheBranchClauseWhenThereIsNoBranch(t *testing.T) {
	u := Unit{ID: "u1", Title: "t", Acceptance: []string{"a"}}
	note, _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: u, Deadline: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "on that branch") || strings.Contains(note, "Reply on branch:") {
		t.Errorf("with no branch there is no branch clause:\n%s", note)
	}
	if !strings.Contains(note, "Reply on the bus") {
		t.Errorf("the note must still say where to reply:\n%s", note)
	}

	withBranch, _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: u, Deadline: time.Now().UTC().Add(time.Hour), Branch: "rowan/x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withBranch, "Reply on branch: rowan/x\n") || !strings.Contains(withBranch, "on that branch") {
		t.Errorf("with a branch the clause is there:\n%s", withBranch)
	}
}

func TestRenderPrintsTheTitleOnceOnly(t *testing.T) {
	u := Unit{ID: "u1", Title: "retire the child shell", Acceptance: []string{"a"}}
	note, _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: u, Deadline: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(note, "retire the child shell"); n != 1 {
		t.Fatalf("the title is printed %d times, want once (the Subject):\n%s", n, note)
	}
}

func TestRenderCcsTheSenderSoABroadcastIncludesSelf(t *testing.T) {
	u := Unit{ID: "u1", Title: "t", Acceptance: []string{"a"}}
	note, _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: u, Deadline: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "Cc: Rowan\n") {
		t.Errorf("with no --cc the sender Ccs itself:\n%s", note)
	}
	named, _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Cc: "Stella", Kind: "work", Unit: u, Deadline: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(named, "Cc: Stella, Rowan\n") {
		t.Errorf("--cc is kept and the sender is added to it:\n%s", named)
	}
	already, _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Cc: "Rowan, Stella", Kind: "work", Unit: u, Deadline: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(already, "Cc: Rowan, Stella\n") {
		t.Errorf("a sender already named is not named twice:\n%s", already)
	}
}

func TestOnBusListsTheAsksTheBusItselfRecords(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	rows, _, err := OnBus("testdata/bus", "Ada", "Bo", now, 0)
	if err != nil {
		t.Fatal(err)
	}
	// three ask notes, one of them answered by a Re from the owner's own lane, and one
	// note that is not an ask at all
	if len(rows) != 2 {
		t.Fatalf("got %d open asks, want 2:\n%+v", len(rows), rows)
	}
	// oldest first
	if rows[0].ID != "ada-bbbbbbbbbbbb" || rows[0].Kind != "read" || rows[0].Unit != "read:worklang" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if !rows[0].Overdue {
		t.Errorf("an ask past the deadline in its own body is overdue: %+v", rows[0])
	}
	if rows[1].ID != "ada-aaaaaaaaaaaa" || rows[1].Age != 30*time.Minute || rows[1].Overdue {
		t.Errorf("row 1 = %+v", rows[1])
	}
	if rows[1].Owner != "Bo" || rows[1].By != "Ada" {
		t.Errorf("row 1 does not carry who asked whom: %+v", rows[1])
	}
}

func TestOnBusRefusesANameTheRosterDoesNotKnow(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	if _, _, err := OnBus("testdata/bus", "Nobody", "Bo", now, 0); err == nil {
		t.Fatal("a sender the roster does not know must be refused by name")
	}
	if _, _, err := OnBus("testdata/bus", "Ada", "Nobody", now, 0); err == nil {
		t.Fatal("an owner the roster does not know must be refused by name")
	}
}

func TestOnBusWithNoOwnerListsEveryAskTheSenderHasOut(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	rows, _, err := OnBus("testdata/bus", "Ada", "", now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

// ---- the sentence that says why the ask is yours ----

func TestRenderSaysTheUnitNamesYouOnlyWhenItDoes(t *testing.T) {
	owned := Unit{ID: "u1", Title: "the ready set", Owner: "Emma"}
	note, notices, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: owned,
		Deadline: time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "the unit names you as its owner") {
		t.Errorf("a unit that names its owner says so:\n%s", note)
	}
	if strings.Contains(note, "assigned by") {
		t.Errorf("a unit that names its owner is not also assigned:\n%s", note)
	}
	for _, n := range notices {
		if strings.Contains(n, "owner") {
			t.Errorf("nothing to notice about an owner the unit names: %q", n)
		}
	}
}

func TestRenderSaysAssignedWhenTheUnitNamesNoOwner(t *testing.T) {
	// The unit carries no :owner -- which is every unit of the real work set -- and the
	// owner came from --owner. Saying "the unit names you as its owner" was a claim the
	// file did not make, to the person least able to check it.
	bare := Unit{ID: "u1", Title: "the ready set"}
	note, notices, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: bare,
		Deadline: time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "the unit names you as its owner") {
		t.Errorf("the unit names no owner; the note must not say it does:\n%s", note)
	}
	if !strings.Contains(note, "assigned by Rowan") {
		t.Errorf("a unit with no owner is assigned by its sender:\n%s", note)
	}
	if !hasNotice(notices, "owner") {
		t.Errorf("a unit with no owner is worth one notice: %v", notices)
	}
}

func TestRenderSaysAssignedWhenTheUnitNamesSomebodyElse(t *testing.T) {
	theirs := Unit{ID: "u1", Title: "the ready set", Owner: "Stella"}
	note, notices, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: theirs,
		Deadline: time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "the unit names you as its owner") {
		t.Errorf("the unit names Stella, not Emma:\n%s", note)
	}
	if !strings.Contains(note, "assigned by Rowan") {
		t.Errorf("an owner the unit does not name is assigned:\n%s", note)
	}
	if !hasNotice(notices, "Stella") {
		t.Errorf("the notice must name the owner the unit does carry: %v", notices)
	}
}

func hasNotice(notices []string, want string) bool {
	for _, n := range notices {
		if strings.Contains(n, want) {
			return true
		}
	}
	return false
}

// ---- the bus read is bounded by files, newest first, and says when it was ----

func TestOnBusReadsEveryNoteWhenItIsNotBounded(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	rows, bounded, err := OnBus("testdata/bus", "Ada", "Bo", now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d open asks, want 2:\n%+v", len(rows), rows)
	}
	if len(bounded) != 0 {
		t.Fatalf("an unbounded read reports no bound: %+v", bounded)
	}
}

func TestOnBusBoundsTheNewestNotesAndNamesTheBound(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	// The sender's lane holds four notes; read one and it is the NEWEST, which is the ask
	// at 11:30. Oldest-first was the bug: it read the note nobody is waiting on.
	rows, bounded, err := OnBus("testdata/bus", "Ada", "Bo", now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "ada-aaaaaaaaaaaa" {
		t.Fatalf("a bounded read takes the NEWEST notes: %+v", rows)
	}
	var mine *Bounded
	for i := range bounded {
		if bounded[i].Lane == "from-ada" {
			mine = &bounded[i]
		}
	}
	if mine == nil || mine.Notes != 4 || mine.Read != 1 {
		t.Fatalf("the bound must name the lane, what it holds and what was read: %+v", bounded)
	}
}

// ---- one reader for the work set ----

// TestLoadReadsTheLispFormThroughWorklang holds the rule that there is ONE reader of
// the (work-set ...) form. `nova-work set check` and `nova-work ask` had a reader
// each, with their own key lists, their own idea of what a unit is and their own
// stamp parser; a unit either of them read and the other did not was a unit one verb
// could check and the other could not ask about. The fold is checked HERE, against
// worklang's own reader, on the file a coordinator actually writes.
func TestLoadReadsTheLispFormThroughWorklang(t *testing.T) {
	const path = "testdata/work-set.lisp"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	limits := worklang.DefaultLimits()
	limits.MaxBytes = int(DefaultMaxBytes)
	want, err := worklang.ParseWorkSet(path, raw, limits)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Units) != len(want.Units) {
		t.Fatalf("the two readers disagree on how many units there are: %d vs %d", len(got.Units), len(want.Units))
	}
	for i := range want.Units {
		w, g := want.Units[i], got.Units[i]
		if g.ID != w.ID || g.Title != w.Title || g.Owner != w.Owner || g.Lane != w.Lane {
			t.Errorf("unit %d differs: %+v vs %+v", i, g, w)
		}
		if w.Deadline == "" {
			if !g.Deadline.IsZero() {
				t.Errorf("unit %q has no deadline in the file but one after the read", w.ID)
			}
			continue
		}
		at, err := worklang.ParseStamp(w.Deadline)
		if err != nil {
			t.Fatal(err)
		}
		if !g.Deadline.Equal(at) {
			t.Errorf("unit %q deadline = %s, want %s", w.ID, g.Deadline, at)
		}
	}
}

func TestLoadCarriesTheBranchAndAcceptanceTheGrammarWrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "set.lisp")
	if err := os.WriteFile(p, []byte(`(work-set "s"
  :units ((unit "u1" :owner "Emma" :lane "work" :branch "rowan/lane-friends"
                :acceptance ("one line per script") :title "retire the child shell")))`), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := Load(p, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Units) != 1 {
		t.Fatalf("got %d units", len(ws.Units))
	}
	u := ws.Units[0]
	if u.Branch != "rowan/lane-friends" {
		t.Errorf("the :branch the grammar writes must survive the one reader: %+v", u)
	}
	if len(u.Acceptance) != 1 || u.Acceptance[0] != "one line per script" {
		t.Errorf("the :acceptance the grammar writes must survive the one reader: %+v", u)
	}
}
