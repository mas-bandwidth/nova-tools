// The rig of tlacheck's tests: the plumbing every test of the tool shares, in
// one file, built on the package's own adapter (testEnv, scriptedTLC,
// checkout, do, call) and never a copy of it. A scenario method or checker
// carries the shape it replaces and the count of its copies, and a shrink card
// converts its tests' calls to the rig, leaving the rig alone
// (docs/STANDARD.md, Tests: shared rigs, one constructor with defaults and
// scenarios over copies).

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// rig is one cold run of the tool: a checkout of cases, the jar it pins, a
// scripted TLC and the environment a verb runs in, with both streams held.
type rig struct {
	t    *testing.T
	root string // the checkout the verbs read, empty until cases writes it
	jar  string // the jar the runs are given, empty until cases writes it
	e    env
	out  *bytes.Buffer
	errs *bytes.Buffer
}

// newRig holds the default environment: nothing dialled, nothing looked up,
// TLC unscripted, both streams the rig's.
func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t}
	r.e, r.out, r.errs = testEnv(t, nil)
	return r
}

// cases writes the three-case checkout and its jar into the rig, the fixture
// most tests start from (the checkout-and-jar shape, 14 copies).
func (r *rig) cases() {
	r.root, r.jar = checkout(r.t)
}

// script replaces the environment's TLC with one that answers each
// configuration with its code and records the runs (the scripted-TLC shape,
// 10 copies).
func (r *rig) script(codes map[string]int, runs *[]tlc.Run) {
	r.e.exec = scriptedTLC(r.t, codes, runs)
}

// run hands one invocation to the environment the rig holds and returns what
// either stream got (the env-and-streams threading shape, 15 copies).
func (r *rig) run(args ...string) result {
	return do(r.e, r.out, r.errs, args...)
}

// runGroups runs every named group on a fresh environment under the standard
// four results (a pass, an invariant counterexample, an action one, a debt
// case) and returns the records files the runs wrote (the run-and-collect
// loop, 4 copies).
func (r *rig) runGroups(groups ...string) []string {
	r.t.Helper()
	codes := map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}
	var runs []string
	for _, group := range groups {
		dir := r.t.TempDir()
		res := call(r.t, scriptedTLC(r.t, codes, nil), "run", "--root", r.root, "--jar", r.jar, "--dir", dir, "--group", group)
		if res.code != 0 {
			r.t.Fatalf("%s: %+v", group, res)
		}
		runs = append(runs, filepath.Join(dir, tlc.RunsFile))
	}
	return runs
}

// refused requires the one-line refusal grammar: exit 2, one line on stderr
// naming every want (the exit-2 refusal check, 10 copies).
func (r *rig) refused(got result, want ...string) {
	r.t.Helper()
	if got.code != 2 || strings.Count(got.stderr, "\n") != 1 {
		r.t.Errorf("want a one-line refusal at exit 2: %+v", got)
	}
	for _, w := range want {
		if !strings.Contains(got.stderr, w) {
			r.t.Errorf("the refusal %q lacks %q", got.stderr, w)
		}
	}
}

// theHostIs requires the tool's label of itself to be the process's own: the
// pin the host test holds and every records file's Host column answers to.
func (r *rig) theHostIs(got, want string) {
	r.t.Helper()
	if got != want {
		r.t.Errorf("%s, want %s", got, want)
	}
}
