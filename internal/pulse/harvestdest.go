package pulse

// THE ONE DESTINATION RESOLVER (Johnny's HOLDs of PR #1809 at 8bfa4020 and at 7f692ef6,
// issue #1824).
//
// `docs/SPEC-SWARM.md:40-44`: **everything a worker writes is data.** A `RESULT.md` is a
// report, never an instruction. The cuts before this one kept looking for somewhere honest
// to read the destination FROM, and each one was still inside the worker's reach:
//
//   - `pushURL` of the RESULT `REPO` FORMED the push URL out of the worker's claim, so the
//     claim was the destination and every guard beside it was an opinion about it;
//   - the veto only fired when the job's origin RESOLVED, so an empty origin verified
//     nothing and let the claim through on every path;
//   - and then the destination came from `git remote get-url origin` ON THE JOB'S CLONE.
//     That clone is the worker's own working directory. A worker runs
//     `git remote set-url origin https://github.com/attacker/exfil.git`, writes a RESULT
//     naming attacker/exfil, and the two agree -- which is Johnny's HOLD at 7f692ef6, and
//     SPEC-SANDBOX 27b already says origin is rewritable from inside the wall.
//
// So the job's origin is a CLAIM, the same class as the RESULT's REPO line, and the rule is
// now one sentence:
//
//	THE DESTINATION IS WHAT THE MANAGER RECORDED BEFORE THE WORKER RAN.
//	Everything the worker can write is compared against it and never read as it.
//
// There are exactly two things the manager records, and `dispatch` is both of them:
//
//   - `dispatchFromLaunchRecord` -- the card file the pulse `cut` wrote and `launch`
//     dispatched, or the manager queue's own `launched/<card>` marker. It exists before the
//     worker does and lives outside every job directory.
//   - `dispatchFromCoordinatorClone` -- the `--clone <owner>/<name>=<dir>` the operator
//     typed on the coordinator's own command line, or failing that the origin of a
//     directory on the coordinator's own disk that no worker has ever been handed.
//
// `resolveDestination` then answers the destination in the two spellings the call sites
// need, and refuses by name:
//
//	HARVEST REFUSED repo-unknown  card=<label> ...
//	HARVEST REFUSED repo-mismatch card=<label> dispatched=<x> origin=<the job clone's>  ...
//	HARVEST REFUSED repo-mismatch card=<label> dispatched=<x> claimed=<the RESULT REPO> ...
//
// Every `git push`, `gh pr create` and `Forge.CreatePR` in this package goes through it --
// `Harvest`'s `push` and `openPR`, `workingRun.one`, `harvestBench` and `manager.openPR` --
// and two generic AST walks keep it that way: `pushpath_class_test.go` fails unless every
// publishing function resolves, and `TestCloneOriginIsOnlyEverComparedNeverRead` fails
// unless `cloneOrigin` is called from this file alone. There is no second implementation
// and no flag that relaxes it, because a guard with a configuration escape is a guard the
// next caller turns off.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// githubCloneBase is the ONE place this package spells the forge's clone URL. Every push
// URL is this plus <owner>/<name>.git, formed here and nowhere else -- which is also what
// keeps the host out of the test files internal/ci's net class test reads.
const githubCloneBase = "https://github.com/"

// dispatch is the destination AS THE MANAGER RECORDED IT, before the worker ran: the only
// kind of statement about where results go that a worker cannot edit. A zero dispatch is
// "nothing the manager wrote names a repository", which is a refusal and never a licence to
// fall back on something the worker can write.
type dispatch struct {
	repo string // owner/name
	from string // launch-record | coordinator-clone
}

// destination is WHERE a harvest may push and open a pull request: one answer in the two
// spellings the call sites need. A caller never assembles either half itself -- that is what
// `pushURL` of a RESULT `REPO` was, and it is gone.
type destination struct {
	repo string // owner/name: the repository a pull request is opened on
	url  string // https://github.com/owner/name.git: the explicit-refspec push URL
	from string // which manager-side record answered, for the operator's line
}

// dispatchFromLaunchRecord is the repository the LAUNCH RECORD names: the card file `cut`
// wrote and `launch` dispatched, or the manager queue's `launched/<card>` marker. Both exist
// before the worker does, and neither is inside a job directory.
//
// A bare swarm root has no cards.tsv, so `discoverRootCards` builds its rows with the
// RESULT.md itself as the card path (rule 11: with no card, the RESULT's own line 1 is the
// contract). That is correct for the contract and catastrophic for the destination -- it
// made the check `the RESULT.md agrees with the RESULT.md`, which is Johnny's word
// "tautological". So a record that IS a RESULT.md answers nothing, here, in the resolver's
// own file, rather than at each of the four call sites where the fifth one would forget.
func dispatchFromLaunchRecord(record string) dispatch {
	r := strings.TrimSpace(record)
	if r == "" || strings.EqualFold(filepath.Base(r), "RESULT.md") {
		return dispatch{}
	}
	return dispatch{repo: cardRepo(r), from: "launch-record"}
}

// dispatchFromCoordinatorClone is the destination the COORDINATOR named on its own command
// line, for the two folds that carry no launch record to the harvesting verb.
//
//	clones  the --clone entries: <owner>/<name>=<dir>, or a bare <dir> for any repo
//	dir     the entry that was selected, i.e. cloneFor(clones, ...)
//
// The name the operator TYPED wins, because it was typed on the coordinator. Failing that,
// git's origin in `dir` -- a directory on the coordinator's own disk, created by the
// operator and never handed to a worker, which is the one origin in this package that is
// not a worker's claim. If the operator both named a repo and pointed at a clone of a
// different one, that disagreement is the operator's and is refused rather than guessed.
//
// NOTE, said out loud rather than buried: with several `<owner>/<name>=<dir>` entries the
// worker's REPO line selects WHICH of them is used. That is a choice from a list the
// operator wrote -- an allowlist, not the worker naming a destination; a repo the operator
// did not name resolves to no clone and is refused.
func dispatchFromCoordinatorClone(clones []string, dir string) (dispatch, error) {
	named := declaredRepoFor(clones, dir)
	onDisk := ""
	if strings.TrimSpace(dir) != "" {
		onDisk = cloneOrigin(dir)
	}
	switch {
	case named != "" && onDisk != "" && named != onDisk:
		return dispatch{}, fmt.Errorf("--clone names %s but git reads %s in %s; the coordinator's own two statements disagree, so nothing is guessed",
			field(named), field(onDisk), field(dir))
	case named != "":
		return dispatch{repo: named, from: "coordinator-clone"}, nil
	case onDisk != "":
		return dispatch{repo: onDisk, from: "coordinator-clone"}, nil
	}
	return dispatch{}, nil
}

// managerDispatch is what a site with BOTH kinds of record asks: the launch record first,
// because it is per-card and the pulse cut it, and the coordinator's `--clone` second,
// because an operator naming one repository on the command line is still the manager
// speaking. A bare swarm root has no cards.tsv and so no launch record at all, and this is
// how it keeps publishing: `--clone <owner>/<name>=<dir>`, typed on the coordinator, rather
// than a REPO line read off the worker's own report.
func managerDispatch(record string, clones []string) (dispatch, error) {
	if d := dispatchFromLaunchRecord(record); d.repo != "" {
		return d, nil
	}
	return theOneCoordinatorDispatch(clones)
}

// theOneCoordinatorDispatch is dispatchFromCoordinatorClone for a fold where the worker's
// own claim must not even SELECT among the operator's entries: `harvest --working`, whose
// jobs push from their own clones and so need a repository name and no directory at all.
//
// It takes the `--clone` list and requires it to name exactly one destination. One entry is
// the ordinary case. Several are ambiguous, and ambiguity resolved by reading the RESULT.md
// is how the destination got back into the worker's hands twice already, so it refuses and
// says which ones it saw.
func theOneCoordinatorDispatch(clones []string) (dispatch, error) {
	var named, bare []string
	for _, c := range clones {
		// The DIRECTORY half is not read on this path: a --working job pushes from its
		// own clone, so all the coordinator has to supply is the repository's NAME.
		name, _, ok := strings.Cut(c, "=")
		if !ok {
			if d := strings.TrimSpace(c); d != "" {
				bare = append(bare, d)
			}
			continue
		}
		if r := normalizeRepo(strings.TrimSpace(name)); r != "" {
			named = append(named, r)
		}
	}
	switch {
	case len(named) == 1 && len(bare) == 0:
		return dispatch{repo: named[0], from: "coordinator-clone"}, nil
	case len(named) == 0 && len(bare) == 1:
		if r := cloneOrigin(bare[0]); r != "" {
			return dispatch{repo: r, from: "coordinator-clone"}, nil
		}
		return dispatch{}, nil
	case len(named)+len(bare) > 1:
		return dispatch{}, fmt.Errorf("--clone names %d destinations (%s); a --working harvest takes exactly one, because choosing between them would mean reading the worker's own RESULT.md",
			len(named)+len(bare), field(strings.Join(append(append([]string{}, named...), bare...), " ")))
	}
	return dispatch{}, nil
}

// declaredRepoFor is the `<owner>/<name>` an operator typed for this directory: the twin of
// cloneFor, which answers the dir for a repo where this answers the repo for a dir. A bare
// `--clone <dir>` declares no repository and answers "".
func declaredRepoFor(clones []string, dir string) string {
	want := strings.TrimSpace(dir)
	if want == "" {
		return ""
	}
	for _, c := range clones {
		name, d, ok := strings.Cut(c, "=")
		if !ok || strings.TrimSpace(d) != want {
			continue
		}
		if r := normalizeRepo(strings.TrimSpace(name)); r != "" {
			return r
		}
	}
	return ""
}

// resolveDestination is THE destination rule, in one place, for every push and PR-open site
// there is.
//
//	label       the card label, for the refusal line
//	d           the manager's own record of where this job's results go -- THE ANSWER
//	workerClone the job's clone, which the worker ran in; its origin is CHECKED, never read
//	claimed     the REPO line off the worker's RESULT.md, likewise checked and never trusted
//
// The two worker-writable statements are treated identically, because they are the same
// class: a `git remote set-url origin` in the clone and a `REPO` line in the RESULT.md are
// both a worker saying where it would like its work to go. Agreement is required; neither
// is ever the source. A site with no worker clone to check passes "" and loses nothing --
// the destination never came from there.
func resolveDestination(label string, d dispatch, workerClone, claimed string) (destination, error) {
	if d.repo == "" {
		return destination{}, fmt.Errorf("HARVEST REFUSED repo-unknown card=%s: nothing the manager recorded before this job ran names a repository -- no launch record, no coordinator --clone. The job clone %s may well have an origin, but a worker can rewrite it (git remote set-url; SPEC-SANDBOX 27b), so it is a claim and never the answer, exactly like the RESULT.md's REPO line (SPEC-SWARM: everything a worker writes is data); nothing was pushed",
			field(label), field(workerClone))
	}
	// THE JOB CLONE'S ORIGIN, AS A CLAIM. This is the whole of Johnny's HOLD at 7f692ef6:
	// the line that used to read `origin, from := cloneOrigin(clone), "clone-origin"` and
	// hand that answer straight to the push. It can now only ever disagree.
	if o := cloneOrigin(workerClone); o != "" && o != d.repo {
		return destination{}, fmt.Errorf("HARVEST REFUSED repo-mismatch card=%s dispatched=%s origin=%s -- this job's clone points somewhere other than the repository it was dispatched for; a worker can rewrite its own origin (git remote set-url), so the dispatch record wins and this is refused (SPEC-SWARM: everything a worker writes is data); nothing was pushed",
			label, d.repo, o)
	}
	if c := normalizeRepo(claimed); c != "" && c != d.repo {
		// The grep-able door, identical on every path, with BOTH halves of the
		// disagreement on it: what was dispatched and what the report asked for.
		return destination{}, fmt.Errorf("HARVEST REFUSED repo-mismatch card=%s dispatched=%s claimed=%s -- a RESULT is a worker's report, not an instruction (SPEC-SWARM: everything a worker writes is data); nothing was pushed",
			label, d.repo, c)
	}
	return destination{repo: d.repo, url: githubCloneBase + d.repo + ".git", from: d.from}, nil
}
