package swarm

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/hygiene"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// THE BRIEF CHECKS: WHAT NOVA-SPRINT ADD HOLDS A CARD BRIEF TO AT ITS BASE TIP.
//
// The owner, 2026-10-05, on cards whose work needed files outside the brief's PATHS: "This
// seems like a common failure mode, can we add a check for this to card lint?" Measured on
// the sprint log of epoch 15: 147 distinct brief defects, each costing two or more attempts
// and two reads; 26 work outside PATHS, 24 docs or SPEC left out of PATHS, 13 a dead or wrong
// base, 11 a named TEST that does not fit, 9 a class-test ledger outside PATHS. A brief is
// the coordinator's card for a repository (`REPO:`, `BASE:`, `PATHS:`, `START:`, `TEST:`,
// `WHO:`), read at the tip of its BASE in the lander's clone, so add refuses the defect
// before a friend spends an attempt finding it:
//
//	paths-at-base         the base check of the same name: every PATHS entry names something
//	                      at the BASE tip, or is a new `_test` file, or a NEW: line names it
//	donewhen-test-name    the base check of the same name, read off the TEST line when the brief
//	                      has one: it reads, and its test is absent at the BASE tip in its
//	                      package, so it can be red there; a ledger card (KIND: ledger) whose
//	                      TEST is a class test under internal/ci/ is the one exception, see
//	                      LedgerKind
//	paths-cover-named     every repository path START and THE TASK name as a file to change is
//	                      covered by PATHS or NEW; a START entry marked `(read)` is only read
//	paths-cover-test      the TEST package's `_test` files are covered
//	paths-cover-testdata  a `dir/*.go` entry also covers `dir/testdata/**` when dir has testdata
//	paths-cover-ledgers   a card whose PATHS covers a package's Go code covers that package's
//	                      class-test ledgers under internal/ci/testdata, on SHARED too
//	paths-cover-docs      a card changing a cmd/<tool> verb or flag covers docs/CLI.md and the
//	                      tool's SPEC, on SHARED too
//	base-is-live          BASE is a sprint base origin holds, never a temporary or deleted one
//	tier-set              line 1 names a tier, never `-`
//	tla-is-heavy-or-above  a card whose PATHS covers tla/ is tier heavy or frontier
//	model-gate-named      a card whose PATHS or NEW reaches a .tla or .cfg under tla/ names a
//	                      STEP that runs `make tlc` or `tlacheck` and a GATE-NEEDS line with tla
//	                      (nova-sprint 705fa0b1)
//	spike-paths-research  a KIND: spike or tracer brief names in PATHS and NEW only paths
//	                      under the research roots (ResearchRoots); its code travels as
//	                      perf/experiments/<id>/experiment.patch, never as source
//	paths-cover-registry  a card that adds a C or C++ test source covers each file at the BASE
//	                      tip that registers its sibling tests by name (a Makefile, test/main.c,
//	                      a CMakeLists.txt); a Go test needs no registry
//	who-serves-tier       the friend WHO names (or, for any friend, some friend) serves the tier
//
// NO EVIDENCE IS NOT NEGATIVE EVIDENCE, as the base checks have it: a check that needs the
// tree and was handed none draws its finding with MISSING, naming what is missing. A brief
// with no PATHS: line is no card brief and none of these hold it; one that names no REPO: or
// no BASE: has no base to read, so the checks that need the tree do not run for it (the
// lander's --base decides where it lands, and add cannot know it).

// BriefBaseRemedies is what each brief token wants, in the table shape of CardBaseRemedies and
// beside it: these hold a brief at add (nova-sprint), not a card under `nova-swarm lint`, so
// they are their own table and `nova-swarm lint --rules` does not count them as its checks.
// paths-at-base and donewhen-test-name answer from CardBaseRemedies.
var BriefBaseRemedies = map[string]string{
	"paths-read-marker":     PathsReadRemedy,
	"pr-carry":              "a card that finishes an open pull request starts from the PR's own head, never from its base: add `CARRY: head=<the PR's full head sha> branch=<the PR branch>` to the brief's header (the CARRY: line brief --widen writes, less the attempt), so the slot is staged from the PR head and the worker merges the base itself; add the brief again",
	"paths-cover-named":     "every repository path START: and THE TASK name as a file to change is in PATHS: (or NEW:); mark a START entry the card only reads `(read)`; apply the corrected PATHS: line add printed",
	"paths-cover-test":      "PATHS: covers the TEST package's `_test` files (`<pkg>/*_test.go`, or the new test file by name), since the card lands with the test it names; apply the corrected PATHS: line add printed",
	"paths-cover-testdata":  "a `<dir>/*.go` entry does not reach `<dir>/testdata`; when the package has testdata, PATHS: names `<dir>/testdata/**` too; apply the corrected PATHS: line add printed",
	"paths-cover-ledgers":   "a card that changes a package's Go code can add or remove the functions and error sites its class-test ledgers under internal/ci/testdata count (errcheck, discarded, staticcheck, the dead code ledger); name each such ledger on PATHS: and on SHARED:, since other cards touch it too; apply the corrected lines add printed",
	"paths-cover-docs":      "a card that changes a cmd/<tool> verb or flag changes its documentation: name docs/CLI.md and the tool's SPEC (docs/SPEC-<TOOL>.md) on PATHS: and on SHARED:; apply the corrected lines add printed",
	"base-is-live":          "BASE: is a sprint base origin holds (sprint/<name>, a base the sprint's cards use, or the trunk for the promotion stream; nova-sprint bases lists them), never a temporary branch (tmp/, wip/, scratch/) or one origin no longer holds; re-cut the card on the sprint branch",
	"tier-set":              "line 1 names the card's tier, `tier: flash|pro|heavy|frontier`; a card with no tier, or `-`, was dealt to friends who could not serve it",
	"tla-is-heavy-or-above": "a card whose PATHS: covers tla/ changes a TLA+ model, and a model is the heavy tier's work or above: write `tier: heavy` on line 1 (or `frontier`), or take tla/ out of PATHS: and give the model to its own card",
	// tla-is-frontier is the old token, kept as an alias for one release, naming the same remedy.
	"tla-is-frontier":      "a card whose PATHS: covers tla/ changes a TLA+ model, and a model is the heavy tier's work or above: write `tier: heavy` on line 1 (or `frontier`), or take tla/ out of PATHS: and give the model to its own card",
	"model-gate-named":     ModelGateRemedy,
	"spike-paths-research": "a spike or tracer is never the product: what it keeps is what it taught, not its code, so its KIND: spike or tracer brief names in PATHS: and NEW: only paths under a research root (ResearchRoots: the research docs directory, rnd/, perf/, benchmark/experiments/ or benchmark/findings/); deliver the code as a patch file (NEW: perf/experiments/<id>/experiment.patch beside perf/findings/<id>.md and perf/experiments/<id>/raw.txt) and edit no source file, or cut the production change as its own card with another KIND",
	"paths-cover-registry": "a C or C++ suite builds and runs only the test sources its registry names (a Makefile listing them, test/main.c running each suite, a CMakeLists.txt adding each file), so a card adding a test source changes the registry too: name each registry the finding names on PATHS:; apply the corrected PATHS: line add printed",
	"who-serves-tier":      "the friend WHO: names serves the card's tier (the friend's row's tiers, else the friend's class), or for `WHO: friend` some friend does; name a friend who serves it, raise the tier to one the friend serves, or drop the WHO: line so the fleet deals it",
}

// ModelGate is the sentence internal/cardgen appends to a model card's STEP 4 gate so the
// worker runs the model and merges tla/RUNS.tsv before committing. Ported from nova-sprint
// 705fa0b1.
const ModelGate = "The PATHS reach tla/, so the gate also runs the model: on a Linux TLC bench (java, the pinned jar at /opt/tla/tla2tools.jar; never a working machine), for each group g that `go run ./tools/tlacheck groups --root . --stale` lists (the groups of the cases your edit touched), run `make tlc TLC_JAR=/opt/tla/tla2tools.jar TLC_OUT=$JOB/scratch/tlc-$g TLC_GROUP=$g`, then `go run ./tools/tlacheck merge --root . --keep tla/RUNS.tsv --out tla/RUNS.tsv $JOB/scratch/tlc-*/RUNS.tsv`, until the groups command prints []; commit tla/RUNS.tsv with the change, never a row written by hand. The lander refuses a head that edits tla/*.tla or tla/*.cfg without a current RUNS.tsv row for each case it touches, naming the case."

// ModelGateRemedy is what model-gate-named wants when PATHS or NEW reaches a .tla or .cfg under
// tla/. Ported from nova-sprint 705fa0b1.
const ModelGateRemedy = "when PATHS: or NEW: reaches a .tla or .cfg under tla/, some STEP must run `make tlc` or `tlacheck` and a `GATE-NEEDS:` line must name `tla` (e.g., `GATE-NEEDS: linux, tla`): \"" + ModelGate + "\""

// PathsReadRemedy is the correction for read-only metadata written in PATHS.
const PathsReadRemedy = "move the read-only file to START with (read) and keep only files the card writes in PATHS"

// PathsReadRefusal names the first PATHS entry carrying START's (read) marker
// (docs/SPEC-CARD-CONTRACT.md, the write allowlist). It validates the value,
// without changing the path lists the overlap and TLA gates read.
func PathsReadRefusal(value string) string {
	for _, entry := range strings.Split(value, ",") {
		if strings.Contains(entry, "(read)") {
			return fmt.Sprintf("PATHS entry %q uses (read), which is START metadata; %s", strings.TrimSpace(entry), PathsReadRemedy)
		}
	}
	return ""
}

// LedgerKind is the KIND: of a ledger card, the card nova-card generate cuts from a
// ratchet ledger of internal/ci (internal/cardgen PlanLedger). Its TEST is the ledger's
// class test, green at the base by construction, and its proof is the ledger shrinking
// with that test still green, never a test red at the base. So donewhen-test-name lets a
// TEST that exists at the base pass for such a card, and for no other: only when the brief
// says KIND: ledger AND its TEST names a test under internal/ci/ (classTestPackage).
const LedgerKind = "ledger"

// classTestPackage says pkg, a TEST line's package, is internal/ci or a package under it,
// where the class tests and their ledgers live.
func classTestPackage(pkg string) bool {
	p := strings.TrimPrefix(path.Clean(strings.TrimSpace(pkg)), "./")
	return p == "internal/ci" || strings.HasPrefix(p, "internal/ci/")
}

// ledgerClassTest says the brief is a ledger card whose TEST names a class test: its
// KIND: is LedgerKind and its TEST package is under internal/ci/.
func ledgerClassTest(kind, pkg string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), LedgerKind) && classTestPackage(pkg)
}

// BriefBase is the evidence the brief checks read beside the brief.
type BriefBase struct {
	// Repo is a git repository holding Sha, the commit at the tip of the brief's BASE:
	// (or the commit BASE: pins); Missing says why they were not handed over, "" when they
	// were.
	Repo, Sha, Missing string
	// Gone says origin holds no branch of BASE's name: deleted, or never pushed.
	Gone bool
	// Listed is the bases the sprint's store lists in use (the bases of its cards).
	Listed []string
	// Friends is each friend's tiers, by name; nil when the friends table was not read.
	Friends map[string][]string
}

// BriefFix is the corrected header lines of a brief: each line the checks would change, as it
// would read with every addition the findings ask for, in the order PATHS, NEW, SHARED, line 1.
type BriefFix []string

// LintBrief holds one card brief to the brief checks at its base, in the order the tokens are
// listed above, and returns the corrected header lines that answer the PATHS, NEW and SHARED
// findings in one step.
func LintBrief(raw []byte, bb BriefBase) ([]CardHeaderFinding, BriefFix) {
	h, _ := cardHeaderBlock(raw)
	var out []CardHeaderFinding
	add := func(check string, line int, excerpt string) {
		out = append(out, CardHeaderFinding{Check: check, Line: max(line, 1), Excerpt: excerpt})
	}
	// 0. pr-carry: a card that finishes an open pull request stages from the PR's own head,
	// not from its base. The head rides the brief's CARRY: line (the one brief --widen
	// writes, `CARRY: <card> attempt <n> head=<sha>`, or a finish-PR card's
	// `CARRY: head=<sha> branch=<pr branch>`); a brief that names a PR and carries none is
	// refused here, before a worker spends an attempt on the base and comes back one commit
	// off it. This holds a brief with no PATHS: too: it is the card's whole staging.
	if namesAFinishPR(raw, h) {
		if _, ok := member.CarryOf(string(raw)); !ok {
			line := 1
			if f := h[PRHeaderKey]; f.found {
				line = f.line
			}
			add("pr-carry", line, "this card finishes an open pull request and its brief carries no CARRY: line, so its slot stages from the base and not from the PR's head; add `CARRY: head=<the PR's full head sha> branch=<the PR branch>` to the brief's header (the line brief --widen writes, less the attempt), then add the brief again")
		}
	}
	// 10b. spike-paths-research, run before the PATHS: return: a spike may name its files on
	// NEW: alone (the fixed3d spikes do), and production code on NEW: is refused all the same.
	// Ported from nova-sprint 39132f64.
	if line, outside := spikeOutsideResearch(h); len(outside) > 0 {
		add("spike-paths-research", line, fmt.Sprintf("KIND %s names %s outside the research roots (%s); a spike's code travels as perf/experiments/<id>/experiment.patch", strings.TrimSpace(h["KIND"].value), quoteDepends(outside), strings.Join(ResearchRoots, ", ")))
	}
	pathsF := h["PATHS"]
	if !pathsF.found {
		return out, nil
	}
	if why := PathsReadRefusal(pathsF.value); why != "" {
		add("paths-read-marker", pathsF.line, why)
		return out, nil
	}
	paths := headerList(pathsF.value)
	news := headerList(h["NEW"].value)
	shared := headerList(h["SHARED"].value)
	scope := append(slices.Clone(paths), news...)
	var addPaths, addNew, addShared []string
	cb := ReadCardBase(raw)
	model, _ := cardhdr.ReadModel(string(raw))

	// the tree at the base, when it was handed over
	var files []string
	treeWhy := ""
	switch {
	case cb.Ref == "" || cb.Named == "":
		treeWhy = "-" // no base to read: the tree checks do not run
	case bb.Missing != "":
		treeWhy = "MISSING: " + bb.Missing
	case bb.Repo == "" || !fullHexSHA(bb.Sha):
		treeWhy = "MISSING: no repository at the BASE tip was handed over, so the brief was not read against " + cb.Ref
	default:
		list, err := baseGit(bb.Repo, "ls-tree", "-r", "--name-only", "-z", "--end-of-options", bb.Sha)
		if err != nil {
			treeWhy = fmt.Sprintf("MISSING: could not list the tree at %s (%s): %v", short12(bb.Sha), cb.Ref, err)
			break
		}
		for _, f := range strings.Split(list, "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	}
	tree := treeWhy == ""
	at := func() string { return short12(bb.Sha) + " (" + cb.Ref + ")" }
	missingFinding := func(check string, line int) {
		if treeWhy != "-" {
			add(check, line, treeWhy)
		}
	}

	// 1. paths-at-base.
	if tree {
		var miss []string
		for _, e := range entriesMissing(files, paths) {
			if !answeredByNew(e, news) {
				miss = append(miss, e)
			}
		}
		if len(miss) > 0 {
			add("paths-at-base", pathsF.line, fmt.Sprintf("PATHS %s names nothing at %s; a file the card creates is named on a NEW: line, a path that is wrong is corrected", quoteDepends(miss), at()))
			addNew = append(addNew, miss...)
		}
	} else {
		missingFinding("paths-at-base", pathsF.line)
	}

	// 2. donewhen-test-name, read off the TEST line.
	testF := h["TEST"]
	tl, testWhy := cardhdr.ParseTest(testF.value)
	dt, runnerForm := runnerDoneTest(testF.value)
	checked := false
	switch {
	case !testF.found: // a brief with no TEST line is held to it where the copy wrapper reads it
		checked = true
	case runnerForm:
		if why, _ := runnerTestLine(testF.value); why != "" {
			add("donewhen-test-name", testF.line, why)
			checked = true
		}
	case testWhy != "":
		add("donewhen-test-name", testF.line, testWhy)
		checked = true
	case tl.None:
		checked = true
	default:
		dt.runner = tl.Runner
		if dt.runner == "" {
			dt.runner = "go"
		}
		dt.name = tl.Name
		if dt.runner == "go" {
			dt.scope = []string{goPkgArg(tl.Package)}
		}
	}
	if !checked {
		if !tree {
			missingFinding("donewhen-test-name", testF.line)
		} else {
			if dt.runner == "ctest" && len(dt.scope) == 0 {
				if _, err := baseGit(bb.Repo, "cat-file", "-e", "--end-of-options", bb.Sha+":CMakeLists.txt"); err != nil {
					add("donewhen-test-name", testF.line, fmt.Sprintf("TEST: ctest %s names a CMake test, and %s holds no CMakeLists.txt at the base", dt.name, at()))
					checked = true
				}
			}
			// the make target must exist at the base (nova-sprint 94736cdb, landed 73b04e12)
			if !checked && dt.runner == "make" && len(dt.scope) > 0 {
				missingTarget := ""
				for _, sc := range dt.scope {
					sc = strings.TrimPrefix(sc, "./")
					if _, err := baseGit(bb.Repo, "cat-file", "-e", "--end-of-options", bb.Sha+":"+sc); err != nil {
						missingTarget = sc
						break
					}
				}
				if missingTarget != "" {
					add("donewhen-test-name", testF.line, fmt.Sprintf("TEST: make %s %s names target/file %s, and %s holds no such file at the base; the target must exist", dt.scope[0], dt.name, missingTarget, at()))
					checked = true
				}
			}
			if !checked {
				present, err := testDefinedAt(bb.Repo, bb.Sha, dt)
				switch {
				case err != nil:
					add("donewhen-test-name", testF.line, fmt.Sprintf("MISSING: could not search the tree at %s for %s: %v", at(), dt.name, err))
				case present && ledgerClassTest(h["KIND"].value, tl.Package):
					// a ledger card: its class test is green at the base by construction (LedgerKind)
				case present:
					where := ""
					if dt.runner == "go" {
						where = tl.Package + " "
					}
					if len(dt.scope) > 0 {
						where = dt.scope[0] + " "
					}
					add("donewhen-test-name", testF.line, fmt.Sprintf("TEST %s exists in %sat %s, so it cannot be red there; name the new test the card adds", dt.name, where, at()))
				}
			}
		}
	}

	// 3. paths-cover-named: START, then THE TASK.
	isDir := func(p string) bool {
		if tree {
			return !slices.Contains(files, p) && slices.ContainsFunc(files, func(f string) bool { return strings.HasPrefix(f, p+"/") })
		}
		return path.Ext(p) == ""
	}
	var named []string
	startF := h["START"]
	for _, e := range startEntries(startF.value) {
		if e.read || !repoPathRE.MatchString(e.path) {
			continue
		}
		if isDir(e.path) {
			if !coversDir(scope, e.path) {
				named = append(named, e.path+"/*.go")
			}
			continue
		}
		if !covers(scope, e.path) {
			named = append(named, e.path)
		}
	}
	if len(named) > 0 {
		add("paths-cover-named", startF.line, fmt.Sprintf("START names %s to change, and PATHS does not cover it; an entry the card only reads is marked (read)", quoteDepends(named)))
		addPaths = append(addPaths, named...)
	}
	taskLine, task := taskParagraph(raw)
	if task != "" {
		if tree {
			var inTask []string
			for _, p := range taskPaths(task) {
				if slices.Contains(files, p) && !covers(scope, p) && !slices.Contains(inTask, p) && !slices.Contains(named, p) {
					inTask = append(inTask, p)
				}
			}
			if len(inTask) > 0 {
				add("paths-cover-named", taskLine, fmt.Sprintf("THE TASK names %s, a file at %s, and PATHS does not cover it; a file the task only reads is marked (read) after it", quoteDepends(inTask), at()))
				addPaths = append(addPaths, inTask...)
			}
		} else {
			missingFinding("paths-cover-named", taskLine)
		}
	}

	// 4. paths-cover-test.
	if testF.found && testWhy == "" && !tl.None {
		dir := strings.TrimSuffix(strings.TrimPrefix(path.Clean(tl.Package), "./"), "/")
		if dir == "." {
			dir = ""
		}
		if !coversTests(scope, dir) {
			want := path.Join(dir, "*_test.go")
			add("paths-cover-test", testF.line, fmt.Sprintf("TEST %s is in %s, and PATHS covers none of its _test files; the card lands with the test it names", tl.Name, orElse(dir, ".")))
			addPaths = append(addPaths, want)
		}
	}

	// 5. paths-cover-testdata.
	if tree {
		var want []string
		for _, e := range paths {
			e = cleanEntry(e)
			dir, base := path.Split(e)
			dir = strings.TrimSuffix(dir, "/")
			if base != "*.go" || dir == "" {
				continue
			}
			td := dir + "/testdata"
			if !slices.ContainsFunc(files, func(f string) bool { return strings.HasPrefix(f, td+"/") }) {
				continue
			}
			if !slices.ContainsFunc(files, func(f string) bool { return strings.HasPrefix(f, td+"/") && covers(scope, f) }) && !slices.Contains(want, td+"/**") {
				want = append(want, td+"/**")
			}
		}
		if len(want) > 0 {
			add("paths-cover-testdata", pathsF.line, fmt.Sprintf("PATHS covers the Go files of a package with testdata at %s and not its testdata: %s", at(), quoteDepends(want)))
			addPaths = append(addPaths, want...)
		}
	} else if slices.ContainsFunc(paths, func(e string) bool { return strings.HasSuffix(cleanEntry(e), "/*.go") }) {
		missingFinding("paths-cover-testdata", pathsF.line)
	}

	// 6. paths-cover-ledgers.
	if tree {
		var want []string
		for _, pkg := range codePackages(files, scope, news) {
			for _, l := range packageLedgers(bb, files, pkg) {
				if !slices.Contains(want, l) {
					want = append(want, l)
				}
			}
		}
		var outP, outS []string
		for _, l := range want {
			if !covers(scope, l) {
				outP = append(outP, l)
			}
			if !covers(shared, l) {
				outS = append(outS, l)
			}
		}
		if len(outP)+len(outS) > 0 {
			add("paths-cover-ledgers", pathsF.line, fmt.Sprintf("PATHS covers Go code of a package its class-test ledgers count, and %s is not on both PATHS and SHARED", quoteDepends(uniq(append(slices.Clone(outP), outS...)))))
			addPaths, addShared = append(addPaths, outP...), append(addShared, outS...)
		}
	} else if len(codePackages(nil, scope, news)) > 0 {
		missingFinding("paths-cover-ledgers", pathsF.line)
	}

	// 7. paths-cover-docs.
	if tools := verbTools(scope, task); len(tools) > 0 {
		want := []string{"docs/CLI.md"}
		for _, tool := range tools {
			if spec := toolSpec(tool, files, tree); spec != "" {
				want = append(want, spec)
			}
		}
		var outP, outS []string
		for _, d := range want {
			if !covers(scope, d) {
				outP = append(outP, d)
			}
			if !covers(shared, d) {
				outS = append(outS, d)
			}
		}
		if len(outP)+len(outS) > 0 {
			add("paths-cover-docs", pathsF.line, fmt.Sprintf("the card changes a verb or flag of %s, and %s is not on both PATHS and SHARED", strings.Join(tools, ", "), quoteDepends(uniq(append(slices.Clone(outP), outS...)))))
			addPaths, addShared = append(addPaths, outP...), append(addShared, outS...)
		}
	}

	// 8. base-is-live.
	if cb.Ref != "" && cb.Named != "" {
		baseF := h["BASE"]
		switch {
		case bb.Gone:
			add("base-is-live", baseF.line, fmt.Sprintf("BASE %s is no branch of origin %s: deleted, or never pushed", cb.Ref, cb.Named))
		case bb.Missing != "":
			add("base-is-live", baseF.line, "MISSING: "+bb.Missing)
		case temporaryBase(cb.Ref):
			add("base-is-live", baseF.line, fmt.Sprintf("BASE %s is a temporary branch; a card is cut on a sprint base", cb.Ref))
		case !liveBase(cb.Ref, bb.Listed):
			listed := "none"
			if len(bb.Listed) > 0 {
				listed = strings.Join(bb.Listed, ", ")
			}
			add("base-is-live", baseF.line, fmt.Sprintf("BASE %s is no sprint base: not sprint/<name>, not the trunk, and not a base the sprint's cards use (%s)", cb.Ref, listed))
		}
	}

	// 9. tier-set.
	if model.Tier == "" || model.Tier == "-" {
		add("tier-set", 1, "line 1 names no tier (`tier: -` is none); a card with no tier was dealt to friends who could not serve it")
	}

	// 10. tla-is-heavy-or-above.
	if model.Tier != cardhdr.RouteHeavy && model.Tier != cardhdr.RouteFrontier && slices.ContainsFunc(scope, func(e string) bool {
		e = cleanEntry(e)
		return e == "tla" || strings.HasPrefix(e, "tla/") || hygiene.MatchGlob(e, "tla/Model.tla")
	}) {
		add("tla-is-heavy-or-above", 1, fmt.Sprintf("PATHS covers tla/, a TLA+ model, and line 1 names tier %s, not heavy or frontier", orElse(model.Tier, "-")))
	}

	// 10a. model-gate-named. Ported from nova-sprint 705fa0b1.
	if line, why := modelGateMissing(raw, h); why != "" {
		add("model-gate-named", line, why)
	}

	// 10b. spike-paths-research runs above, before the PATHS: return (spikeOutsideResearch).

	// 10c. paths-cover-registry. Ported from nova-sprint 80008ad8.
	if added := addedTestSources(files, tree, paths, news); len(added) > 0 {
		if tree {
			var want []string
			for _, src := range added {
				for _, r := range testRegistries(bb, files, src) {
					if !covers(paths, r) && !slices.Contains(want, r) {
						want = append(want, r)
					}
				}
			}
			if len(want) > 0 {
				add("paths-cover-registry", pathsF.line, fmt.Sprintf("the card adds the test source %s, and %s at %s names its sibling tests, so the card changes it too; PATHS does not cover it", quoteDepends(added), quoteDepends(want), at()))
				addPaths = append(addPaths, want...)
			}
		} else {
			missingFinding("paths-cover-registry", pathsF.line)
		}
	}

	// 11. who-serves-tier.
	if w, why := cardhdr.ReadWho(string(raw)); why == "" && w.Friend && model.Tier != "" && model.Tier != "-" {
		switch {
		case bb.Friends == nil:
			add("who-serves-tier", 1, "MISSING: the friends table was not read, so the WHO line's friend was not held to tier "+model.Tier)
		case w.Name != "":
			if tiers, ok := bb.Friends[w.Name]; ok && !slices.Contains(tiers, model.Tier) {
				add("who-serves-tier", 1, fmt.Sprintf("WHO names friend %s, who serves %s, and the card is tier %s", w.Name, orElse(strings.Join(tiers, ","), "no tier"), model.Tier))
			}
		default:
			serves := false
			for _, tiers := range bb.Friends {
				serves = serves || slices.Contains(tiers, model.Tier)
			}
			if !serves {
				add("who-serves-tier", 1, fmt.Sprintf("WHO names any friend, and no friend of the table serves tier %s", model.Tier))
			}
		}
	}

	var fix BriefFix
	if addPaths = notIn(uniq(addPaths), paths); len(addPaths) > 0 {
		fix = append(fix, "PATHS: "+strings.Join(append(slices.Clone(paths), addPaths...), ","))
	}
	if addNew = notIn(uniq(addNew), news); len(addNew) > 0 {
		fix = append(fix, "NEW: "+strings.Join(append(slices.Clone(news), addNew...), ","))
	}
	if addShared = notIn(uniq(addShared), shared); len(addShared) > 0 {
		fix = append(fix, "SHARED: "+strings.Join(append(slices.Clone(shared), addShared...), ","))
	}
	if slices.ContainsFunc(out, func(f CardHeaderFinding) bool { return f.Check == "tla-is-heavy-or-above" }) {
		fix = append(fix, heavyLine(firstLine(raw)))
	}
	return out, fix
}

// modelStepToolRE matches the model runner a STEP names (`make tlc` or `tlacheck`).
// Ported from nova-sprint 705fa0b1.
var modelStepToolRE = regexp.MustCompile(`\bmake\s+(?:-\S+\s+)*tlc\b|\btlacheck\b`)

// reachesModelFile says the PATHS or NEW entry e reaches a .tla or .cfg under tla/. Ported
// from nova-sprint 705fa0b1, reading the entry with nova-tools' covers.
func reachesModelFile(e string) bool {
	e = cleanEntry(e)
	one := []string{e}
	if covers(one, "tla/M.tla") || covers(one, "tla/MCM.cfg") {
		return true
	}
	return strings.HasPrefix(e, "tla/") && (path.Ext(e) == ".tla" || path.Ext(e) == ".cfg")
}

// modelTouchLine is the line of the first PATHS or NEW header whose entries reach a .tla or
// .cfg under tla/; 0 when neither does. Ported from nova-sprint 705fa0b1.
func modelTouchLine(h map[string]headerField) int {
	for _, key := range []string{"PATHS", "NEW"} {
		f := h[key]
		if !f.found {
			continue
		}
		if slices.ContainsFunc(headerList(f.value), reachesModelFile) {
			return f.line
		}
	}
	return 0
}

// stepRunsModel says the brief has at least one STEP line (steps > 0) and whether some STEP
// paragraph runs `make tlc` or `tlacheck`. Ported from nova-sprint 705fa0b1.
func stepRunsModel(raw []byte) (steps int, runs bool) {
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "STEP ") || strings.HasPrefix(t, "STEP\t") || strings.HasPrefix(t, "STEP:") || strings.HasPrefix(t, "STEP."):
			steps++
			in = true
		case t == "" || taskHeadRE.MatchString(t):
			in = false
		}
		if in && modelStepToolRE.MatchString(t) {
			runs = true
		}
	}
	return steps, runs
}

// gateNeedsTLA says a GATE-NEEDS header line names tla. Ported from nova-sprint 705fa0b1.
func gateNeedsTLA(raw []byte, h map[string]headerField) bool {
	if slices.ContainsFunc(headerList(h["GATE-NEEDS"].value), func(n string) bool {
		return strings.EqualFold(n, "tla")
	}) {
		return true
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := cardhdr.KeyValue(line); ok && k == "GATE-NEEDS" {
			if slices.ContainsFunc(headerList(v), func(n string) bool { return strings.EqualFold(n, "tla") }) {
				return true
			}
		}
	}
	return false
}

// modelGateMissing returns the line and refusal for model-gate-named when PATHS or NEW reaches
// a .tla or .cfg under tla/ and the brief omits either a STEP running `make tlc` or `tlacheck`
// or a GATE-NEEDS line naming `tla`. A header-only stub with no STEP lines and no REPO, BASE,
// STOP, GATE-NEEDS or NEW header is not a stepped brief and is not held here. Ported from
// nova-sprint 705fa0b1.
func modelGateMissing(raw []byte, h map[string]headerField) (int, string) {
	line := modelTouchLine(h)
	if line == 0 {
		return 0, ""
	}
	steps, hasTool := stepRunsModel(raw)
	if steps == 0 && !h["REPO"].found && !h["BASE"].found && !h["STOP"].found && !h["GATE-NEEDS"].found && !h["NEW"].found {
		return 0, ""
	}
	hasGateNeeds := gateNeedsTLA(raw, h)
	if !hasGateNeeds && hasTool && !h["REPO"].found && !h["BASE"].found && !h["GATE-NEEDS"].found {
		if _, task := taskParagraph(raw); task == "" && strings.Contains(string(raw), "on a Linux bench, then: /tmp/tlacheck merge") {
			hasGateNeeds = true
		}
	}
	if hasTool && hasGateNeeds {
		return 0, ""
	}
	var miss []string
	if !hasTool {
		miss = append(miss, "no STEP runs `make tlc` or `tlacheck`")
	}
	if !hasGateNeeds {
		miss = append(miss, "no GATE-NEEDS line names `tla`")
	}
	return line, fmt.Sprintf("PATHS or NEW reaches a .tla or .cfg under tla/, and %s; %s", strings.Join(miss, " and "), ModelGateRemedy)
}

// ResearchRoots are the directories a KIND: spike or tracer brief may name in PATHS: and NEW:
// (spike-paths-research). The owner's tracer-bullet rule: a tracer is never the product; what is
// kept is what it taught, not its code. In sprint 21 two schema tracers named production
// packages and one landed; the fixed3d spikes named only perf/ and shipped their code as an
// experiment.patch.
var ResearchRoots = []string{"docs/research/", "rnd/", "perf/", "benchmark/experiments/", "benchmark/findings/"}

// spikeKind says a KIND: value is a spike or a tracer, in any case.
func spikeKind(kind string) bool {
	k := strings.ToLower(strings.TrimSpace(kind))
	return k == "spike" || k == "tracer"
}

// underResearchRoot says the PATHS or NEW entry e lies under one of ResearchRoots.
func underResearchRoot(e string) bool {
	e = cleanEntry(e)
	return slices.ContainsFunc(ResearchRoots, func(r string) bool {
		return strings.HasPrefix(e, r) || e == strings.TrimSuffix(r, "/")
	})
}

// spikeOutsideResearch is, for a KIND: spike or tracer brief, every PATHS and NEW entry
// outside ResearchRoots (as written), with the line of the first header that names one; nil
// for any other brief.
func spikeOutsideResearch(h map[string]headerField) (int, []string) {
	if !spikeKind(h["KIND"].value) {
		return 0, nil
	}
	var outside []string
	line := 0
	for _, key := range []string{"PATHS", "NEW"} {
		f := h[key]
		if !f.found {
			continue
		}
		for _, e := range headerList(f.value) {
			if !underResearchRoot(e) {
				outside = append(outside, e)
				if line == 0 {
					line = f.line
				}
			}
		}
	}
	return line, outside
}

// testSourceExts are the extensions of a C or C++ source a test registry lists.
var testSourceExts = []string{".c", ".cc", ".cpp", ".cxx"}

// testSource says p is a C or C++ test source: a C or C++ file under a test/ or tests/
// directory, or one named *_test.* or test_*.*. A Go test is none: go test finds it alone.
func testSource(p string) bool {
	p = cleanEntry(p)
	if strings.ContainsAny(p, "*?[") || !slices.Contains(testSourceExts, path.Ext(p)) {
		return false
	}
	stem := strings.TrimSuffix(path.Base(p), path.Ext(p))
	if strings.HasSuffix(stem, "_test") || strings.HasPrefix(stem, "test_") {
		return true
	}
	return slices.ContainsFunc(strings.Split(path.Dir(p), "/"), func(seg string) bool { return seg == "test" || seg == "tests" })
}

// addedTestSources is every test source the card adds: a NEW entry that is one, and a PATHS
// entry that is one and that the tree at the base does not hold. With no tree, every test
// source PATHS or NEW names, since none can be shown to exist already.
func addedTestSources(files []string, tree bool, paths, news []string) []string {
	var out []string
	for _, e := range append(slices.Clone(news), paths...) {
		e = cleanEntry(e)
		if !testSource(e) || slices.Contains(out, e) {
			continue
		}
		if tree && slices.Contains(files, e) && !slices.ContainsFunc(news, func(n string) bool { return cleanEntry(n) == e }) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// registryNames are the base names of a file that registers test sources: a build file in
// the test source's directory or any directory above it, or a test runner's main beside it.
var (
	registryBuildNames = []string{"Makefile", "makefile", "GNUmakefile", "CMakeLists.txt"}
	registryMainNames  = []string{"main.c", "main.cc", "main.cpp", "main.cxx"}
)

// testRegistries is every file at the base that registers the test source src's siblings:
// a build file of src's directory or one above it, or a main beside src, whose text names a
// test source of src's directory (src itself aside) by file name or by stem, as a whole word.
// None when the directory holds no other test source, or nothing names one (a glob builds
// them all).
func testRegistries(bb BriefBase, files []string, src string) []string {
	dir := path.Dir(src)
	var siblings []string
	for _, f := range files {
		if path.Dir(f) == dir && f != src && testSource(f) && !slices.Contains(registryMainNames, path.Base(f)) {
			siblings = append(siblings, f)
		}
	}
	if len(siblings) == 0 {
		return nil
	}
	var cands []string
	for _, n := range registryMainNames {
		cands = append(cands, path.Join(dir, n))
	}
	for d := dir; ; d = path.Dir(d) {
		for _, n := range registryBuildNames {
			cands = append(cands, path.Join(d, n))
		}
		if d == "." || d == "/" {
			break
		}
	}
	var pats []*regexp.Regexp
	for _, s := range siblings {
		base := path.Base(s)
		stem := strings.TrimSuffix(base, path.Ext(base))
		pats = append(pats, regexp.MustCompile(`(^|[^A-Za-z0-9_])(`+regexp.QuoteMeta(base)+`|`+regexp.QuoteMeta(stem)+`)($|[^A-Za-z0-9_])`))
	}
	var out []string
	for _, c := range cands {
		if !slices.Contains(files, c) || slices.Contains(out, c) {
			continue
		}
		text, err := baseGit(bb.Repo, "show", "--end-of-options", bb.Sha+":"+c)
		if err != nil {
			continue
		}
		if slices.ContainsFunc(pats, func(re *regexp.Regexp) bool { return re.MatchString(text) }) {
			out = append(out, c)
		}
	}
	return out
}

// BriefRemedy is what a brief token wants: BriefBaseRemedies, else CardBaseRemedies.
func BriefRemedy(check string) string {
	if r, ok := BriefBaseRemedies[check]; ok {
		return r
	}
	return CardBaseRemedies[check]
}

// headerList is a PATHS-shaped value's entries, commas or blanks between them; none and -
// name nothing.
func headerList(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if p != "none" && p != "-" {
			out = append(out, p)
		}
	}
	return out
}

// cleanEntry is a PATHS entry as the matcher reads it: no leading ./, no trailing /.
func cleanEntry(e string) string {
	return strings.TrimPrefix(strings.TrimSuffix(e, "/"), "./")
}

// entriesMissing is every entry of entries that names nothing in files (pathsMissingAt's rule).
func entriesMissing(files, entries []string) []string {
	var miss []string
	for _, e := range entries {
		e = cleanEntry(e)
		if newTestFile(e) {
			continue
		}
		if !slices.ContainsFunc(files, func(f string) bool { return f == e || strings.HasPrefix(f, e+"/") || hygiene.MatchGlob(e, f) }) {
			miss = append(miss, e)
		}
	}
	return miss
}

// answeredByNew says a NEW: entry names the PATHS entry e: the same path, or one glob matching
// the other.
func answeredByNew(e string, news []string) bool {
	return slices.ContainsFunc(news, func(n string) bool {
		n = cleanEntry(n)
		return n == e || hygiene.MatchGlob(e, n) || hygiene.MatchGlob(n, e)
	})
}

// covers says one entry of scope names the file p: the file, a directory above it, or a glob.
func covers(scope []string, p string) bool {
	return slices.ContainsFunc(scope, func(e string) bool {
		e = cleanEntry(e)
		return e == p || strings.HasPrefix(p, e+"/") || hygiene.MatchGlob(e, p)
	})
}

// coversDir says scope reaches into the directory dir: it names dir, a directory above it, or
// a file or glob inside it.
func coversDir(scope []string, dir string) bool {
	return slices.ContainsFunc(scope, func(e string) bool {
		e = cleanEntry(e)
		return e == dir || strings.HasPrefix(dir, e+"/") || strings.HasPrefix(e, dir+"/") || hygiene.MatchGlob(e, dir+"/x.go")
	})
}

// coversTests says scope covers a `_test.go` file of the package in dir ("" the root): the
// package's tests by glob or directory, or one test file named.
func coversTests(scope []string, dir string) bool {
	probe := path.Join(dir, "zz_probe_test.go")
	return slices.ContainsFunc(scope, func(e string) bool {
		e = cleanEntry(e)
		if strings.HasSuffix(e, "_test.go") && path.Dir(e) == orElse(dir, ".") {
			return true
		}
		return covers([]string{e}, probe)
	})
}

// startEntry is one START entry: its path, and whether it is marked `(read)`.
type startEntry struct {
	path string
	read bool
}

// startEntries reads a START value: entries split on commas outside parentheses, each its
// first word (the path) and, in parentheses after it, what the card does there; `(read)`
// marks a file the card only reads.
func startEntries(v string) []startEntry {
	var out []startEntry
	depth, from := 0, 0
	flush := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		f := strings.Fields(s)
		if len(f) == 0 {
			return
		}
		p := strings.Trim(f[0], "`")
		note := ""
		if i := strings.Index(s, "("); i >= 0 {
			note = strings.TrimSuffix(strings.TrimSpace(s[i+1:]), ")")
		}
		out = append(out, startEntry{path: cleanEntry(p), read: strings.EqualFold(strings.TrimSpace(note), "read")})
	}
	for i, r := range v {
		switch r {
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case ',':
			if depth == 0 {
				flush(v[from:i])
				from = i + 1
			}
		}
	}
	flush(v[from:])
	return out
}

// repoPathRE is a repository path a brief names: segments joined by slashes, or one file name
// with an extension; a bare word (a tool, a verb) is no path.
var repoPathRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.*-]+)+$|^[A-Za-z0-9_-]+\.[A-Za-z0-9]+$`)

// taskHeadRE opens a paragraph a brief is cut into: `THE TASK.`, `STEP 1.`, `RULES.`,
// `COORDINATOR NOTE (...)`: words in capitals, then a stop, a colon or a parenthesis.
var taskHeadRE = regexp.MustCompile(`^(STEP[ \t]+[0-9]|[A-Z][A-Z -]{2,}[.:(])`)

// taskParagraph is THE TASK paragraph of a brief and the line it begins on: from its line to
// the next paragraph head; "" when the brief has none.
func taskParagraph(raw []byte) (int, string) {
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "THE TASK") {
			continue
		}
		var b strings.Builder
		b.WriteString(strings.TrimPrefix(strings.TrimPrefix(l, "THE TASK"), "."))
		for _, m := range lines[i+1:] {
			if taskHeadRE.MatchString(m) {
				break
			}
			b.WriteString("\n" + m)
		}
		return i + 1, b.String()
	}
	return 0, ""
}

// PRHeaderKey is the header line a card that finishes an open pull request carries:
// `PR: <owner>/<repo>#<n>`. Add and brief read it (with THE TASK's prose below) only to
// refuse a brief that names a PR and carries no CARRY: line, so the slot stages from the
// PR's head (docs/SPEC-SPRINT.md, the brief checks).
const PRHeaderKey = "PR"

// NamesAFinishPR says the brief is a card that finishes an open pull request: its header
// carries a PR: line, or THE TASK names the PR's branch it starts from. The prose the first
// finish-PR cards were staged with is what the lint catches, so the generator's CARRY: line
// is required whether or not it writes a PR: line. add reads it to hold such a brief even
// with no PATHS: line (the PR carry is the card's whole staging), and LintBrief runs the
// pr-carry check on every brief it is handed.
func NamesAFinishPR(raw []byte) bool {
	h, _ := cardHeaderBlock(raw)
	return namesAFinishPR(raw, h)
}

// namesAFinishPR is NamesAFinishPR with the block already read.
func namesAFinishPR(raw []byte, h map[string]headerField) bool {
	if f := h[PRHeaderKey]; f.found {
		return true
	}
	_, task := taskParagraph(raw)
	return strings.Contains(strings.ToLower(task), "start from the pr's branch")
}

// taskPathRE is a path-shaped word in prose; readMarkRE is `(read)` right after one.
var (
	taskPathRE = regexp.MustCompile("[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+")
	readMarkRE = regexp.MustCompile(`^[` + "`" + `"']?[ \t]*\(read\)`)
)

// taskPaths is every path-shaped word of the task text not marked `(read)` after it, its
// trailing stop cut; the caller keeps those the tree holds as files.
func taskPaths(task string) []string {
	var out []string
	for _, m := range taskPathRE.FindAllStringIndex(task, -1) {
		p := strings.TrimRight(task[m[0]:m[1]], ".")
		if readMarkRE.MatchString(task[m[1]:]) {
			continue
		}
		out = append(out, cleanEntry(p))
	}
	return out
}

// codePackages is every directory whose Go code (a `.go` file that is no test) scope covers:
// a file of the tree it names, or a NEW one; with no tree, read off the entries alone.
func codePackages(files, scope, news []string) []string {
	var out []string
	addPkg := func(f string) {
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") {
			if d := path.Dir(f); !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	if files == nil {
		for _, e := range scope {
			e = cleanEntry(e)
			if strings.HasSuffix(e, ".go") && !strings.HasSuffix(e, "_test.go") {
				addPkg(e)
			}
		}
		return out
	}
	for _, f := range files {
		if covers(scope, f) {
			addPkg(f)
		}
	}
	for _, n := range news {
		if n = cleanEntry(n); !strings.ContainsAny(n, "*?[") {
			addPkg(n)
		}
	}
	return out
}

// ledgerRoot is where the class-test ledgers live, and ledgerKinds the per-package ledgers a
// change to a package's functions or error sites moves: <root>/<kind>/<package>.txt.
const ledgerRoot = "internal/ci/testdata"

var ledgerKinds = []string{"errcheck", "discarded", "staticcheck"}

// deadCodeLedger is the row ledger of unreachable functions: `<package> <count>` per row.
const deadCodeLedger = ledgerRoot + "/dead_code_allowlist.txt"

// packageLedgers is every class-test ledger at the base that counts the package pkg.
func packageLedgers(bb BriefBase, files []string, pkg string) []string {
	var out []string
	for _, k := range ledgerKinds {
		if l := ledgerRoot + "/" + k + "/" + pkg + ".txt"; slices.Contains(files, l) {
			out = append(out, l)
		}
	}
	if slices.Contains(files, deadCodeLedger) {
		if text, err := baseGit(bb.Repo, "show", "--end-of-options", bb.Sha+":"+deadCodeLedger); err == nil {
			for _, row := range strings.Split(text, "\n") {
				if f := strings.Fields(row); len(f) >= 2 && f[0] == pkg {
					out = append(out, deadCodeLedger)
					break
				}
			}
		}
	}
	return out
}

// flagWordRE is the task naming a verb or a flag: the words, or a `--flag`.
var flagWordRE = regexp.MustCompile(`(?i)\b(verbs?|flags?)\b|(^|[\s` + "`" + `(])--[a-z][a-z0-9-]+`)

// verbTools is each cmd/<tool> whose Go code scope covers, when the task names a verb or a
// flag; none when it names neither.
func verbTools(scope []string, task string) []string {
	if !flagWordRE.MatchString(task) {
		return nil
	}
	var out []string
	for _, e := range scope {
		e = cleanEntry(e)
		rest, ok := strings.CutPrefix(e, "cmd/")
		if !ok || strings.HasSuffix(e, "_test.go") {
			continue
		}
		tool, file, _ := strings.Cut(rest, "/")
		if tool == "" || strings.ContainsAny(tool, "*?[") || file != "" && !strings.HasSuffix(file, ".go") && file != "*" && file != "**" {
			continue
		}
		if !slices.Contains(out, tool) {
			out = append(out, tool)
		}
	}
	return out
}

// toolSpec is the SPEC of a tool: docs/SPEC-<TOOL>.md, the tool's name upper case without its
// nova- prefix, else with it (SPEC-NOVA-DECIDE.md); "" when the tree holds neither. With no
// tree, the first spelling.
func toolSpec(tool string, files []string, tree bool) string {
	short := "docs/SPEC-" + strings.ToUpper(strings.TrimPrefix(tool, "nova-")) + ".md"
	long := "docs/SPEC-" + strings.ToUpper(tool) + ".md"
	switch {
	case !tree:
		return short
	case slices.Contains(files, short):
		return short
	case slices.Contains(files, long):
		return long
	}
	return ""
}

// trunkBases are the bases a card is cut on outside the sprint branches: the trunk, which the
// sprint-branch rule keeps to the promotion stream.
var trunkBases = []string{"dev", "main", "master"}

// liveBase says ref is a sprint base: sprint/<name>, the trunk, or a base the store lists.
func liveBase(ref string, listed []string) bool {
	return strings.HasPrefix(ref, "sprint/") || slices.Contains(trunkBases, ref) || slices.Contains(listed, ref)
}

// temporaryBase says ref is a branch kept for a while and thrown away: tmp/, temp/, wip/,
// scratch/, or a name ending -tmp or -wip.
func temporaryBase(ref string) bool {
	first, _, _ := strings.Cut(ref, "/")
	switch strings.ToLower(first) {
	case "tmp", "temp", "wip", "scratch":
		return true
	}
	l := strings.ToLower(ref)
	return strings.HasSuffix(l, "-tmp") || strings.HasSuffix(l, "-wip")
}

// lineTierRE is the tier word of line 1, its value possibly empty.
var lineTierRE = regexp.MustCompile(`\btier:\s*[A-Za-z0-9_-]*`)

// heavyLine is line 1 with its tier heavy, the least tier the TLA+ rule takes.
func heavyLine(line string) string {
	if lineTierRE.MatchString(line) {
		return lineTierRE.ReplaceAllString(line, "tier: heavy")
	}
	return strings.TrimRight(line, " ") + " tier: heavy"
}

// goPkgArg is a TEST package as a go test argument: ./-relative.
func goPkgArg(pkg string) string {
	if strings.HasPrefix(pkg, "./") || pkg == "." {
		return pkg
	}
	return "./" + pkg
}

// orElse is v, or alt when v is empty.
func orElse(v, alt string) string {
	if v == "" {
		return alt
	}
	return v
}

// uniq keeps the first of each string, in order.
func uniq(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// notIn is xs without the entries have names already.
func notIn(xs, have []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.ContainsFunc(have, func(h string) bool { return cleanEntry(h) == x }) {
			out = append(out, x)
		}
	}
	return out
}
