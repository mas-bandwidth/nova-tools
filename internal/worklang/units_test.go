package worklang_test

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// The amendment's reader contract (docs/SPEC-WORKLANG.md, "Amendment 1"), seen
// red first. This slice is PARSE ONLY: the reader accepts the new keys, checks
// their shape, and refuses what it cannot represent. Nothing here schedules,
// admits, serialises or leases -- that is the kernel's side and it is Stella's
// and Emma's to write.
func TestWorklangWorkSet(t *testing.T) {
	// A1 worklang-reads-the-real-work-set: the (work-set ... :units (...)) form
	// a coordinator writes today is a plan this reader reads, with every unit,
	// its id and its fields. Before the amendment ParsePlan refused it outright
	// ("the top form must be (:plan ...)"), so the real file was data no tool
	// could open.
	t.Run("worklang-reads-the-real-work-set", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-shape.lisp")
		if ws.ID != "pitstop-2026-09-17" {
			t.Errorf("work-set id = %q, want pitstop-2026-09-17", ws.ID)
		}
		if len(ws.Units) != 20 {
			t.Fatalf("units = %d, want 20", len(ws.Units))
		}
		// The template inside tests:from-findings' :derive is a unit form the
		// expander will mint later; it is not a unit of this set today.
		if _, ok := ws.Unit("tests:{it}"); ok {
			t.Error("a :derive template was read as a unit of the set")
		}
		if _, ok := ws.Fields["done-when"]; !ok {
			t.Error(":done-when was dropped, not kept")
		}
		// Every key the real 79-unit set uses, at least once in the fixture: a
		// key the reader does not know is kept, never dropped and never guessed.
		for _, key := range []string{
			"needs", "pr", "prs", "card", "cards", "spec", "replaces", "findings",
			"note", "owner", "status", "evidence", "sprint", "lane", "deadline",
			"derive", "budget", "affinity", "title", "inputs",
		} {
			if !unitKeySeen(ws, key) {
				t.Errorf("fixture lost the real set's key :%s", key)
			}
		}
	})

	// A2 worklang-unit-id-is-stable-and-required: a unit's id is the second
	// element of its form, a string, minted once. A unit with no id, an empty
	// id, or an id another unit already carries is refused naming the id.
	t.Run("worklang-unit-id-is-stable-and-required", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-shape.lisp")
		if got := ws.Units[0].ID; got != "verb:hygiene" {
			t.Errorf("first unit id = %q, want verb:hygiene", got)
		}
		// A unit with no id, an empty id, or an id another unit already carries is
		// a FINDING, not a refusal. This file's split (workset.go's header) puts a
		// file the reader could not READ on the refusal side and a file it read
		// whose CONTENT is wrong on the finding side, and a set with two "dup"
		// units is a set that was read: `set check` prints every such defect in one
		// pass rather than stopping at the first, which is the whole reason Check
		// collects. The rule is unchanged -- an id is minted once and never reused
		// -- and it is reported for every offender rather than for the first.
		findsRule(t, `(work-set "w" :units ((unit :needs ())))`, "NO-ID", "")
		findsRule(t, `(work-set "w" :units ((unit "" :needs ())))`, "NO-ID", "")
		findsRule(t, `(work-set "w" :units ((unit "a") (unit "a")))`, "DUPLICATE", "a")
	})

	// A3 worklang-attempt-without-a-termination-proof-is-uncertain: an attempt
	// is a record, not a counter. Every attempt carries its rung, its owner and
	// its outcome, and an outcome that is not :uncertain must carry the
	// termination proof that lets its resources be reused.
	t.Run("worklang-attempt-without-a-termination-proof-is-uncertain", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		u := mustUnit(t, ws, "verb:capacity")
		att := u.Attempts()
		if len(att) != 1 {
			t.Fatalf("attempts = %d, want 1", len(att))
		}
		if att[0].Rung != "flash" || att[0].Outcome != "green" || !att[0].HasProof {
			t.Errorf("attempt = %+v, want rung=flash outcome=green proof", att[0])
		}
		// An outcome with no proof is the whole point of the rule: refused, and
		// the refusal says the word the author must write instead.
		refusesWith(t,
			`(work-set "w" :units ((unit "a" :attempts ((:n 1 :rung "flash" :outcome :green)))))`,
			"uncertain")
		// An unknown outcome is refused naming the value: the outcome set is
		// closed, and a spelling is not a new state.
		refusesWith(t,
			`(work-set "w" :units ((unit "a" :attempts ((:n 1 :rung "flash" :outcome :maybe)))))`,
			"maybe")
	})

	// A4 worklang-uncertain-is-a-state: uncertain is a state of its own, neither
	// open nor failed. Stella's lease rule is the reason: an expiry is UNKNOWN
	// until termination is proved, so the state must be writable and readable.
	t.Run("worklang-uncertain-is-a-state", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		if got := mustUnit(t, ws, "verb:fill").State(); got != "uncertain" {
			t.Errorf("state = %q, want uncertain", got)
		}
		if got := mustUnit(t, ws, "verb:capacity").State(); got != "closed" {
			t.Errorf("state = %q, want closed", got)
		}
		for _, state := range worklang.KnownStates() {
			src := `(work-set "w" :units ((unit "a" :state :` + state + `)))`
			if _, err := worklang.ParseWorkSet("w.work", []byte(src), worklang.DefaultLimits()); err != nil {
				t.Errorf("state %q refused: %v", state, err)
			}
		}
		refusesWith(t, `(work-set "w" :units ((unit "a" :state :nearly)))`, "nearly")
	})

	// A5 worklang-resources-are-a-vector-not-a-slot: a unit asks for what it
	// actually consumes -- cpu, memory, disk, network, gpu and named scarce
	// classes -- not for one opaque slot (ideas #783, Patrick's read).
	t.Run("worklang-resources-are-a-vector-not-a-slot", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		res := mustUnit(t, ws, "verb:hygiene").Resources()
		if got := res["cpu"]; got != 2 {
			t.Errorf("cpu = %d, want 2", got)
		}
		if got := res["memory-gb"]; got != 4 {
			t.Errorf("memory-gb = %d, want 4", got)
		}
		if got := res["disk-gb"]; got != 10 {
			t.Errorf("disk-gb = %d, want 10", got)
		}
		// A named scarce class is a dimension of the same vector, so the day the
		// darwin leg is the clock the request says so.
		fill := mustUnit(t, ws, "verb:fill").Resources()
		if _, ok := fill["class:darwin-runner"]; !ok {
			t.Errorf("a named scarce class was dropped from the vector: %v", fill)
		}
		// The vector's shape is checked here and nowhere else: an entry that is
		// not a keyword-headed list cannot be an admission request.
		refusesWith(t, `(work-set "w" :units ((unit "a" :resources ("cpu"))))`, ":resources")
		refusesWith(t, `(work-set "w" :units ((unit "a" :resources ((:cpu "two")))))`, ":cpu")
		refusesWith(t, `(work-set "w" :units ((unit "a" :resources ((:class "darwin-runner")))))`, ":n")
	})

	// A6 worklang-lane-is-a-resource-of-capacity-one: a lane is one entry of
	// that vector -- a resource of capacity 1 over the area of the tree
	// queue/control/lanes.tsv names. The reader carries it; capacity 1 and the
	// lanes file are the scheduler's to enforce.
	t.Run("worklang-lane-is-a-resource-of-capacity-one", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		u := mustUnit(t, ws, "verb:hygiene")
		if got := u.Lane; got != "pulse" {
			t.Errorf("lane = %q, want pulse", got)
		}
		if got := u.Resources()["lane"]; got != 1 {
			t.Errorf("a lane is a resource of capacity 1; got %d", got)
		}
		// The set as written today carries the plain :lane key, and it must keep
		// meaning the same resource.
		before := mustReadWorkSet(t, "testdata/pitstop-shape.lisp")
		if got := mustUnit(t, before, "lanes:spec").Lane; got != "docs" {
			t.Errorf("the plain :lane key stopped naming the lane: %q", got)
		}
		// Two lanes in one vector is a refusal: a unit sits in exactly one area
		// of the tree, so two capacity-1 lanes is a double reservation.
		refusesWith(t,
			`(work-set "w" :units ((unit "a" :resources ((:lane "docs") (:lane "work")))))`,
			":lane")
		// The old spelling and the new one must agree: one area of the tree,
		// named once, or the unit holds two capacity-1 reservations.
		refusesWith(t, `(work-set "w" :units ((unit "a" :lane "docs" :resources ((:lane "work")))))`, "disagree")
	})

	// A7 worklang-writes-are-paths-under-the-repo: :writes is the closed list of
	// paths a unit edits. The reader holds them as strings; two units whose
	// writes intersect serialising even across lanes is the scheduler's rule,
	// and Writes() is the set it reads.
	t.Run("worklang-writes-are-paths-under-the-repo", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		got := mustUnit(t, ws, "lanes:spec").Writes()
		want := []string{"docs/SPEC-WORKLANG.md", "docs/SPEC-JOBS.md"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("writes = %v, want %v", got, want)
		}
		refusesWith(t, `(work-set "w" :units ((unit "a" :writes (1 2))))`, ":writes")
		refusesWith(t, `(work-set "w" :units ((unit "a" :writes ("/etc/passwd"))))`, "absolute")
	})

	// A10 worklang-tools-name-a-verb-and-a-version: :tools is which verbs a unit
	// needs installed at what version, with an optional semantic tool key that
	// invalidates the unit's outputs when it moves.
	t.Run("worklang-tools-name-a-verb-and-a-version", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		tools := mustUnit(t, ws, "lanes:spec").Tools()
		if len(tools) != 1 || tools[0].Verb != "nova-work" || tools[0].At != "0.4.0" {
			t.Fatalf("tools = %+v, want one nova-work at 0.4.0", tools)
		}
		if tools[0].Key != "worklang-reader-2026-09-18" {
			t.Errorf("tool key = %q, want worklang-reader-2026-09-18", tools[0].Key)
		}
		refusesWith(t, `(work-set "w" :units ((unit "a" :tools ((:nova-work)))))`, ":at")
	})

	// A11 worklang-a-collection-names-its-members-after-the-run: an output
	// collection is named before the run and its members only after it, so
	// :members :unknown-before-run is the value the grammar admits.
	t.Run("worklang-a-collection-names-its-members-after-the-run", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		cols := mustUnit(t, ws, "verb:hygiene").Collections()
		if len(cols) != 1 || cols[0].Name != "receipts" || cols[0].Under != "out/receipts" {
			t.Fatalf("collections = %+v, want one receipts under out/receipts", cols)
		}
		if !cols[0].MembersUnknown {
			t.Error("a collection's members are unknown before the run; the flag was lost")
		}
		refusesWith(t, `(work-set "w" :units ((unit "a" :collects ((:under "out/")))))`, ":name")
	})

	// A12 worklang-warm-state-is-retained-apart-from-active: warm state is
	// accounted apart from the resources a unit is actively holding, so a kept
	// worktree or a loaded compiler is never charged as running capacity.
	t.Run("worklang-warm-state-is-retained-apart-from-active", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		warm := mustUnit(t, ws, "verb:hygiene").Warm()
		if len(warm.Retained) != 1 || len(warm.Active) != 1 {
			t.Fatalf("warm = %+v, want one retained and one active entry", warm)
		}
		refusesWith(t, `(work-set "w" :units ((unit "a" :warm ((:cpu 1)))))`, ":retained")
	})

	// A13 worklang-owner-is-a-mind: an owner is a mind -- a friend, a child rung
	// or a swarm -- in one spelling, so `nova-work ask` and the pull worker read
	// the same form. The real set's "Emma" and "all" keep working.
	t.Run("worklang-owner-is-a-mind", func(t *testing.T) {
		ws := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		for id, want := range map[string]string{
			"verb:hygiene":  "swarm:flash",
			"verb:fill":     "Emma",
			"lanes:spec":    "child:opus",
			"read:worklang": "all",
		} {
			if got := mustUnit(t, ws, id).Owner; got != want {
				t.Errorf("%s owner = %q, want %q", id, got, want)
			}
		}
		refusesWith(t, `(work-set "w" :units ((unit "a" :owner 7)))`, ":owner")
	})

	// A14 worklang-acceptance-is-read-and-the-real-set-has-none: the amendment's
	// finding about the file itself. Every unit of the amended fixture names its
	// evidence; not one unit of the real set as it stands does, and the reader
	// must be able to say so for every unit rather than guess a finish line.
	t.Run("worklang-acceptance-is-read-and-the-real-set-has-none", func(t *testing.T) {
		before := mustReadWorkSet(t, "testdata/pitstop-shape.lisp")
		for _, u := range before.Units {
			if len(u.Acceptance) != 0 || len(u.Criteria()) != 0 {
				t.Errorf("%s: the pre-amendment set is not supposed to name acceptance", u.ID)
			}
		}
		if n := len(before.WithoutAcceptance()); n != len(before.Units) {
			t.Errorf("WithoutAcceptance = %d, want all %d units", n, len(before.Units))
		}

		after := mustReadWorkSet(t, "testdata/pitstop-amended.lisp")
		acc := mustUnit(t, after, "verb:hygiene").Criteria()
		if len(acc) != 1 || acc[0].ID != "a1" || acc[0].Kind != "test" || acc[0].Predicate != "passes" {
			t.Fatalf("acceptance = %+v, want one (:id a1 :kind test :predicate passes)", acc)
		}
		if acc[0].Subject != "test:internal/pulse@HEAD" {
			t.Errorf("subject = %q, want test:internal/pulse@HEAD", acc[0].Subject)
		}
		if n := len(after.WithoutAcceptance()); n != 0 {
			t.Errorf("the amended fixture left %d units without acceptance", n)
		}
		refusesWith(t,
			`(work-set "w" :units ((unit "a" :acceptance ((:id "a1" :kind :test :subject "s" :predicate :hopes)))))`,
			"hopes")
	})

	// The new keys are additive on a :node too, so one grammar serves the plan
	// form and the work-set form: a node carrying them keeps them in Fields, not
	// in Unknown.
	t.Run("worklang-node-accepts-the-amendment-keys", func(t *testing.T) {
		src := `(:plan :version 1 (:node :id "n1" :kind docs :lane "docs" :writes ("docs/x.md")` +
			` :resources ((:lane "docs")) :tools ((:nova-work :at "0.4.0")) :owner "Stella"` +
			` :state :uncertain :warm (:retained () :active ())` +
			` :collects ((:name "out" :under "out/" :members :unknown-before-run))` +
			` :attempts ((:n 1 :rung "flash" :outcome :uncertain))))`
		plan, err := worklang.ParsePlan("work.work", []byte(src), worklang.DefaultLimits())
		if err != nil {
			t.Fatalf("a node carrying the amendment keys was refused: %v", err)
		}
		if len(plan.Nodes) != 1 {
			t.Fatalf("nodes = %d, want 1", len(plan.Nodes))
		}
		for _, key := range []string{"lane", "writes", "resources", "tools", "owner", "state", "warm", "collects", "attempts"} {
			if _, ok := plan.Nodes[0].Fields[key]; !ok {
				t.Errorf(":%s landed in Unknown, not Fields", key)
			}
		}
	})
}

func mustReadWorkSet(t *testing.T, path string) *worklang.WorkSet {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	ws, err := worklang.ParseWorkSet(path, data, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("%s refused: %v", path, err)
	}
	return ws
}

func mustUnit(t *testing.T, ws *worklang.WorkSet, id string) worklang.Unit {
	t.Helper()
	u, ok := ws.Unit(id)
	if !ok {
		t.Fatalf("unit %q is not in %s", id, ws.File)
	}
	return u
}

// unitKeySeen reports whether the key appears anywhere in the set: on the
// work-set form itself (:inputs, :done-when) or on one of its units. Either way
// the reader kept it rather than dropping it.
func unitKeySeen(ws *worklang.WorkSet, key string) bool {
	if _, ok := ws.Fields[key]; ok {
		return true
	}
	if _, ok := ws.Unknown[key]; ok {
		return true
	}
	for _, u := range ws.Units {
		if _, ok := u.Fields[key]; ok {
			return true
		}
		if _, ok := u.Unknown[key]; ok {
			return true
		}
	}
	return false
}

// refusesWith reads src and checks it is refused (exit 2) with want in the
// message: every refusal names the field or the value that owes it.
func refusesWith(t *testing.T, src, want string) {
	t.Helper()
	_, err := worklang.ParseWorkSet("w.work", []byte(src), worklang.DefaultLimits())
	if err == nil {
		t.Fatalf("read instead of refused: %s", src)
	}
	ref := assertRefusal(t, err)
	if !strings.Contains(ref.Error(), want) {
		t.Errorf("refusal does not name %q: %s", want, ref.Error())
	}
}

// findsRule reads src -- which this reader CAN read -- and checks that Check
// reports the rule, naming the unit when one is named. It is the finding half of
// the split refusesWith holds the other end of.
func findsRule(t *testing.T, src, rule, unit string) {
	t.Helper()
	ws, err := worklang.ParseWorkSet("w.work", []byte(src), worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("refused instead of read: %s: %v", src, err)
	}
	findings, _ := ws.Check(worklang.Options{})
	for _, f := range findings {
		if f.Rule == rule && (unit == "" || f.Unit == unit) {
			return
		}
	}
	t.Errorf("no %s finding for %s: %+v", rule, src, findings)
}
