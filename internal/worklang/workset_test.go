package worklang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures are cut from the real set rather than invented, so what this package
// proves it reads is what a coordinator actually writes.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func parseFixture(t *testing.T, name string) *WorkSet {
	t.Helper()
	ws, err := ParseWorkSet(name, readFixture(t, name), DefaultLimits())
	if err != nil {
		t.Fatalf("ParseWorkSet(%s): %v", name, err)
	}
	return ws
}

// A work set in the real shape reads: the units come back in written order, with
// the keys this reader knows read and the ones it does not KEPT rather than
// refused. `plan check` could not read a byte of this file, which is the gap.
func TestParseWorkSetReadsTheRealShape(t *testing.T) {
	ws := parseFixture(t, "pitstop-cut.lisp")
	if ws.ID != "pitstop-cut" {
		t.Errorf("work set id = %q, want pitstop-cut", ws.ID)
	}
	if !strings.HasPrefix(ws.Title, "Coordination tools") {
		t.Errorf("work set title = %q", ws.Title)
	}
	if len(ws.Units) != 14 {
		t.Fatalf("read %d units, want the fixture's 14", len(ws.Units))
	}
	if ws.Units[0].ID != "verb:hygiene" {
		t.Errorf("first unit = %q, want verb:hygiene (written order)", ws.Units[0].ID)
	}
	byID := map[string]Unit{}
	for _, u := range ws.Units {
		byID[u.ID] = u
	}
	pull, ok := byID["pull:queue"]
	if !ok {
		t.Fatal("pull:queue was not read")
	}
	if pull.Owner != "Stella" || pull.Lane != "work" {
		t.Errorf("pull:queue owner/lane = %q/%q, want Stella/work", pull.Owner, pull.Lane)
	}
	if pull.Deadline != "2026-09-18T18:00Z" {
		t.Errorf("pull:queue deadline = %q, want the text as written", pull.Deadline)
	}
	if got := strings.Join(pull.Needs, ","); got != "promote:main" {
		t.Errorf("pull:queue needs = %q, want promote:main", got)
	}
	// :status is written both as a string and as a keyword in the real file, and
	// both say the same thing.
	if byID["repair:1072"].Status != "closed" {
		t.Errorf(`repair:1072 :status = %q, want closed`, byID["repair:1072"].Status)
	}
	if byID["docs:readme"].Status != "review" {
		t.Errorf(`docs:readme :status = %q, want review (a keyword value)`, byID["docs:readme"].Status)
	}
	// The keys this reader does not know are kept, not dropped: a later slice reads
	// what this one ignores without a second reader.
	if got := strings.Join(byID["verb:hygiene"].Keys, ","); !strings.Contains(got, "budget") || !strings.Contains(got, "affinity") {
		t.Errorf("verb:hygiene keys = %q, want the unknown ones kept", got)
	}
}

// The real set's own two absent needs, found by reading the real file: nothing
// defines repair:spec-1208-1209 or merge:simulate. Both are findings, not refusals
// -- the file was read whole and it is its content that is wrong.
func TestCheckFindsTheRealSetsAbsentNeeds(t *testing.T) {
	ws := parseFixture(t, "pitstop-cut.lisp")
	findings, counts := ws.Check(Options{})
	var absent []string
	for _, f := range findings {
		if f.Rule == "NEEDS" {
			absent = append(absent, f.Unit+" "+f.Detail())
		}
	}
	want := []string{
		"stack:redis-live need=repair:spec-1208-1209",
		"batch:verb need=merge:simulate",
	}
	if strings.Join(absent, "|") != strings.Join(want, "|") {
		t.Errorf("absent needs = %q, want %q", absent, want)
	}
	if counts.Units != 14 {
		t.Errorf("units = %d, want 14", counts.Units)
	}
	if counts.Units != counts.Ready+counts.Blocked+counts.Done {
		t.Errorf("units=%d does not close: ready=%d blocked=%d done=%d",
			counts.Units, counts.Ready, counts.Blocked, counts.Done)
	}
	// Four of the cut's units name an owner; those are the ones the machinery
	// routes to a friend rather than cutting as a card.
	if counts.Owned != 5 {
		t.Errorf("owned = %d, want 5", counts.Owned)
	}
}

// Every rule runs over every unit in ONE pass. A checker that stopped at the first
// finding would cost the caller one round trip per defect.
func TestCheckReportsEveryRuleInOnePass(t *testing.T) {
	ws := parseFixture(t, "set-defects.lisp")
	findings, _ := ws.Check(Options{
		Minds:     map[string]bool{"emma": true},
		MindsFile: "minds.json",
		Lanes:     map[string]bool{"work": true},
		LanesFile: "lanes.tsv",
	})
	got := map[string][]string{}
	for _, f := range findings {
		got[f.Rule] = append(got[f.Rule], f.Unit+" "+f.Detail())
	}
	for _, rule := range []string{"NO-ID", "DUPLICATE", "NEEDS", "CYCLE", "OWNER", "LANE", "DEADLINE"} {
		if len(got[rule]) == 0 {
			t.Errorf("rule %s found nothing; the fixture has one of each", rule)
		}
	}
	// The three cycles: a<->b, self->self, and x->y->z->x, each named once and each
	// starting at its smallest member so one loop has one spelling.
	cycles := strings.Join(got["CYCLE"], "|")
	for _, want := range []string{"cycle=a->b->a", "cycle=self->self", "cycle=x->y->z->x"} {
		if !strings.Contains(cycles, want) {
			t.Errorf("cycles %q do not name %q", cycles, want)
		}
	}
	if n := len(got["DUPLICATE"]); n != 1 {
		t.Errorf("duplicate findings = %d, want 1 (the second id, naming the first)", n)
	}
	if n := len(got["NO-ID"]); n != 2 {
		t.Errorf("no-id findings = %d, want 2 (the empty id and the member that is not a unit)", n)
	}
	if !strings.Contains(strings.Join(got["OWNER"], "|"), "owner=Nobody") {
		t.Errorf("owner findings = %q, want the unknown owner named", got["OWNER"])
	}
	if !strings.Contains(strings.Join(got["DEADLINE"], "|"), "next tuesday") {
		t.Errorf("deadline findings = %q, want the unreadable deadline named", got["DEADLINE"])
	}
}

// The owner match folds case, because a work set writes a friend's name the way a
// person does and a registry writes a mind's the way a machine does. Nothing else
// is guessed at: a name neither spelling holds is a finding.
func TestOwnerMatchFoldsCase(t *testing.T) {
	ws := parseFixture(t, "pitstop-cut.lisp")
	findings, _ := ws.Check(Options{
		Minds:     map[string]bool{"emma": true, "stella": true},
		MindsFile: "minds.json",
	})
	for _, f := range findings {
		if f.Rule == "OWNER" {
			t.Errorf("Emma and Stella are in the registry, yet %s %s was reported", f.Unit, f.Detail())
		}
	}
}

// Without --minds and without --lanes those two rules are OFF rather than run
// against a guessed file: there is no default registry and no discovery.
func TestNoRegistryMeansNoOwnerOrLaneRule(t *testing.T) {
	ws := parseFixture(t, "set-defects.lisp")
	findings, _ := ws.Check(Options{})
	for _, f := range findings {
		if f.Rule == "OWNER" || f.Rule == "LANE" {
			t.Errorf("with no registry, %s %s was still reported", f.Rule, f.Unit)
		}
	}
}

// The mechanical ready set: a unit is ready when it is not done and every need is
// done. The cut's chain is land:1260 -> land:1261 -> promote:main -> batch:land,
// so settling one need moves exactly one unit into the set.
func TestReadySetIsMechanical(t *testing.T) {
	ws := parseFixture(t, "pitstop-cut.lisp")
	ids := func(us []Unit) string {
		var out []string
		for _, u := range us {
			out = append(out, u.ID)
		}
		return strings.Join(out, ",")
	}
	// repair:1072 carries :status "closed", so the document itself says it is done
	// and it is never in the ready set.
	before := ids(ws.Ready(nil))
	if strings.Contains(before, "repair:1072") {
		t.Errorf(`ready set %q holds a unit whose :status is "closed"`, before)
	}
	if strings.Contains(before, "land:1261") {
		t.Errorf("ready set %q holds land:1261, which needs land:1260", before)
	}
	if !strings.Contains(before, "land:1260") {
		t.Errorf("ready set %q is missing land:1260, which needs nothing", before)
	}
	// --done settles land:1260, and exactly land:1261 joins the set.
	after := ids(ws.Ready(map[string]bool{"land:1260": true}))
	if !strings.Contains(after, "land:1261") {
		t.Errorf("with land:1260 done, ready = %q, want land:1261 in it", after)
	}
	if strings.Contains(after, "land:1260") {
		t.Errorf("a unit named by --done is still in the ready set: %q", after)
	}
	// A unit whose need does not exist is never ready: the absent need is unmet,
	// which is a second reason (beside rule 2) it is never handed to anyone.
	if strings.Contains(before, "stack:redis-live") {
		t.Errorf("ready set %q holds a unit whose need no unit defines", before)
	}
	if got := ws.Blocked(nil)["land:1261"]; got != "land:1260" {
		t.Errorf("land:1261 blocked by %q, want land:1260", got)
	}
}

// Both RFC3339 spellings parse: a coordinator writes the minute-precision one, and
// a reader that took only the full one refused a real unit.
func TestParseStampReadsBothSpellings(t *testing.T) {
	for _, s := range []string{"2026-09-19T12:00:00Z", "2026-09-19T12:00Z", "2026-09-19T12:00:00+00:00"} {
		at, err := ParseStamp(s)
		if err != nil {
			t.Errorf("ParseStamp(%q): %v", s, err)
			continue
		}
		if at.Format("2006-01-02T15:04Z") != "2026-09-19T12:00Z" {
			t.Errorf("ParseStamp(%q) = %s, want 2026-09-19T12:00Z", s, at)
		}
	}
	if _, err := ParseStamp("next tuesday"); err == nil {
		t.Error("an unreadable stamp parsed instead of naming what is accepted")
	} else if !strings.Contains(err.Error(), "2026-09-19T12:00Z") {
		t.Errorf("the refusal %q does not say what it accepts", err)
	}
}

// A refusal is a file this reader could not read AT ALL, and it is exit 2 -- never
// blurred with a finding, which is a file it read whole whose content is wrong.
func TestParseWorkSetRefusals(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"a plan is not a work set", `(:plan :version 1 (:node :id "n" :kind docs))`, "not a work set"},
		{"no units", `(work-set "x" :title "t")`, "carries no :units"},
		{"units is not a list", `(work-set "x" :units "u1")`, ":units must be a list"},
		{"a dispatch macro is still refused", "(work-set \"x\" :units ((unit \"a\" :title #.(evil))))", "dispatch macro"},
		{"the byte bound still holds", `(work-set "x" :units ())`, "--max-bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			limits := DefaultLimits()
			if strings.Contains(c.want, "max-bytes") {
				limits.MaxBytes = 4
			}
			_, err := ParseWorkSet("f.lisp", []byte(c.src), limits)
			if err == nil {
				t.Fatalf("read a file that should be refused: %q", c.src)
			}
			ref, ok := err.(*Refusal)
			if !ok {
				t.Fatalf("error %v is not a *Refusal; a refusal is always exit 2", err)
			}
			if ref.ExitCode() != 2 {
				t.Errorf("refusal exit = %d, want 2", ref.ExitCode())
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("refusal %q does not name %q", err, c.want)
			}
		})
	}
}

// One loop has one spelling however the walk enters it, so a cycle is never
// reported twice under two names.
func TestFindCyclesNamesEachLoopOnce(t *testing.T) {
	needs := map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}}
	cycles := FindCycles([]string{"a", "b", "c"}, needs)
	if len(cycles) != 1 {
		t.Fatalf("found %d cycles, want 1: %v", len(cycles), cycles)
	}
	if got := strings.Join(cycles[0], " -> "); got != "a -> b -> c -> a" {
		t.Errorf("cycle = %q, want a -> b -> c -> a", got)
	}
	if got := FindCycles([]string{"a", "b"}, map[string][]string{"a": {"b"}}); len(got) != 0 {
		t.Errorf("an acyclic graph reported %v", got)
	}
}

// TestReadUnitCarriesBranchAndAcceptance holds the two keys the ask side needs. They
// are read HERE because this is the one reader of the work-set form: a key only one
// caller reads is the second reader growing back.
func TestReadUnitCarriesBranchAndAcceptance(t *testing.T) {
	src := []byte(`(work-set "s" :units ((unit "u1" :branch "rowan/lane-friends"
	                                            :acceptance ("a line in notes.md" "a test")
	                                            :title "retire the child shell")))`)
	ws, err := ParseWorkSet("set.lisp", src, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Units) != 1 {
		t.Fatalf("got %d units", len(ws.Units))
	}
	u := ws.Units[0]
	if u.Branch != "rowan/lane-friends" {
		t.Errorf("Branch = %q", u.Branch)
	}
	if len(u.Acceptance) != 2 || u.Acceptance[0] != "a line in notes.md" || u.Acceptance[1] != "a test" {
		t.Errorf("Acceptance = %#v", u.Acceptance)
	}
	if u.Keys[0] != "branch" || u.Keys[1] != "acceptance" {
		t.Errorf("every key is still recorded in written order: %#v", u.Keys)
	}
}
