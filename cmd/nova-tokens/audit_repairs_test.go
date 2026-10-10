package main

import (
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcesRefusesNonexistentRoots(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repos := write(t, filepath.Join(dir, "repos.tsv"), "repo\tpath\nnova-tools\t/fake\n")

	// Missing claude root
	noClaude := filepath.Join(dir, "no-claude")
	r := invoke(t, "sources", "--all", "--repos", repos, "--claude", "bench="+noClaude)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "SOURCES REFUSED:")
	wantContains(t, r.stderr, "--claude bench="+noClaude+" does not exist")

	// Missing bus root
	noBus := filepath.Join(dir, "no-bus")
	r = invoke(t, "sources", "--all", "--repos", repos, "--bus", noBus)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "SOURCES REFUSED:")
	wantContains(t, r.stderr, "--bus does not exist: "+noBus)

	// Missing swarm root
	noSwarm := filepath.Join(dir, "no-swarm")
	r = invoke(t, "sources", "--all", "--repos", repos, "--swarm", "bench="+noSwarm)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "SOURCES REFUSED:")
	wantContains(t, r.stderr, "--swarm bench="+noSwarm+" does not exist")

	// Missing scratch root when opencode is provided
	noScratch := filepath.Join(dir, "no-scratch")
	r = invoke(t, "sources", "--all", "--repos", repos, "--opencode", "bench="+filepath.Join(dir, "db.sqlite"), "--scratch", noScratch)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "SOURCES REFUSED:")
	wantContains(t, r.stderr, "--scratch does not exist: "+noScratch)
}

func TestCheckThroughFlag(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "reports"))
	const ver = "nova-tokens v1 day=2026-09-18 at=2026-09-18T23:55:00Z build=b turns=1 sources=x\n"
	const hdr = "date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"
	row := "2026-09-18\tm\tr\t10\t10\t-\t-\t-\t0\tUTC\tx\n"
	write(t, filepath.Join(out, "2026-09-18.tsv"), ver+hdr+row)

	// Invalid day format exits 2
	r := invoke(t, "check", "--out", out, "--through", "not-a-day")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "CHECK REFUSED: --through is not a day: not-a-day")
	wantContains(t, r.stderr, "it wants YYYY-MM-DD")
	assert.False(t, strings.Contains(r.stderr, "--all"), "--through refusal mentions --all: %s", r.stderr)

	// Last folded day (2026-09-18) is older than through (2026-09-20) -> exit 1 with stale message
	r = invoke(t, "check", "--out", out, "--through", "2026-09-20")
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "CHECK STALE last=2026-09-18 through=2026-09-20")

	// Last folded day (2026-09-18) matches through (2026-09-18) -> exit 0
	r = invoke(t, "check", "--out", out, "--through", "2026-09-18")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "CHECK OK")

	// Last folded day (2026-09-18) is newer than through (2026-09-15) -> exit 0
	r = invoke(t, "check", "--out", out, "--through", "2026-09-15")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "CHECK OK")
}
