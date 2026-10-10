package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Admission refuses a brief whose PATHS do not hold what it names (docs/SPEC-CARD-CONTRACT.md,
// what admission verifies). The case the day kept holding: PATHS names cmd/nova-sprint/brief.go,
// which is not at the base, and func cmdBrief lives in cmd/nova-sprint/verbs.go. One line per
// miss, each naming verbs.go, so the author fixes PATHS in one edit. The tree is in memory:
// no git, no clock, no socket.
func TestAdmissionRefusesABriefWhosePathsDoNotHoldWhatItNames(t *testing.T) {
	t.Parallel()
	files := []string{
		"a.go",
		"cmd/nova-sprint/add.go",
		"cmd/nova-sprint/verbs.go",
		"internal/x/x.go",
		"internal/x/x_test.go",
		"README",
	}
	text := map[string]string{
		"a.go":                     "package a\n",
		"cmd/nova-sprint/add.go":   "package main\n\nimport \"sprint\"\n",
		"cmd/nova-sprint/verbs.go": "package main\n\nfunc cmdBrief() {}\nfunc cmdAdd() {}\n\ntype AdmissionTree struct{}\n",
		"internal/x/x.go":          "package x\n\nfunc Fix() {}\n",
		"internal/x/x_test.go":     "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n",
		"README":                   "base\n",
	}
	tree := AdmissionTree{
		Files: files,
		At:    "abcdefabcdef (sprint/s)",
		Hold: func(ident string) ([]string, error) {
			var hit []string
			for _, f := range files {
				if strings.Contains(text[f], ident) {
					hit = append(hit, f)
				}
			}
			return hit, nil
		},
	}
	brief := func(paths, start, stop, test, extra string) string {
		b := "RESULT: c sha=0123456789ab tier: pro\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/s\nPATHS: " + paths + "\n"
		if start != "" {
			b += "START: " + start + "\n"
		}
		if stop != "" {
			b += "STOP: " + stop + "\n"
		}
		if test != "" {
			b += "TEST: " + test + "\n"
		}
		if extra != "" {
			b += extra
			if !strings.HasSuffix(extra, "\n") {
				b += "\n"
			}
		}
		return b + "\nTHE TASK. Fix the empty case."
	}
	at := "abcdefabcdef (sprint/s)"

	t.Run("the PATHS file is not at the base and the func lives elsewhere", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief(
			"cmd/nova-sprint/brief.go",
			"",
			"func cmdBrief is named with cmd/nova-sprint/brief.go",
			"none the admission check",
			"",
		), tree)
		require.Equal(t, []string{
			"PATHS cmd/nova-sprint/brief.go names nothing at " + at + "; the nearest file is cmd/nova-sprint/verbs.go",
			"PATHS do not hold func cmdBrief named with cmd/nova-sprint/brief.go: it is in cmd/nova-sprint/verbs.go",
		}, got)
	})

	t.Run("a glob that matches nothing", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief("internal/nope/*.go", "", "", "none a glob", ""), tree)
		require.Equal(t, []string{
			"PATHS internal/nope/*.go matches nothing at " + at + "; the nearest file is internal/x/x.go",
		}, got)
	})

	t.Run("a markdown code span names a func outside PATHS", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief(
			"cmd/nova-sprint/add.go",
			"",
			"func `cmdBrief` is named with `cmd/nova-sprint/verbs.go`",
			"none a code span",
			"",
		), tree)
		require.Equal(t, []string{
			"PATHS do not hold func cmdBrief named with cmd/nova-sprint/verbs.go: it is in cmd/nova-sprint/verbs.go",
		}, got)
	})

	t.Run("a qualified name outside PATHS is the name, not the qualifier", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief(
			"cmd/nova-sprint/add.go",
			"",
			"type sprint.AdmissionTree is named with cmd/nova-sprint/verbs.go",
			"none a qualified name",
			"",
		), tree)
		require.Equal(t, []string{
			"PATHS do not hold type AdmissionTree named with cmd/nova-sprint/verbs.go: it is in cmd/nova-sprint/verbs.go",
		}, got)
	})

	t.Run("a code span around a qualified name outside PATHS", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief(
			"cmd/nova-sprint/add.go",
			"",
			"func `sprint.cmdBrief` is named with `cmd/nova-sprint/verbs.go`",
			"none a spanned qualified name",
			"",
		), tree)
		require.Equal(t, []string{
			"PATHS do not hold func cmdBrief named with cmd/nova-sprint/verbs.go: it is in cmd/nova-sprint/verbs.go",
		}, got)
	})

	t.Run("a qualified name inside PATHS passes", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief(
			"cmd/nova-sprint/verbs.go",
			"",
			"type sprint.AdmissionTree is named with cmd/nova-sprint/verbs.go",
			"none a qualified name held",
			"",
		), tree)
		require.Empty(t, got)
	})

	t.Run("the func is only outside PATHS", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief(
			"cmd/nova-sprint/add.go",
			"",
			"func cmdBrief lives in cmd/nova-sprint/verbs.go",
			"none the func is elsewhere",
			"",
		), tree)
		require.Equal(t, []string{
			"PATHS do not hold func cmdBrief named with cmd/nova-sprint/verbs.go: it is in cmd/nova-sprint/verbs.go",
		}, got)
	})

	t.Run("an existing test outside PATHS", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief("internal/x/x.go", "", "", "./internal/x TestOld", ""), tree)
		require.Equal(t, []string{
			"PATHS do not hold test TestOld named with internal/x: it is in internal/x/x_test.go",
		}, got)
	})

	t.Run("a brief whose paths hold what it names", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief(
			"cmd/nova-sprint/verbs.go",
			"",
			"func cmdBrief lives in cmd/nova-sprint/verbs.go",
			"./internal/x TestNew",
			"",
		), tree)
		require.Empty(t, got, "the new test is absent, and the func is inside PATHS")
	})

	t.Run("a missing tree is one line and no invented miss", func(t *testing.T) {
		t.Parallel()
		got := PathsAdmission(brief("cmd/nova-sprint/brief.go", "", "func cmdBrief is named with cmd/nova-sprint/brief.go", "none a missing tree", ""),
			AdmissionTree{Missing: "the base sprint/s could not be fetched", At: "sprint/s"})
		require.Equal(t, []string{"MISSING: the base sprint/s could not be fetched"}, got)
	})

	t.Run("a new test file and a NEW line may be absent", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, PathsAdmission(brief("internal/x/x_new_test.go", "", "", "none a new test file", ""), tree))
		require.Empty(t, PathsAdmission(brief("internal/fresh.go", "", "", "none a new file", "NEW: internal/fresh.go"), tree))
	})

	t.Run("a CARRY brief skips a file only the head has and still checks the func", func(t *testing.T) {
		t.Parallel()
		carry := "CARRY: s1-1 attempt 1 head=0123456789abcdef0123456789abcdef01234567\n"
		require.Empty(t, PathsAdmission(brief("a.go,b.go", "", "", "none a widen", carry), tree), "b.go is only at the head")
		got := PathsAdmission(brief("a.go,b.go", "", "func cmdBrief lives in cmd/nova-sprint/verbs.go", "none a widen", carry), tree)
		require.Equal(t, []string{
			"PATHS do not hold func cmdBrief named with cmd/nova-sprint/verbs.go: it is in cmd/nova-sprint/verbs.go",
		}, got)
	})

	t.Run("no REPO or no PATHS is not read against a tree", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, PathsAdmission("RESULT: c tier: pro\nPATHS: missing.go\nBASE: sprint/s\n\nTHE TASK.", tree))
		require.Empty(t, PathsAdmission("RESULT: c tier: pro\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/s\n\nTHE TASK.", AdmissionTree{Missing: "gone"}))
	})

	t.Run("the first PATHS hold is a brief defect carrying the proposed PATHS", func(t *testing.T) {
		t.Parallel()
		report := "friend amy HOLD: PATHS do not hold cmdBrief\nPATHS-PROPOSED: cmd/nova-sprint/verbs.go"
		want := BriefDefectPaths + "; PATHS: cmd/nova-sprint/verbs.go"
		assert.Equal(t, want, BriefDefectOf(report))
		assert.Equal(t, BriefDefectPaths+"; PATHS: cmd/nova-sprint/verbs.go",
			BriefDefectOf("HOLD: PATHS do not hold func cmdBrief\nPATHS: cmd/nova-sprint/verbs.go"))
		assert.Empty(t, BriefDefectOf("HOLD: not PATHS do not hold the func"))
		assert.Empty(t, BriefDefectOf("HOLD: paths-do-not-hold the func"))
		assert.Empty(t, BriefDefectOf("HOLD: the gate is red\nPATHS-PROPOSED: cmd/nova-sprint/verbs.go"), "a proposal alone stays the worker's failure")

		w := friendWorld(t, friendBrief("friend amy"))
		dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"})
		row := FriendRow("amy")
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: row, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: report}))
		wc := w.s.Fleet.Card("s1-1.w1")
		require.NotNil(t, wc)
		assert.Equal(t, DoneDefect, wc.Col)
		assert.Equal(t, want, wc.F(FieldBriefDefect))
		assert.Equal(t, 0, w.s.Fleet.Count(row, DoneFailed))
		assert.Equal(t, 0, w.s.Primary("s1-1").Int("failed"), "the first such hold, not the fourth")
		assert.Empty(t, w.notesOf(NWorkFailed))
		js := w.notesOf(NBriefDefect)
		require.Len(t, js, 1)
		assert.Equal(t, 1, js[0].Attempt)
		assert.Contains(t, js[0].What, "PATHS: cmd/nova-sprint/verbs.go")
		assert.Contains(t, js[0].What, BriefDefectPaths)

		w2 := friendWorld(t, friendBrief("friend amy"))
		dealStarted(w2, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"})
		w2.must(Finish(w2.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: row, Gens: gensOf(w2.s, "s1-1.w1"), Failed: true,
			Report: "HOLD: the gate is red\nPATHS-PROPOSED: cmd/nova-sprint/verbs.go"}))
		assert.Equal(t, DoneFailed, w2.s.Fleet.Card("s1-1.w1").Col)
		assert.Len(t, w2.notesOf(NWorkFailed), 1)
		assert.Empty(t, w2.notesOf(NBriefDefect))
	})
}

// ApplyBriefFix applies every corrected header line swarm.LintBrief computed: the line for
// its key replaces the brief's own, and a key the brief has none of is inserted after the
// header block, so add stores the brief its own lint asked for instead of the seat editing
// it by hand (docs/SPEC-SPRINT.md section 11, the brief checks).
func TestApplyBriefFixAppliesEveryCorrectedHeaderLine(t *testing.T) {
	t.Parallel()
	brief := strings.Join([]string{
		"RESULT: c sha=0123456789ab tier: pro",
		"REPO: mas-bandwidth/nova-tools",
		"BASE: sprint/s",
		"PATHS: internal/x/*.go",
		"TEST: ./internal/x TestNew",
		"",
		"THE TASK. Fix internal/x/x.go.",
	}, "\n")
	got := ApplyBriefFix(brief, []string{
		"PATHS: internal/x/*.go,internal/x/testdata/**",
		"NEW: internal/x/fresh.go",
		"SHARED: internal/ci/testdata/errcheck/internal/x.txt",
		"RESULT: c sha=0123456789ab tier: frontier",
	})
	want := strings.Join([]string{
		"RESULT: c sha=0123456789ab tier: frontier",
		"REPO: mas-bandwidth/nova-tools",
		"BASE: sprint/s",
		"PATHS: internal/x/*.go,internal/x/testdata/**",
		"TEST: ./internal/x TestNew",
		"NEW: internal/x/fresh.go",
		"SHARED: internal/ci/testdata/errcheck/internal/x.txt",
		"",
		"THE TASK. Fix internal/x/x.go.",
	}, "\n")
	assert.Equal(t, want, got)
}

// The deal-time brief lint: a ready card whose brief no longer passes at its base tip is
// parked in the fix column with the exact BRIEF DRIFT line, dealt to no worker, and the
// tick raises one judgment whose decisions include brief --fix; once the brief is fixed
// (brief --fix wrote the lint's own corrected lines) the next tick deals it
// (docs/SPEC-SPRINT.md section 11, the brief checks).
func TestABriefThatDriftedAtTheDealIsParkedAndFixed(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 1, 2, "m1")
	drift := "BRIEF DRIFT check=paths-at-base line=4: MISSING: origin mas-bandwidth/nova-tools holds no branch sprint/gone"
	r := TickReq{BriefDrift: func(_ *Snapshot, c *Card) string {
		if c.ID == "s1-1" {
			return drift
		}
		return ""
	}}
	w.part(TickDeal, r)
	assert.Equal(t, Ready, w.s.Work.Card("s1-1").Col, "a card whose brief drifted is not dealt")
	ns := w.notesOf(NBriefDrift)
	require.Len(t, ns, 1, "the park raises one judgment")
	assert.Equal(t, drift, ns[0].What, "the judgment names the exact line")
	assert.Contains(t, TickDecisions[NBriefDrift], "brief")

	// the brief passes again: the next tick deals it
	w.part(TickDeal, TickReq{})
	assert.Equal(t, Working, w.s.Work.Card("s1-1").Col, "a brief that passes is dealt")
}
