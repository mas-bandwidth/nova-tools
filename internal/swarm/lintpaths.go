package swarm

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// THE BRIEF CHECKS: WHAT `nova-sprint add` HOLDS A CODING BRIEF TO AT ITS BASE TIP.
//
// The owner, 2026-10-05, on cards whose work needed files outside the brief's PATHS:
// "This seems like a common failure mode, can we add a check for this to card lint?"
// Epoch 15's sprint log held 147 distinct brief defects, each costing two or more
// attempts and two reads: 26 work outside PATHS, 24 docs or SPEC left out of PATHS, 13 a
// dead or wrong base, 11 a named TEST that does not fit, 9 a class-test ledger outside
// PATHS. A coding brief (its header names a repository, REPO: or base-repo:, and a
// PATHS: line) is held at add to two of the base checks above, read at the BASE tip in
// the lander's clone (paths-at-base, donewhen-test-name from its TEST: line), and to the
// nine tokens below, each in CardBaseRemedies beside them. A PATHS or SHARED refusal
// carries the corrected line, so the coordinator applies it in one step.
//
// NO EVIDENCE IS NOT NEGATIVE EVIDENCE, as above: a check whose base could not be read
// (no clone, a fetch that failed, no BASE: line) or whose friends table was not read
// refuses with MISSING, naming what is missing, and never passes.

// The brief checks' tokens.
const (
	CheckPathsCoverNamed    = "paths-cover-named"
	CheckPathsCoverTest     = "paths-cover-test"
	CheckPathsCoverTestdata = "paths-cover-testdata"
	CheckPathsCoverLedgers  = "paths-cover-ledgers"
	CheckPathsCoverDocs     = "paths-cover-docs"
	CheckBaseIsLive         = "base-is-live"
	CheckTierSet            = "tier-set"
	CheckTLAIsFrontier      = "tla-is-frontier"
	CheckWhoServesTier      = "who-serves-tier"
)

func init() {
	for k, v := range map[string]string{
		CheckPathsCoverNamed:    "every repository path the brief names in START: or THE TASK as a file to change is covered by PATHS: (a START entry only to read is marked `(read)`); apply the corrected PATHS: line the refusal prints",
		CheckPathsCoverTest:     "PATHS: covers the `_test.go` files of the TEST: line's package directory (`<dir>/*_test.go`, or the file the test goes in), since the card writes its red test there; apply the corrected PATHS: line the refusal prints",
		CheckPathsCoverTestdata: "a PATHS: entry `<dir>/*.go` of a package that has `<dir>/testdata` at BASE covers `<dir>/testdata/**` too, since a change to the package's tests moves its fixtures; apply the corrected PATHS: line the refusal prints",
		CheckPathsCoverLedgers:  "a card whose PATHS: reaches a package's Go files covers every class-test ledger under internal/ci/testdata/<class>/<package>.txt whose rows key on that package's functions or error sites, on PATHS: and on SHARED: (every card of the package edits it); apply the corrected PATHS: and SHARED: lines the refusal prints",
		CheckPathsCoverDocs:     "a card whose PATHS: reaches a cmd/<tool> Go file (a verb or a flag) covers docs/CLI.md and the tool's docs/SPEC-<TOOL>.md, on PATHS: and on SHARED:; apply the corrected PATHS: and SHARED: lines the refusal prints",
		CheckBaseIsLive:         "BASE: is a live sprint base: a branch origin holds at add (the lander's clone fetched it), under sprint/ (or main, master, dev), never a card's attempt branch (`.w<n>[.g<n>].e<n>`, pruned at landing), a personal or temporary branch, or a deleted one; re-cut the card on the sprint branch its stream lands on",
		CheckTierSet:            "line 1 carries the card's tier (`tier: flash|pro|heavy|frontier`) or the header pins a model; a card with none was dealt to friends who could not serve it",
		CheckTLAIsFrontier:      "a card whose PATHS: covers tla/ (a TLA+ model) is tier frontier: write `tier: frontier` on line 1",
		CheckWhoServesTier:      "the friend a WHO: line names (or, for `WHO: friend`, some friend of the friends table) serves the card's tier: her class lists it; name a friend that does, re-tier the card, or drop the WHO: line for the fleet",
	} {
		CardBaseRemedies[k] = v
	}
}

// TreeChecks is the checks that read the tree at the BASE tip: with no tree, one
// paths-at-base refusal names them all.
var TreeChecks = []string{"paths-at-base", "donewhen-test-name", CheckPathsCoverNamed, CheckPathsCoverTest, CheckPathsCoverTestdata, CheckPathsCoverLedgers, CheckPathsCoverDocs}

// BriefBase is the evidence the brief checks read: the BASE tip in the lander's clone,
// and the friends table. Missing and FriendsMissing say why evidence was not read.
type BriefBase struct {
	Repo    string // the lander's clone of the brief's repository
	Sha     string // the BASE tip, the 40 hex digits rev-parse printed; "" when Missing
	Missing string // why the base was not read (no clone, a failed fetch); "" when Sha is
	Gone    string // why origin holds no such BASE branch (deleted, never pushed): base-is-live refuses
	// Friends is each friend's tiers, her class (empty is flash,pro); nil with
	// FriendsMissing when the table was not read.
	Friends        map[string][]string
	FriendsMissing string
}

// BriefFinding is one brief check's finding and, for a PATHS or SHARED refusal, the
// corrected line, "" for none.
type BriefFinding struct {
	CardHeaderFinding
	Fix string
}

// BriefChecked reports whether a brief is a coding brief the add holds to the brief
// checks: its header names a repository and a PATHS: line that is not none.
func BriefChecked(raw []byte) bool {
	return ReadCardBase(raw).Named != "" && len(briefList(raw, "PATHS")) > 0
}

// BriefTier is the brief's tier: line 1's `tier:`, or "pin" when the header pins a model;
// "" when it carries none.
func BriefTier(raw []byte) string {
	m, _ := cardhdr.ReadModel(string(raw))
	if m.Pin != "" && m.Tier == "" {
		return "pin"
	}
	return m.Tier
}

// LintBrief returns the brief checks' findings for one coding brief (BriefChecked), in
// the order the tokens are listed: the two base checks first.
func LintBrief(raw []byte, bb BriefBase) []BriefFinding {
	var out []BriefFinding
	h, _ := cardHeaderBlock(raw)
	line := func(key string) int { return max(h[key].line, 1) }
	add := func(check string, at int, excerpt, fix string) {
		out = append(out, BriefFinding{CardHeaderFinding{Check: check, Line: max(at, 1), Excerpt: excerpt}, fix})
	}
	paths, shared := briefList(raw, "PATHS"), briefList(raw, "SHARED")
	base := ReadCardBase(raw)
	pathsFix := func(more ...string) string { return "PATHS: " + strings.Join(union(paths, more), ",") }
	sharedFix := func(more ...string) string { return "SHARED: " + strings.Join(union(shared, more), ",") }

	// base-is-live and the evidence every tree check reads.
	var files []string
	missing := bb.Missing
	switch {
	case base.Ref == "":
		missing = "the brief names no BASE:, so its base was not read"
		add(CheckBaseIsLive, 1, "MISSING: "+missing+"; name the sprint branch its stream lands on", "")
	case bb.Gone != "":
		add(CheckBaseIsLive, line("BASE"), fmt.Sprintf("BASE %s is not a branch origin holds (%s): deleted, never pushed or mistyped", base.Ref, bb.Gone), "")
	case attemptBranchRE.MatchString(base.Ref):
		add(CheckBaseIsLive, line("BASE"), fmt.Sprintf("BASE %s is a card's attempt branch, pruned once it lands: never a base", base.Ref), "")
	case !liveBaseName(base.Ref):
		add(CheckBaseIsLive, line("BASE"), fmt.Sprintf("BASE %s is not a sprint base (sprint/<name>, or main, master, dev): a personal or temporary branch", base.Ref), "")
	}
	if missing == "" && base.Ref != "" && bb.Gone != "" {
		missing = "origin holds no BASE " + base.Ref
	}
	if missing == "" && !fullHexSHA(bb.Sha) {
		missing = "no BASE tip was resolved"
	}
	if missing == "" {
		var err error
		if files, err = treeFiles(bb.Repo, bb.Sha); err != nil {
			missing = fmt.Sprintf("could not list the tree at %s: %v", short12(bb.Sha), err)
		}
	}
	if missing != "" {
		// one refusal for the seven checks that read the tree, naming each, never a pass
		add("paths-at-base", line("PATHS"), "MISSING: "+missing+", so "+strings.Join(TreeChecks, ", ")+" were not run at BASE", "")
	}
	at := "BASE " + base.Ref
	if bb.Sha != "" {
		at += " (" + short12(bb.Sha) + ")"
	}

	if files != nil {
		// paths-at-base: every entry names a file, directory or glob at the BASE tip, a new
		// `_test` file, or a glob for new files in a directory the tip holds.
		var miss []string
		for _, e := range paths {
			e = cleanEntry(e)
			if !newTestFile(e) && !entryAt(e, files) && !(isGlob(e) && dirAt(path.Dir(e), files)) {
				miss = append(miss, e)
			}
		}
		if len(miss) > 0 {
			add("paths-at-base", line("PATHS"), fmt.Sprintf("PATHS %s does not exist at %s; a new file is a glob in a directory the base holds", quoteDepends(miss), at), "")
		}

		// donewhen-test-name, from the TEST: line: the named test is absent at the tip.
		testLine := h["TEST"]
		var tl cardhdr.TestLine
		switch {
		case !testLine.found:
			add("donewhen-test-name", 1, "no TEST: line; a card names the test that is red at BASE and green when the work is done: "+cardhdr.TestRemedy, "")
		default:
			var why string
			if tl, why = cardhdr.ParseTest(testLine.value); why != "" {
				add("donewhen-test-name", testLine.line, why, "")
			} else if !tl.None {
				pkg := "./" + strings.TrimPrefix(strings.TrimSuffix(tl.Package, "/"), "./")
				present, err := testDefinedAt(bb.Repo, bb.Sha, doneTest{runner: "go", name: tl.Name, scope: []string{pkg}})
				switch {
				case err != nil:
					add("donewhen-test-name", testLine.line, fmt.Sprintf("MISSING: could not search the tree at %s for %s: %v", short12(bb.Sha), tl.Name, err), "")
				case present:
					add("donewhen-test-name", testLine.line, fmt.Sprintf("TEST %s exists at %s, so it cannot be red there; name the new test the card adds", tl.Name, at), "")
				}
			}
		}

		// paths-cover-named: START's paths (but those marked read) and THE TASK's files.
		var uncovered []string
		for _, n := range namedPaths(raw, h, files) {
			if !covered(n, paths, files) && !slices.Contains(uncovered, n) {
				uncovered = append(uncovered, n)
			}
		}
		if len(uncovered) > 0 {
			add(CheckPathsCoverNamed, line("PATHS"), fmt.Sprintf("the brief names %s to change, and PATHS does not cover it", quoteDepends(uncovered)), pathsFix(uncovered...))
		}

		// paths-cover-test: the TEST package's _test files.
		if tl.Package != "" {
			dir := strings.TrimSuffix(strings.TrimPrefix(path.Clean(tl.Package), "./"), "/")
			if dir == "." {
				dir = ""
			}
			if !coversTests(dir, paths) {
				add(CheckPathsCoverTest, line("PATHS"), fmt.Sprintf("TEST names package %s, and PATHS covers none of its _test.go files", tl.Package), pathsFix(joinDir(dir, "*_test.go")))
			}
		}

		// paths-cover-testdata: dir/*.go covers dir/testdata/** when the package has one.
		var testdata []string
		for _, e := range paths {
			e = cleanEntry(e)
			if path.Base(e) != "*.go" {
				continue
			}
			td := joinDir(path.Dir(e), "testdata")
			if dirAt(td, files) && !covered(td+"/x", paths, nil) && !slices.Contains(testdata, td+"/**") {
				testdata = append(testdata, td+"/**")
			}
		}
		if len(testdata) > 0 {
			add(CheckPathsCoverTestdata, line("PATHS"), fmt.Sprintf("PATHS covers the Go files of a package with testdata, and not %s", quoteDepends(testdata)), pathsFix(testdata...))
		}

		// paths-cover-ledgers and paths-cover-docs: files every card of a package edits.
		pkgs := goPackages(paths, files)
		var ledgers []string
		for _, p := range pkgs {
			for _, f := range files {
				if c, ok := ledgerOf(f); ok && c == p && functionLedger(bb.Repo, bb.Sha, f, p) {
					ledgers = append(ledgers, f)
				}
			}
		}
		sharedOn(add, CheckPathsCoverLedgers, line("PATHS"), "a class-test ledger of a package PATHS reaches, keyed on its functions or error sites", ledgers, paths, shared, pathsFix, sharedFix)
		var docs []string
		for _, p := range pkgs {
			tool, ok := strings.CutPrefix(p, "cmd/")
			if !ok || strings.Contains(tool, "/") {
				continue
			}
			for _, d := range []string{"docs/CLI.md", "docs/SPEC-" + strings.ToUpper(strings.TrimPrefix(tool, "nova-")) + ".md"} {
				if slices.Contains(files, d) && !slices.Contains(docs, d) {
					docs = append(docs, d)
				}
			}
		}
		sharedOn(add, CheckPathsCoverDocs, line("PATHS"), "the CLI reference and SPEC of a cmd/<tool> PATHS reaches", docs, paths, shared, pathsFix, sharedFix)
	}

	// tier-set, tla-is-frontier, who-serves-tier: the card's tier.
	tier := BriefTier(raw)
	if tier == "" {
		add(CheckTierSet, 1, "line 1 names no tier and the header pins no model", "")
	}
	for _, e := range paths {
		if e = cleanEntry(e); (e == "tla" || strings.HasPrefix(e, "tla/") || e == "**" || e == "*") && tier != cardhdr.RouteFrontier {
			add(CheckTLAIsFrontier, line("PATHS"), fmt.Sprintf("PATHS %s covers tla/, and the card's tier is %s", e, orDash(tier)), "")
			break
		}
	}
	if w, why := cardhdr.ReadWho(string(raw)); why == "" && w.Friend && tier != "" && tier != "pin" {
		switch {
		case bb.Friends == nil:
			add(CheckWhoServesTier, line("WHO"), "MISSING: the friends table was not read ("+orDash(bb.FriendsMissing)+"), so no friend was held to tier "+tier, "")
		case w.Name != "":
			if !slices.Contains(friendTiers(bb.Friends[w.Name]), tier) {
				add(CheckWhoServesTier, line("WHO"), fmt.Sprintf("WHO: friend %s, whose class is %s, does not serve tier %s", w.Name, strings.Join(friendTiers(bb.Friends[w.Name]), ","), tier), "")
			}
		default:
			var serves []string
			for _, n := range slices.Sorted(maps.Keys(bb.Friends)) {
				if slices.Contains(friendTiers(bb.Friends[n]), tier) {
					serves = append(serves, n)
				}
			}
			if len(serves) == 0 {
				add(CheckWhoServesTier, line("WHO"), "WHO: friend, and no friend of the friends table serves tier "+tier, "")
			}
		}
	}
	return out
}

// sharedOn refuses the files a card's PATHS must cover and its SHARED declare, with the
// corrected lines.
func sharedOn(add func(check string, at int, excerpt, fix string), check string, at int, what string, need, paths, shared []string, pathsFix, sharedFix func(...string) string) {
	var notPaths, notShared []string
	for _, f := range need {
		if !covered(f, paths, nil) && !slices.Contains(notPaths, f) {
			notPaths = append(notPaths, f)
		}
		if !covered(f, shared, nil) && !slices.Contains(notShared, f) {
			notShared = append(notShared, f)
		}
	}
	var fix []string
	if len(notPaths) > 0 {
		fix = append(fix, pathsFix(notPaths...))
	}
	if len(notShared) > 0 {
		fix = append(fix, sharedFix(notShared...))
	}
	if len(fix) > 0 {
		add(check, at, fmt.Sprintf("%s: %s is not on PATHS and SHARED", what, quoteDepends(union(notPaths, notShared))), strings.Join(fix, " | "))
	}
}

// attemptBranchRE is a card attempt's branch: sprint/<card>.w<n>[.g<n>].e<n>.
var attemptBranchRE = regexp.MustCompile(`\.w[0-9]+(\.g[0-9]+)?\.e[0-9]+$`)

// liveBaseName is a sprint base's name: sprint/<name>, or a long-lived branch.
func liveBaseName(ref string) bool {
	switch ref {
	case "main", "master", "dev":
		return true
	}
	rest, ok := strings.CutPrefix(ref, "sprint/")
	return ok && rest != ""
}

// briefList is a header line's entries (PATHS:, SHARED:), commas or blanks between
// them; none and - name nothing.
func briefList(raw []byte, key string) []string {
	v, _ := CardHeaderValue(raw, key)
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if p != "none" && p != "-" {
			out = append(out, p)
		}
	}
	return out
}

// union is a with each of more it lacks appended, in order.
func union(a, more []string) []string {
	out := slices.Clone(a)
	for _, m := range more {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

func cleanEntry(e string) string {
	return strings.TrimPrefix(strings.TrimSuffix(e, "/"), "./")
}

func isGlob(e string) bool { return strings.ContainsAny(e, "*?[") }

func joinDir(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// friendTiers is a friend's class as tiers; a class that lists none is flash,pro, as a
// worker advertising nothing is (cardhdr).
func friendTiers(class []string) []string {
	if len(class) == 0 {
		return []string{cardhdr.RouteFlash, cardhdr.RoutePro}
	}
	return class
}

// treeFiles is every file of the tree at sha.
func treeFiles(repo, sha string) ([]string, error) {
	list, err := baseGit(repo, "ls-tree", "-r", "--name-only", "-z", "--end-of-options", sha)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(list, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// entryAt says a PATHS entry names something in files: a file, a directory, or a glob.
func entryAt(e string, files []string) bool {
	return slices.ContainsFunc(files, func(f string) bool {
		return f == e || strings.HasPrefix(f, e+"/") || hygiene.MatchGlob(e, f)
	})
}

// dirAt says files hold something under dir ("" or "." is the root).
func dirAt(dir string, files []string) bool {
	if dir == "" || dir == "." {
		return len(files) > 0
	}
	return slices.ContainsFunc(files, func(f string) bool { return strings.HasPrefix(f, dir+"/") })
}

// covered says p is covered by an entry: the entry is p, a directory over it, or a glob
// matching it. A p that is a directory of files (in files) is covered by an entry inside it.
func covered(p string, entries, files []string) bool {
	for _, e := range entries {
		e = strings.TrimSuffix(cleanEntry(e), "/**")
		if e == p || strings.HasPrefix(p, e+"/") || hygiene.MatchGlob(e, p) || e == "**" {
			return true
		}
		if files != nil && !slices.Contains(files, p) && dirAt(p, files) && strings.HasPrefix(e, p+"/") {
			return true
		}
	}
	return false
}

// coversTests says an entry covers a new _test.go file in dir.
func coversTests(dir string, entries []string) bool {
	probe := joinDir(dir, "zz_brief_lint_test.go")
	for _, e := range entries {
		e = cleanEntry(e)
		switch {
		case covered(probe, []string{e}, nil):
			return true
		case path.Dir(e) == orDot(dir) && strings.HasSuffix(e, "_test.go"):
			return true // the literal file, or a glob of them, the test goes in
		}
	}
	return false
}

func orDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

// goPackages is the package directories whose Go files (not tests) PATHS reaches, in order.
func goPackages(entries, files []string) []string {
	var out []string
	addPkg := func(p string) {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, e := range entries {
		e = cleanEntry(e)
		switch {
		case strings.HasSuffix(e, ".go") && !strings.HasSuffix(e, "_test.go") && !strings.Contains(path.Dir(e), "*"):
			addPkg(path.Dir(e))
		case !strings.Contains(path.Base(e), ".") && !isGlob(e) && dirAt(e, files):
			// a directory: every package under it whose Go files are in the tree
			for _, f := range files {
				if strings.HasPrefix(f, e+"/") && strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") && !strings.Contains(f, "/testdata/") {
					addPkg(path.Dir(f))
				}
			}
		}
	}
	return out
}

// ledgerOf is the package a class-test ledger file keys on:
// internal/ci/testdata/<class>/<package>.txt.
func ledgerOf(f string) (string, bool) {
	rest, ok := strings.CutPrefix(f, "internal/ci/testdata/")
	if !ok || !strings.HasSuffix(rest, ".txt") {
		return "", false
	}
	_, pkg, ok := strings.Cut(strings.TrimSuffix(rest, ".txt"), "/")
	return pkg, ok && strings.Contains(pkg, "/")
}

// functionLedger says the ledger's rows key on the package's functions or error sites:
// a row `<package>/<file>.go:<function>:<site>`.
func functionLedger(repo, sha, f, pkg string) bool {
	text, err := baseGit(repo, "show", "--end-of-options", sha+":"+f)
	if err != nil {
		return true // an unread ledger of the package is held as one: never a pass on no evidence
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(pkg) + `/[^/: \t]+\.go:[A-Za-z_][A-Za-z0-9_.]*:`)
	for _, l := range strings.Split(text, "\n") {
		if re.MatchString(strings.TrimSpace(l)) {
			return true
		}
	}
	return false
}

// pathTokenRE is a repository path in prose: segments joined by /, no URL scheme, no
// placeholder.
var pathTokenRE = regexp.MustCompile("(?:^|[\\s(\x60\"'])((?:\\./)?[A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_.*-]+)+)")

// readMarkRE is a START entry's mark that it is only read.
var readMarkRE = regexp.MustCompile(`\(\s*read\b`)

// namedPaths is the paths the brief names to change: each START entry's path (an entry
// marked `(read)` aside), and each path of THE TASK that names a file at the tip.
func namedPaths(raw []byte, h map[string]headerField, files []string) []string {
	var out []string
	topDirs := map[string]bool{}
	for _, f := range files {
		if d, _, ok := strings.Cut(f, "/"); ok {
			topDirs[d] = true
		}
	}
	if s := h["START"]; s.found {
		for _, entry := range splitTopLevel(s.value) {
			if readMarkRE.MatchString(entry) {
				continue
			}
			f := strings.Fields(entry)
			if len(f) == 0 {
				continue
			}
			p := cleanEntry(strings.TrimRight(f[0], ".,;:"))
			if d, _, ok := strings.Cut(p, "/"); (ok && topDirs[d] || slices.Contains(files, p)) && !isGlob(p) && !strings.ContainsAny(p, "<>") {
				out = append(out, p)
			}
		}
	}
	for _, m := range pathTokenRE.FindAllStringSubmatch(taskText(string(raw)), -1) {
		p := cleanEntry(strings.TrimRight(m[1], ".,;:"))
		if !isGlob(p) && slices.Contains(files, p) {
			out = append(out, p)
		}
	}
	return out
}

// splitTopLevel splits a header value on the commas outside parentheses.
func splitTopLevel(v string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range v {
		switch r {
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(v[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(v[start:]))
}

// taskText is the brief's THE TASK paragraph: from its `THE TASK.` line to the next STEP,
// RULES or `Libraries considered` line.
func taskText(brief string) string {
	var b strings.Builder
	in := false
	for _, l := range strings.Split(brief, "\n") {
		switch {
		case strings.HasPrefix(l, "THE TASK"):
			in = true
		case stepHeadRE.MatchString(l), strings.HasPrefix(l, "RULES"), strings.HasPrefix(l, "Libraries considered"):
			in = false
		}
		if in {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}
