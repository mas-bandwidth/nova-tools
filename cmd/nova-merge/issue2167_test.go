package main

import (
	"os"
	"path/filepath"
	"testing"
)

// nova-tools #2167: `batch --reference` naming a SHALLOW working copy.
//
// The checkout a dogfooder stands in is a shallow clone, and it is the --reference a
// person standing in the repository types. git refuses it as a reference repository,
// and `batch` relayed git's sentence as BATCH REFUSED with no remedy:
//
//	BATCH REFUSED: git clone --quiet --reference <path> -- <url> <clone>: exit status 128:
//	fatal: reference repository '<path>' is shallow
//
// The reason was on the caller's own bench and this tool could have said it, with the
// remedy beside it: omit --reference, or name a full clone. The gate now refuses a
// shallow --reference BEFORE anything is touched, in its own words. The second half of
// this test holds the other edge of the same hole: a FULL clone as --reference is what
// the flag is for, and it still gets past the refusal and is still used.
func TestIssue2167(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	before := len(l.pushes())

	// The shallow working copy, the shape ~/johnny-working/nova-tools was on the bench
	// in the report: a --depth 1 clone over the transport, verified shallow the way the
	// report verified it.
	shallow := filepath.Join(l.dir, "shallow-working-copy")
	l.git(l.dir, "clone", "--quiet", "--depth", "1", fileURL(l.remote), shallow)
	if got := l.git(shallow, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("the working copy is not shallow (git says %q), so this run does not reproduce the report", got)
	}

	root := filepath.Join(l.dir, "batch")
	exit, stdout, stderr := l.run("batch", "--name", "integration-reference", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--reference", shallow, "--timeout", "5m")

	// The refusal itself is the shape both before and after: exit 2, one line on
	// stderr. WHAT THAT LINE CARRIES is the issue.
	if exit != 2 {
		t.Fatalf("a shallow --reference is a refusal, exit 2; got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")
	// It names the flag, the working copy and the fact...
	contains(t, stderr, "--reference")
	contains(t, stderr, shallow)
	contains(t, stderr, "is shallow")
	// ...and the REMEDY, both halves of it: omit --reference, or name a full clone.
	// git's own sentence names neither, which is the hole #2167 is.
	contains(t, stderr, "omit --reference")
	contains(t, stderr, "full clone")
	absent(t, stderr, "fatal: reference repository")
	absent(t, stdout, "BATCH")
	// And it is a PREFLIGHT: the shallowness of the caller's reference is a fact of the
	// bench, knowable before anything is touched, so nothing is built under --root
	// before the refusal is said.
	if _, err := os.Stat(root); err == nil {
		t.Errorf("the batch built %s before refusing a --reference it could have refused before touching anything", root)
	}
	if got := len(l.pushes()); got != before {
		t.Errorf("the remote received %d new pushes; a refused batch pushes nothing", got-before)
	}

	// THE OTHER EDGE: a full clone as --reference is the flag working as documented, and
	// it still gets past the refusal -- the report measured the same on its bench. The
	// gate must not have learned to refuse every reference, which would pass the half
	// above and quietly take the optimisation away.
	full := filepath.Join(l.dir, "full-working-copy")
	l.git(l.dir, "clone", "--quiet", fileURL(l.remote), full)
	if got := l.git(full, "rev-parse", "--is-shallow-repository"); got != "false" {
		t.Fatalf("the working copy is shallow (git says %q), so this run says nothing about a full one", got)
	}
	fexit, fstdout, fstderr := l.run("batch", "--name", "integration-full", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--reference", full, "--timeout", "5m")
	if fexit != 0 {
		t.Fatalf("a full clone as --reference is the flag working, exit 0; got %d\nstdout: %s\nstderr: %s", fexit, fstdout, fstderr)
	}
	contains(t, fstdout, "BATCH OK name=integration-full")
	// And the reference was USED, not quietly dropped: the clone borrows the reference's
	// object store, which is the whole point of --reference.
	alternates := filepath.Join(root, "integration-full", "repo", ".git", "objects", "info", "alternates")
	raw, err := os.ReadFile(alternates)
	if err != nil {
		t.Fatalf("the clone wrote no %s: %v\nstdout: %s\nstderr: %s", alternates, err, fstdout, fstderr)
	}
	contains(t, string(raw), filepath.Join(full, ".git", "objects"))
}
