// verification re-measures docs/roadmaps/nova-work.sexp against the acceptance
// suite at HEAD (nova-tools#3459). The sexp's :verification block says every
// verified criterion's test "passed at :revision"; nothing re-derived that, so
// the block said total=336 at a revision four days and 1,204 commits old while
// the suite passed 549. This verb makes the claim mechanical:
//
//   - it runs the suite (lisp/nova-work/run-tests.sh under --repo, the block's own
//     :suite) and reads its `TEST <name> PASS|FAIL` lines and its one
//     `NOVA-WORK SLICE1 total= pass= fail=` summary;
//   - it lists STALE every criterion whose :state is verified but one of whose
//     named tests is red or gone from the suite -- the rows the sexp would lie
//     about at a new :revision -- and PROPOSE every criterion whose own :tests all
//     pass but whose :state is not verified (the flip a reader then makes; the
//     verb flips no :state itself);
//   - --write rewrites :measured-at, :revision and :suite-result in place, byte for
//     byte outside those three strings, and only when the suite is green, nothing
//     is STALE and lisp/nova-work has no uncommitted change (else the measurement
//     is not of HEAD); --check writes nothing and is the merge gate (make check).
//
// Which tests name a criterion: the criterion row's own :tests when it carries one
// (the :criteria-rule's "the proving tests when known"), else its feature's :tests.
// A PROPOSE needs the row's own :tests: a feature's list proves the rows already
// verified and says nothing about which test would prove the rest. A group
// qualified by a Go file ("internal/ghcapture/adapter_test.go: TestX") is proved by
// `go test`, not by this suite, and is not judged here.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// suiteRun runs the acceptance suite in the checkout at repo and returns its
// combined output. A non-zero exit with a summary line is a red suite, not an
// error: the caller reads the summary.
type suiteRun func(ctx context.Context, repo string) ([]byte, error)

// headRead answers the checkout's HEAD sha and whether lisp/nova-work holds an
// uncommitted change (so a suite run there did not measure HEAD).
type headRead func(repo string) (sha string, dirty bool, err error)

func runSuite(ctx context.Context, repo string) ([]byte, error) {
	// Absolute: a relative program path resolves against cmd.Dir in the child,
	// so --repo . would name lisp/nova-work/lisp/nova-work/run-tests.sh.
	root, err := filepath.Abs(repo)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "lisp", "nova-work")
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "run-tests.sh"))
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	return cmd.CombinedOutput()
}

func readHead(repo string) (string, bool, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", false, fmt.Errorf("git rev-parse HEAD in %s: %w", repo, err)
	}
	st, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--", "lisp/nova-work").Output()
	if err != nil {
		return "", false, fmt.Errorf("git status in %s: %w", repo, err)
	}
	return strings.TrimSpace(string(out)), len(bytes.TrimSpace(st)) > 0, nil
}

var (
	suiteTestRE    = regexp.MustCompile(`^TEST (\S+) (PASS|FAIL)\b`)
	suiteSummaryRE = regexp.MustCompile(`^NOVA-WORK SLICE1 total=(\d+) pass=(\d+) fail=(\d+)\s*$`)
)

// suiteResult is one transcript read: every test's verdict and the summary line.
type suiteResult struct {
	verdict map[string]bool // name -> passed
	failed  []string        // red tests, in transcript order
	summary string          // the summary line verbatim, "" when absent
	total   string          // the summary's three counts
	pass    string
	fail    string
}

func parseSuite(out []byte) suiteResult {
	r := suiteResult{verdict: map[string]bool{}}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if m := suiteTestRE.FindStringSubmatch(line); m != nil {
			pass := m[2] == "PASS"
			// A name reported twice counts red if either run was red.
			if prev, seen := r.verdict[m[1]]; seen {
				pass = pass && prev
			}
			r.verdict[m[1]] = pass
			if !pass {
				r.failed = append(r.failed, m[1])
			}
			continue
		}
		if m := suiteSummaryRE.FindStringSubmatch(line); m != nil {
			r.summary = strings.TrimSpace(line)
			r.total, r.pass, r.fail = m[1], m[2], m[3]
		}
	}
	return r
}

// namedTests splits one :tests string into the test names this suite judges and
// the count of names left to `go test`. Groups are ';'-separated; a group may
// be "<file>: a, b"; names within a group are ','-separated.
func namedTests(s string) (lisp []string, external int) {
	for _, group := range strings.Split(s, ";") {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		names := group
		if i := strings.Index(group, ": "); i > 0 && !strings.ContainsAny(group[:i], " \t") {
			file := group[:i]
			names = group[i+2:]
			if strings.HasSuffix(file, ".go") {
				for _, n := range strings.Split(names, ",") {
					if strings.TrimSpace(n) != "" {
						external++
					}
				}
				continue
			}
		}
		for _, n := range strings.Split(names, ",") {
			if n = strings.TrimSpace(n); n != "" {
				lisp = append(lisp, n)
			}
		}
	}
	return lisp, external
}

// criterionRow is one :criteria row of one :by-feature entry.
type criterionRow struct {
	id, state   string
	ownTests    string // the row's own :tests, "" when absent
	hasOwn      bool
	featureTest string // the feature's :tests
}

// verificationDoc is the parsed sexp plus the three string forms --write edits.
type verificationDoc struct {
	data     []byte
	criteria []criterionRow
	fields   map[string]worklang.Form // measured-at, revision, suite-result
}

var verificationFields = []string{"measured-at", "revision", "suite-result"}

func readVerificationDoc(path string) (*verificationDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// The same bounds the roadmap reader names for this file (roadmap.go).
	limits := worklang.Limits{MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 1 << 14}
	form, err := worklang.Read(path, data, limits)
	if err != nil {
		return nil, err
	}
	if form.Kind != worklang.List {
		return nil, fmt.Errorf("%s: top-level form is not a list", path)
	}
	ver, ok := plistAny(form.List, "verification")
	if !ok || ver.Kind != worklang.List {
		return nil, fmt.Errorf("%s: no :verification list", path)
	}
	doc := &verificationDoc{data: data, fields: map[string]worklang.Form{}}
	for _, k := range verificationFields {
		f, ok := plistAny(ver.List, k)
		if !ok || f.Kind != worklang.String {
			return nil, fmt.Errorf("%s: :verification has no string :%s", path, k)
		}
		doc.fields[k] = f
	}
	byFeature, ok := plistAny(ver.List, "by-feature")
	if !ok || byFeature.Kind != worklang.List {
		return nil, fmt.Errorf("%s: no :by-feature list", path)
	}
	for _, entry := range byFeature.List {
		if entry.Kind != worklang.List {
			continue
		}
		ft := ""
		if f, ok := plistAny(entry.List, "tests"); ok && f.Kind == worklang.String {
			ft = f.Value
		}
		crit, ok := plistAny(entry.List, "criteria")
		if !ok || crit.Kind != worklang.List {
			continue
		}
		for _, row := range crit.List {
			if row.Kind != worklang.List {
				continue
			}
			id, _ := plistString(row.List, "id")
			state, _ := plistString(row.List, "state")
			if id == "" {
				continue
			}
			c := criterionRow{id: id, state: state, featureTest: ft}
			if f, ok := plistAny(row.List, "tests"); ok && f.Kind == worklang.String {
				c.ownTests, c.hasOwn = f.Value, true
			}
			doc.criteria = append(doc.criteria, c)
		}
	}
	if len(doc.criteria) == 0 {
		return nil, fmt.Errorf("%s: :by-feature holds no :criteria rows", path)
	}
	return doc, nil
}

// verificationFinding is one listed row: a STALE verified criterion or a PROPOSE.
type verificationFinding struct {
	id, state string
	red, gone []string
	tests     []string
}

// judge compares every criterion with the transcript.
func judge(doc *verificationDoc, suite suiteResult) (stale, propose []verificationFinding, verified int, unclaimed int) {
	named := map[string]bool{}
	for _, c := range doc.criteria {
		spec := c.featureTest
		if c.hasOwn {
			spec = c.ownTests
		}
		lisp, external := namedTests(spec)
		feature, _ := namedTests(c.featureTest)
		for _, n := range append(append([]string(nil), lisp...), feature...) {
			named[n] = true
		}
		var red, gone []string
		for _, n := range lisp {
			pass, ok := suite.verdict[n]
			switch {
			case !ok:
				gone = append(gone, n)
			case !pass:
				red = append(red, n)
			}
		}
		if c.state == "verified" {
			verified++
			if len(lisp) == 0 && external == 0 {
				gone = append(gone, "(no test named)")
			}
			if len(red)+len(gone) > 0 {
				stale = append(stale, verificationFinding{id: c.id, state: c.state, red: red, gone: gone})
			}
			continue
		}
		if c.hasOwn && len(lisp) > 0 && len(red)+len(gone) == 0 {
			propose = append(propose, verificationFinding{id: c.id, state: c.state, tests: lisp})
		}
	}
	for n, pass := range suite.verdict {
		if pass && !named[n] {
			unclaimed++
		}
	}
	return stale, propose, verified, unclaimed
}

// quoteSexp spells s as a restricted-Lisp string literal: the reader's two
// escapes, and a line break or tab folded to a space (a value is one line).
var sexpStringEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ", "\r", " ", "\t", " ")

func quoteSexp(s string) string { return `"` + sexpStringEscaper.Replace(s) + `"` }

// rewriteVerification replaces the three string forms, last byte first, and
// touches no other byte of the file.
func rewriteVerification(doc *verificationDoc, values map[string]string) []byte {
	type edit struct {
		at, end int
		text    string
	}
	edits := make([]edit, 0, len(values))
	for k, v := range values {
		f := doc.fields[k]
		edits = append(edits, edit{f.Offset, f.End, quoteSexp(v)})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].at > edits[j].at })
	out := append([]byte(nil), doc.data...)
	for _, e := range edits {
		out = append(out[:e.at], append([]byte(e.text), out[e.end:]...)...)
	}
	return out
}

func verificationRefused(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "VERIFICATION REFUSED %s\n", oneline.Escape(what))
	return 1
}

func cmdVerification(args []string, stdout, stderr io.Writer, deps Deps) int {
	fs := flag.NewFlagSet("verification", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	sexp := fs.String("sexp", "", "the roadmap sexp (required)")
	repo := fs.String("repo", "", "the checkout whose suite runs (required)")
	check := fs.Bool("check", false, "measure and judge; write nothing")
	write := fs.Bool("write", false, "measure, judge and rewrite :verification")
	timeout := fs.Duration("timeout", 15*time.Minute, "the suite's wall bound")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " verification", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	switch {
	case fs.NArg() > 0:
		return refuse(stderr, " verification", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	case strings.TrimSpace(*sexp) == "":
		return refuse(stderr, " verification", "--sexp is required; refusing to guess")
	case strings.TrimSpace(*repo) == "":
		return refuse(stderr, " verification", "--repo is required; refusing to guess")
	case *check == *write:
		return refuse(stderr, " verification", "give exactly one of --check or --write")
	case *timeout <= 0:
		return refuse(stderr, " verification", "--timeout must be positive")
	// --write on the forest is the kernel's (#3340): refused before the suite
	// runs, the same forge-call boundary attempt record, set check
	// --write-status, next --take, ask --record, dependencies --graph and plan
	// expand --out already hold.
	case *write && isForestPath(*sexp):
		return forestRefused(stderr, " verification", *sexp)
	}
	suiteFn, headFn, now := deps.Suite, deps.Head, deps.Now
	if suiteFn == nil {
		suiteFn = runSuite
	}
	if headFn == nil {
		headFn = readHead
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	doc, err := readVerificationDoc(*sexp)
	if err != nil {
		return verificationRefused(stderr, oneline.Err(err)+"; remedy: fix the sexp so it reads")
	}
	sha, dirty, err := headFn(*repo)
	if err != nil {
		return verificationRefused(stderr, oneline.Err(err)+"; remedy: --repo names a git checkout")
	}
	if *write && dirty {
		return verificationRefused(stderr, "lisp/nova-work has uncommitted changes, so the suite would not measure HEAD "+sha+"; remedy: commit or drop them, then rerun")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	out, runErr := suiteFn(ctx, *repo)
	suite := parseSuite(out)
	if suite.summary == "" {
		why := "the suite printed no NOVA-WORK SLICE1 summary line"
		if ctx.Err() != nil {
			why = "the suite ran past --timeout " + timeout.String()
		} else if runErr != nil {
			var exit *exec.ExitError
			if !errors.As(runErr, &exit) {
				why += " (" + oneline.Err(runErr) + ")"
			}
		}
		return verificationRefused(stderr, why+"; nothing written; remedy: make test-lisp and read its tail")
	}

	stale, propose, verified, unclaimed := judge(doc, suite)
	for _, n := range suite.failed {
		fmt.Fprintf(stdout, "VERIFICATION RED test=%s\n", oneline.Field(n))
	}
	for _, f := range stale {
		fmt.Fprintf(stdout, "VERIFICATION STALE %s red=%s gone=%s\n", oneline.Field(f.id),
			oneline.Field(joinOrDash(f.red)), oneline.Field(joinOrDash(f.gone)))
	}
	for _, f := range propose {
		fmt.Fprintf(stdout, "VERIFICATION PROPOSE %s state=%s tests=%s\n", oneline.Field(f.id),
			oneline.Field(f.state), oneline.Field(strings.Join(f.tests, ",")))
	}
	counts := fmt.Sprintf("revision=%s total=%s pass=%s fail=%s criteria=%d verified=%d stale=%d propose=%d unclaimed=%d",
		oneline.Field(sha), oneline.Field(suite.total), oneline.Field(suite.pass), oneline.Field(suite.fail),
		len(doc.criteria), verified, len(stale), len(propose), unclaimed)

	if suite.fail != "0" || len(suite.failed) > 0 {
		return verificationRefused(stderr, counts+"; the suite is red, nothing written; remedy: fix the RED tests")
	}
	if len(stale) > 0 {
		return verificationRefused(stderr, counts+"; a verified criterion's named test is red or gone, nothing written; remedy: restore the test, rename it in the sexp's :tests, or set that criterion's :state to unverified")
	}
	if *check {
		fmt.Fprintf(stdout, "VERIFICATION OK mode=check %s\n", oneline.Escape(counts))
		return 0
	}

	values := map[string]string{
		"measured-at":  now().UTC().Format("2006-01-02"),
		"revision":     sha,
		"suite-result": suite.summary,
	}
	next := rewriteVerification(doc, values)
	// Read the result back before it replaces the file: a rewrite that does not
	// read, or does not say what was measured, is never written.
	back, err := worklang.Read(*sexp, next, worklang.Limits{MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 1 << 14})
	if err != nil {
		return verificationRefused(stderr, "the rewritten sexp does not read ("+oneline.Err(err)+"); nothing written")
	}
	ver, _ := plistAny(back.List, "verification")
	for _, k := range verificationFields {
		if got, _ := plistString(ver.List, k); got != values[k] {
			return verificationRefused(stderr, "the rewritten :"+k+" reads back as "+got+"; nothing written")
		}
	}
	if err := writeWorkSet(*sexp, next); err != nil {
		return verificationRefused(stderr, oneline.Err(err)+"; nothing written")
	}
	fmt.Fprintf(stdout, "VERIFICATION OK mode=write file=%s measured-at=%s %s\n",
		oneline.Field(*sexp), oneline.Field(values["measured-at"]), oneline.Escape(counts))
	return 0
}

func joinOrDash(xs []string) string {
	if len(xs) == 0 {
		return "-"
	}
	return strings.Join(xs, ",")
}
