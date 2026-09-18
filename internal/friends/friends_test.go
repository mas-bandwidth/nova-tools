package friends

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	note, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: *u, Deadline: deadline, Branch: "rowan/lane-friends"})
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
	if _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: u, Deadline: time.Now().UTC(), Branch: "b"}); err == nil {
		t.Fatal("a title with a newline must be refused, never written onto a header line")
	}
}

func TestRenderRefusesAUnitWithNoAcceptance(t *testing.T) {
	u := Unit{ID: "u1", Title: "a unit", Needs: []string{"n"}}
	if _, err := Render(AskSpec{From: "Rowan", Owner: "Emma", Kind: "work", Unit: u, Deadline: time.Now().UTC(), Branch: "b"}); err == nil {
		t.Fatal("an ask with no acceptance is a unit nobody can finish; it must be refused")
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
