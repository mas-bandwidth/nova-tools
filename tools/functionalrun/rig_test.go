package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/require"
)

// The rig of tools/functionalrun (docs/STANDARD.md section 8: a rig is a
// helper struct owning the plumbing): one fake engine and run config per tier
// run, the synctest bubble every tier run needs, and the checks the tier-run
// shapes repeat. Each serves at least two of them; the go.mod fixture goes
// through pkg/testkit, and the engine fake stays package-specific as its
// HARNESS.md asks.

type rig struct {
	t      *testing.T
	eng    *fakeEngine
	cfg    runConfig
	stdout strings.Builder
	stderr strings.Builder
	exit   int
}

// newRig builds a rig with this owner's labelled caches and one start code
// per container start in order, or one code for every start when given one.
func newRig(t *testing.T, owner string, codes ...int) *rig {
	t.Helper()
	dir := t.TempDir()
	testkit.WriteFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	c := testConfig()
	c.src = dir
	c.image = "localhost/nova-functional:given"
	eng := &fakeEngine{answers: map[string]fakeAnswer{
		strings.Join(reapListArgs(), " "):                                {out: "[]"},
		"image inspect --format {{.Id}} localhost/nova-functional:given": {out: "sha256:img\n"},
		strings.Join(volumeInspectArgs(c.gocache), " "):                  {out: owner + "|gocache\n"},
		strings.Join(volumeInspectArgs(c.gomod), " "):                    {out: owner + "|gomod\n"},
	}}
	if len(codes) == 1 {
		eng.startCode = codes[0]
	} else {
		eng.startCodes = codes
	}
	return &rig{t: t, eng: eng, cfg: c}
}

// run runs the tier in a synctest bubble and records the exit.
func (r *rig) run() int {
	r.t.Helper()
	r.exit = runTierIn(r.t, r.eng, r.cfg, &r.stdout, &r.stderr)
	return r.exit
}

// requireExit fails the test when the recorded exit is not want.
func (r *rig) requireExit(want int) {
	r.t.Helper()
	require.Equal(r.t, want, r.exit, "a tier run exits %d, want %d\n%s", r.exit, want, r.stderr.String())
}

// calls returns every argv the engine saw, joined into lines.
func (r *rig) calls() []string {
	return r.eng.argvs()
}
