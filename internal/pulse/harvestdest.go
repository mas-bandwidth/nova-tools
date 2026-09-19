package pulse

// THE ONE DESTINATION RESOLVER (Johnny's HOLD of PR #1809 at 8bfa4020, issue #1824).
//
// `docs/SPEC-SWARM.md:40-44`: **everything a worker writes is data.** A `RESULT.md` is a
// report, never an instruction. The first three cuts of this fix kept treating its `REPO`
// line as the destination and then bolting a veto on beside it, which is a different thing
// and a weaker one:
//
//   - `pushURL` of the RESULT `REPO` still FORMED the push URL from the worker's claim, so the
//     claim was the destination and the guard was an opinion about it;
//   - the veto only fired when the job's origin resolved -- an empty or unreadable origin
//     verified nothing and let the claim through, which is exactly the bare-root case
//     every bench card lands in when its clone is missing;
//   - `harvest --bench` opened its pull request `-R <RESULT REPO>`, and `--working` and
//     `manager.openPR` pushed `https://github.com/<RESULT REPO>.git`, past the veto's
//     reach on three of the four paths.
//
// So the claim stops being the destination. `resolveDestination` ANSWERS the destination --
// both spellings of it, the push URL and the pull-request repo -- from a record no worker
// wrote: git's own `origin` on the job's clone, or failing that the launch record, the card
// the pulse cut. The `REPO` line is then only ever a claim to be checked against that
// answer, and a claim that disagrees, or an answer nothing could give, is refused by name:
//
//	HARVEST REFUSED repo-unknown card=<label> ...
//	HARVEST REFUSED repo-mismatch card=<label> origin=<origin> claimed=<the RESULT REPO> ...
//
// Every `git push`, `gh pr create` and `Forge.CreatePR` in this package goes through it --
// `Harvest`'s `push` and `openPR`, `workingRun.one`, `harvestBench` and `manager.openPR` --
// and `TestEveryPublishSiteResolvesItsDestination` walks the package's syntax tree and fails
// unless the function holding the call resolved the destination here. There is no second
// implementation and no flag that relaxes it, because a guard with a configuration escape is
// a guard the next caller turns off.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// githubCloneBase is the ONE place this package spells the forge's clone URL. Every push
// URL is this plus <owner>/<name>.git, formed here and nowhere else -- which is also what
// keeps the host out of the test files internal/ci's net class test reads.
const githubCloneBase = "https://github.com/"

// destination is WHERE a harvest may push and open a pull request: one answer in the two
// spellings the call sites need. A caller never assembles either half itself -- that is what
// `pushURL` of a RESULT `REPO` was, and it is gone.
type destination struct {
	repo string // owner/name: the repository a pull request is opened on
	url  string // https://github.com/owner/name.git: the explicit-refspec push URL
	from string // clone-origin | launch-record: what answered, for the operator's line
}

// resolveDestination is THE destination rule, in one place, for every push and PR-open site
// there is.
//
//	label   the card label, for the refusal line
//	clone   the job's own clone directory; git's recorded `origin` is read from it
//	record  the LAUNCH record -- the card file the pulse cut -- or "" when the site has none
//	claimed the REPO line off the worker's RESULT.md, which is checked and never trusted
//
// The order is deliberate: git's record first, because git wrote it when the card cloned and
// no card can edit it without the push failing anyway; the launch record second, because the
// pulse cut it before the worker ran. A RESULT.md is neither, at any position -- see
// launchRecordRepo, which refuses to be handed one.
func resolveDestination(label, clone, record, claimed string) (destination, error) {
	origin, from := cloneOrigin(clone), "clone-origin"
	if origin == "" {
		origin, from = launchRecordRepo(record), "launch-record"
	}
	if origin == "" {
		return destination{}, fmt.Errorf("HARVEST REFUSED repo-unknown card=%s: git reads no origin from this job's clone %s and no launch record names a repository -- a destination is never taken from a RESULT.md (SPEC-SWARM: everything a worker writes is data); nothing was pushed",
			field(label), field(clone))
	}
	if c := normalizeRepo(claimed); c != "" && c != origin {
		// The grep-able door, identical on all four paths, with BOTH halves of the
		// disagreement on it: what was resolved and what the report asked for.
		return destination{}, fmt.Errorf("HARVEST REFUSED repo-mismatch card=%s origin=%s claimed=%s -- a RESULT is a worker's report, not an instruction (SPEC-SWARM: everything a worker writes is data); nothing was pushed", label, origin, c)
	}
	return destination{repo: origin, url: githubCloneBase + origin + ".git", from: from}, nil
}

// launchRecordRepo is the repository the LAUNCH RECORD names: the card file `cut` wrote,
// which exists before the worker does.
//
// A bare swarm root has no cards.tsv, so `discoverRootCards` builds its rows with the
// RESULT.md itself as the card path (rule 11: with no card, the RESULT's own line 1 is the
// contract). That is correct for the contract and catastrophic for the destination -- it
// made the check `the RESULT.md agrees with the RESULT.md`, which is Johnny's word
// "tautological". So the record is refused when it IS a RESULT.md, here, in the resolver,
// rather than at each of the four call sites where the fifth one would forget.
func launchRecordRepo(record string) string {
	r := strings.TrimSpace(record)
	if r == "" || strings.EqualFold(filepath.Base(r), "RESULT.md") {
		return ""
	}
	return cardRepo(r)
}
