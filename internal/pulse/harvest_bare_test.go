package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHarvestFoldsBareSwarmRoot (SPEC-PULSE rule 12): harvest folds the RESULT.md
// files already sitting in a bare swarm root's <root>/<slot>/jobs/<label>/ whoever
// put them there, not only a root nova-pulse cut wrote a cards.tsv into. The loop
// that actually runs cards on this bench hands them straight to nova-swarm batch,
// so the job dirs are all the root has and harvest must fold them.
func TestHarvestFoldsBareSwarmRoot(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/628")

	// A bare swarm root: no cards.tsv, one card a batch left in its job dir, with the
	// clone it made. NOTHING under this root was written by anyone but the worker -- the
	// clone's own `origin` included, since one `git remote set-url` rewrites it (Johnny's
	// HOLD of #1809 at 7f692ef6) -- so the destination comes from the coordinator's own
	// `--clone`, passed below, and the clone's origin is only checked against it.
	job := filepath.Join(root, "1", "jobs", "card-880")
	if err := os.MkdirAll(filepath.Join(job, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"),
		[]byte("RESULT card-880 sha=aaa\nDONE\nBRANCH rowan/br-880\nREPO owner/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errs := runHarvestWithClones(t, root, []string{"owner/repo=" + filepath.Join(root, "coordinator-clone")})
	if strings.Contains(errs, "HARVEST REFUSED") {
		t.Fatalf("a bare swarm root must be folded, not refused:\n%s", errs)
	}
	if !strings.Contains(out, "HARVEST PR repo=owner/repo pr=628 label=card-880 branch=rowan/br-880") {
		t.Fatalf("want the bare root's job dir folded and pushed, got:\n%s", out)
	}
	if !strings.Contains(out, "pushed=1") || !strings.Contains(out, "prs=1") {
		t.Fatalf("want pushed=1 prs=1, got:\n%s", out)
	}
}
