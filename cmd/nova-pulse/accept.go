package main

// nova-pulse accept -- the gate between harvest's line-1 verify and its push
// (SPEC-TOOLWORK §1 rules 1-5 and 10, nova-tools#2222).
//
// Every earlier rule reads what the worker SAID. This one executes the claim: it reads
// the card (never RESULT.md), makes its own private clone of the job's head, and runs
// the common gate in the spec's order, the first failure deciding:
//
//	(a)  hygiene: identity, stray-file, secret, out-of-path (internal/hygiene, the one
//	     definition the merge lane and nova-check share)
//	(b)  shape: the card changed a test file (no-test); its TEST: exists at head
//	     (named-test-missing)
//	(b2) the base's tests survive: every Test at the base in a touched package is still
//	     there and has gained no Skip, and the base's copy of every pre-existing test
//	     file the card changed, overlaid on the head, passes (test-weakened); a file the
//	     card names under TEST-EDIT: is excused from the overlay
//	(c)  build, vet, and the touched packages' tests green at head (build, vet,
//	     red-at-head; a red the card neither wrote nor named is run once at the base,
//	     and red there too is ABSTAIN base-red)
//	(d)  the card's negative control: `nova-review mutate`'s range form over the head;
//	     a test the card wrote or changed that stays green without the change is
//	     vacuous-test, and the card's TEST: must be among the reds (named-test-not-red)
//
// Each command runs once; a red is a finding, never a rerun (rule 5). No model, no
// forge, no network: GOPROXY is off (rule 10).
//
// --cert must be a live CERTIFY OK record (§2 rule 4) for --bench: the go leg passed,
// until= ahead and at most 24 hours after at=, build= this tool's build, go= the host's
// go version, wall= not none for a code card, and not voided on file by an earlier
// toolchain abstain on this bench (§2 rules 6-7); anything else is ABSTAIN
// bench-uncertified (§1 rule 3) with the first failed check on stderr.
//
// Not yet here, and said so rather than faked: the selftest's twelve seeds and the
// control-on-file refusal of rule 8 (recut-2222 part 2), and running the commands
// through nova-sandbox (§2 rule 2's --toolchain, a sandbox change of its own). The
// control id is computed and printed now.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/review"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// acceptGatedKinds are the kinds whose declared gate is the common one. A kind not
// here is ABSTAIN unknown-kind: the gate never guesses a kind's checks.
var acceptGatedKinds = map[string]bool{"fix-red": true, "fix": true}

var (
	acceptTestName = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)
	acceptFailLine = regexp.MustCompile(`--- FAIL: (Test[A-Za-z0-9_]*)`)
	acceptAtLine   = regexp.MustCompile(`^(?:\./)?([^\s:]+\.go):(\d+)`)
	acceptSkipCall = regexp.MustCompile(`\.Skip(?:Now|f)?\(`)
)

// acceptCard is what the gate reads from the card: its typed header lines, first
// occurrence of each, validated before any is used.
type acceptCard struct {
	Label, Kind, Test string
	Paths             []string
	TestEdit          []string // TEST-EDIT: the pre-existing test files the card may change
}

func parseAcceptCard(text, fallback string) (acceptCard, error) {
	c := acceptCard{Label: fallback}
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		val = strings.TrimSpace(val)
		switch key {
		case "LABEL":
			if val != "" {
				c.Label = val
			}
		case "KIND":
			c.Kind = val
		case "TEST":
			c.Test = val
		case "PATHS":
			c.Paths = acceptFields(val)
		case "TEST-EDIT":
			c.TestEdit = acceptFields(val)
		}
	}
	if c.Test != "" && !acceptTestName.MatchString(c.Test) {
		return c, fmt.Errorf("the card's TEST: %q is not a Go test name", c.Test)
	}
	if len(c.Paths) > 0 {
		if err := hygiene.ValidatePaths(c.Paths); err != nil {
			return c, fmt.Errorf("the card's PATHS: %v", err)
		}
	}
	for _, f := range c.TestEdit {
		if !strings.HasSuffix(f, "_test.go") || hygiene.ValidatePaths([]string{f}) != nil || strings.ContainsAny(f, "*?[") {
			return c, fmt.Errorf("the card's TEST-EDIT: %q is not a repo-relative test file", f)
		}
	}
	return c, nil
}

func acceptFields(val string) []string {
	return strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

// acceptVerdict is one gate answer: Word is OK, REJECT or ABSTAIN.
type acceptVerdict struct {
	Word, Reason, At  string
	Head, Base        string
	Tests, RedWithout int
}

type acceptJob struct {
	Job, Base  string
	Card       acceptCard
	Identities []hygiene.Identity
	TempRoot   string
}

func acceptReject(reason, at string) acceptVerdict {
	return acceptVerdict{Word: "REJECT", Reason: reason, At: at}
}

// acceptGate runs the common gate over j. An error is could-not-run (exit 2), never a
// verdict.
func acceptGate(ctx context.Context, j acceptJob) (acceptVerdict, error) {
	abs, err := filepath.Abs(j.Job)
	if err != nil {
		return acceptVerdict{}, err
	}
	j.Job = abs
	head, err := acceptGit(ctx, j.Job, "rev-parse", "HEAD^{commit}")
	if err != nil {
		return acceptVerdict{}, fmt.Errorf("--job %s has no head commit", j.Job)
	}
	base, err := acceptGit(ctx, j.Job, "rev-parse", j.Base+"^{commit}")
	if err != nil {
		return acceptVerdict{}, fmt.Errorf("--base %s names no commit in the job", j.Base)
	}
	if mb, err := acceptGit(ctx, j.Job, "merge-base", base, head); err == nil && mb != "" {
		base = mb
	}
	v, err := acceptGateAt(ctx, j, base, head)
	v.Head, v.Base = head, base
	return v, err
}

func acceptGateAt(ctx context.Context, j acceptJob, base, head string) (acceptVerdict, error) {
	root := j.TempRoot
	if root == "" {
		root = os.TempDir()
	}
	wt, err := os.MkdirTemp(root, "nova-pulse-accept-")
	if err != nil {
		return acceptVerdict{}, err
	}
	// Rule 3: never the worker's own working copy. A private clone has none of the job
	// clone's config, hooks, attributes or untracked files.
	defer func() { _ = safepath.RemoveUnder(root, wt) }()
	if _, err := acceptGit(ctx, wt, "clone", "--quiet", "--shared", "--no-checkout", j.Job, "."); err != nil {
		return acceptVerdict{}, err
	}
	if _, err := acceptGit(ctx, wt, "checkout", "--quiet", "--detach", head); err != nil {
		return acceptVerdict{}, err
	}

	// (a) hygiene, in the spec's order.
	findings, err := hygiene.Check(ctx, hygiene.Options{Repo: wt, Base: base, Head: head, Paths: j.Card.Paths, Identities: j.Identities, Kind: j.Card.Kind})
	if err != nil {
		return acceptVerdict{}, err
	}
	rank := map[string]int{"identity": 0, "stray-file": 1, "secret": 2, "out-of-path": 3}
	sort.SliceStable(findings, func(a, b int) bool { return rank[findings[a].Token] < rank[findings[b].Token] })
	if len(findings) > 0 {
		return acceptReject(findings[0].Token, findings[0].At), nil
	}

	// (b) shape.
	changed, err := acceptGit(ctx, wt, "diff", "--name-status", "--no-renames", base, head)
	if err != nil {
		return acceptVerdict{}, err
	}
	dirs := map[string]bool{}
	var testFiles, changedBaseTests []string
	for _, line := range strings.Split(changed, "\n") {
		status, file, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasSuffix(file, ".go") {
			continue
		}
		dirs[path.Dir(file)] = true
		if strings.HasSuffix(file, "_test.go") && status != "D" {
			testFiles = append(testFiles, file)
		}
		if strings.HasSuffix(file, "_test.go") && status == "M" {
			changedBaseTests = append(changedBaseTests, file)
		}
	}
	if len(testFiles) == 0 {
		return acceptReject("no-test", "-"), nil
	}
	baseFuncs, _, err := acceptTestFuncs(ctx, wt, base, dirs)
	if err != nil {
		return acceptVerdict{}, err
	}
	headFuncs, bad, err := acceptTestFuncs(ctx, wt, head, dirs)
	if err != nil {
		return acceptVerdict{}, err
	}
	if bad != "" {
		return acceptReject("build", bad), nil
	}
	if _, ok := headFuncs[j.Card.Test]; !ok {
		return acceptReject("named-test-missing", j.Card.Test), nil
	}

	// (b2) the base's tests survive.
	for _, name := range acceptSorted(baseFuncs) {
		now, ok := headFuncs[name]
		if !ok || len(acceptSkipCall.FindAllString(now, -1)) > len(acceptSkipCall.FindAllString(baseFuncs[name], -1)) {
			return acceptReject("test-weakened", name), nil
		}
	}
	if v, done, err := acceptOverlayBaseTests(ctx, wt, base, head, changedBaseTests, j.Card.TestEdit); done || err != nil {
		return v, err
	}
	// The tests the card wrote or changed: new at head, or a different body.
	cardTests := map[string]bool{}
	for name, body := range headFuncs {
		if baseFuncs[name] != body {
			cardTests[name] = true
		}
	}

	// (c) positive.
	var pkgs []string
	for _, d := range acceptSorted(dirs) {
		pkgs = append(pkgs, "./"+d)
	}
	for _, step := range []struct{ token, verb string }{{"build", "build"}, {"vet", "vet"}} {
		if out, err := acceptGo(ctx, wt, append([]string{step.verb}, pkgs...)...); err != nil {
			return acceptRed(step.token, "-", out), nil
		}
	}
	if out, err := acceptGo(ctx, wt, append([]string{"test", "-count=1"}, pkgs...)...); err != nil {
		var reds []string
		for _, m := range acceptFailLine.FindAllStringSubmatch(out, -1) {
			reds = append(reds, m[1])
		}
		if len(reds) == 0 {
			return acceptRed("red-at-head", "test", out), nil
		}
		for _, r := range reds {
			if cardTests[r] || r == j.Card.Test {
				return acceptRed("red-at-head", r, out), nil
			}
		}
		// Reds the card neither wrote nor named: once at the base (rule 5).
		if _, err := acceptGit(ctx, wt, "checkout", "--quiet", "--detach", base); err != nil {
			return acceptVerdict{}, err
		}
		_, baseErr := acceptGo(ctx, wt, append([]string{"test", "-count=1", "-run", "^(" + strings.Join(reds, "|") + ")$"}, pkgs...)...)
		if baseErr != nil {
			return acceptVerdict{Word: "ABSTAIN", Reason: "base-red", At: reds[0]}, nil
		}
		return acceptRed("red-at-head", reds[0], out), nil
	}

	// (d) the card's negative control.
	res, err := review.Mutate(ctx, review.MutateOptions{Repo: wt, Base: base, Head: head, TempRoot: root, Test: j.Card.Test})
	if errors.Is(err, review.ErrTestNotRun) {
		return acceptReject("named-test-not-red", j.Card.Test), nil
	}
	if err != nil {
		return acceptVerdict{}, fmt.Errorf("mutate: %v", err)
	}
	for _, g := range res.Greens {
		if cardTests[g.Name] {
			return acceptReject("vacuous-test", g.Name), nil
		}
	}
	if !res.Pass {
		return acceptReject("named-test-not-red", j.Card.Test), nil
	}
	return acceptVerdict{Word: "OK", Tests: res.Red + res.Green, RedWithout: res.Red}, nil
}

// acceptOverlayBaseTests is rule 4(b2)'s second half: the base's copy of every
// pre-existing test file the card changed (files, less those TEST-EDIT: excuses) is
// written over the head and the base's Tests of those files run, once, per package. A
// red, or a base copy that no longer compiles against the head, is test-weakened: the
// card changed what a test it did not own asserts. The head's files are restored after.
// done is true when v is the verdict.
func acceptOverlayBaseTests(ctx context.Context, wt, base, head string, files, excused []string) (v acceptVerdict, done bool, err error) {
	skip := map[string]bool{}
	for _, f := range excused {
		skip[f] = true
	}
	byDir := map[string][]string{}
	for _, f := range files {
		if !skip[f] {
			byDir[path.Dir(f)] = append(byDir[path.Dir(f)], f)
		}
	}
	for _, d := range acceptSorted(byDir) {
		var names []string
		for _, f := range byDir[d] {
			src, err := acceptGit(ctx, wt, "show", base+":"+f)
			if err != nil {
				return v, false, err
			}
			if parsed, perr := parser.ParseFile(token.NewFileSet(), f, src, 0); perr == nil {
				for _, decl := range parsed.Decls {
					if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && acceptTestName.MatchString(fn.Name.Name) {
						names = append(names, fn.Name.Name)
					}
				}
			}
			if err := os.WriteFile(filepath.Join(wt, filepath.FromSlash(f)), []byte(src+"\n"), 0o644); err != nil {
				return v, false, err
			}
		}
		run := "^$"
		if len(names) > 0 {
			run = "^(" + strings.Join(names, "|") + ")$"
		}
		// -vet=off: vet is step (c)'s, and its finding is vet, not this one.
		out, terr := acceptGo(ctx, wt, "test", "-count=1", "-vet=off", "-run", run, "./"+d)
		if _, err := acceptGit(ctx, wt, append([]string{"checkout", "--quiet", head, "--"}, byDir[d]...)...); err != nil {
			return v, false, err
		}
		if terr == nil {
			continue
		}
		if m := acceptFailLine.FindStringSubmatch(out); m != nil {
			return acceptRed("test-weakened", m[1], out), true, nil
		}
		// No test ran red: a compile error in an overlaid base copy is the card's (it
		// changed what that copy relies on); any other is step (c)'s build finding.
		for _, f := range byDir[d] {
			if strings.Contains(out, path.Base(f)+":") {
				return acceptRed("test-weakened", f, out), true, nil
			}
		}
	}
	return v, false, nil
}

// acceptCertMaxLife is §2 rule 6's lifetime: a record whose until= is more than this
// after its at= is not a certification, however far ahead until= reads.
const acceptCertMaxLife = 24 * time.Hour

// acceptCertWant is what a live certification must match on this run: the bench the
// gate runs on, this tool's build identity, the host's go version as the gate's own go
// reports it, whether the card changes code (§2 rule 7), and the ids voided on file.
type acceptCertWant struct {
	Bench, Build, Go string
	Code             bool
	Void             map[string]bool
}

// acceptCertLive reads a bench certification record (SPEC-TOOLWORK §2 rule 4): the
// first CERTIFY OK line. It is live only when every one of these holds, and the first
// that does not is returned as why (empty when live), for the gate to print beside its
// ABSTAIN bench-uncertified (§1 rule 3):
//
//	bench      bench= and cert= present and bench= is --bench
//	go-leg     go among legs= and not among failed=
//	stale      at= and until= parse, at= is not in the future and until= is after now
//	over-24h   until= is at most 24 hours after at= (rule 6)
//	build      build= is this tool's build identity (rule 6: nova-update adopt voids it)
//	go-version go= is the host's go version (rule 6: a changed go version voids it)
//	wall-none  wall= is present, and is not none when the card changes code (rule 7)
//	void       the record's content id is not voided on file (rule 6: an accept on this
//	           bench that abstained with reason=toolchain; see acceptCertVoid)
func acceptCertLive(raw string, now time.Time, want acceptCertWant) (why string) {
	for _, line := range strings.Split(raw, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "CERTIFY OK ")
		if !ok {
			continue
		}
		kv := map[string]string{}
		for _, field := range strings.Fields(rest) {
			if k, v, ok := strings.Cut(field, "="); ok {
				if _, dup := kv[k]; !dup {
					kv[k] = v
				}
			}
		}
		has := func(list, leg string) bool {
			for _, one := range strings.Split(list, ",") {
				if one == leg {
					return true
				}
			}
			return false
		}
		at, aerr := time.Parse(time.RFC3339, kv["at"])
		until, uerr := time.Parse(time.RFC3339, kv["until"])
		switch {
		case kv["bench"] == "" || kv["cert"] == "" || kv["bench"] != want.Bench:
			return "bench"
		case !has(kv["legs"], "go") || has(kv["failed"], "go"):
			return "go-leg"
		case aerr != nil || uerr != nil || at.After(now) || !now.Before(until):
			return "stale"
		case until.Sub(at) > acceptCertMaxLife:
			return "over-24h"
		case kv["build"] == "" || kv["build"] != want.Build:
			return "build"
		case kv["go"] == "" || kv["go"] != want.Go:
			return "go-version"
		case kv["wall"] == "" || (want.Code && kv["wall"] == "none"):
			return "wall-none"
		case want.Void[acceptCertID(raw)]:
			return "void"
		}
		return ""
	}
	return "not-a-record"
}

// acceptCertID is a record's content id: the first twelve hex of its SHA-256 (§2 rule
// 4's cert=<id>, computed rather than trusted).
func acceptCertID(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:12]
}

// acceptCertVoidPath is where the voids of the record at cert are kept: beside it, so
// the record itself is never edited (§2 rule 6: a void record is re-run, never edited).
func acceptCertVoidPath(cert string) string { return cert + ".void" }

// acceptCertVoids reads the ids voided beside the record at cert: every `CERT VOID
// cert=<id>` line. A re-run certify writes a record with a new id, which no line names.
func acceptCertVoids(cert string) map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(acceptCertVoidPath(cert))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "CERT VOID ")
		if !ok {
			continue
		}
		for _, field := range strings.Fields(rest) {
			if id, ok := strings.CutPrefix(field, "cert="); ok && id != "" {
				out[id] = true
			}
		}
	}
	return out
}

// acceptCertVoid voids the record with id on file: one appended `CERT VOID` line. It is
// called when an accept on this bench abstains with reason=toolchain (§2 rule 6).
func acceptCertVoid(cert, id, label string, now time.Time) error {
	f, err := os.OpenFile(acceptCertVoidPath(cert), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "CERT VOID cert=%s reason=toolchain label=%s at=%s\n", id, oneline.Field(label), now.UTC().Format(time.RFC3339))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// acceptHostGo is the go version the gate's own go commands run with, asked of `go env
// GOVERSION` outside any module so no go.mod's toolchain line can answer instead.
func acceptHostGo(ctx context.Context) string {
	out, err := acceptGo(ctx, os.TempDir(), "env", "GOVERSION")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// acceptRed is a REJECT on a command's output, or the bench's ABSTAIN when the first
// line that is not a notice names the wall, a missing toolchain or a full disk.
func acceptRed(token, at, out string) acceptVerdict {
	first := "-"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "go: downloading") {
			first = line
			break
		}
	}
	for _, bench := range []string{"SANDBOX DENIED", "WALL", "no space left on device", "executable file not found", "GOPROXY=off"} {
		if strings.Contains(first, bench) {
			return acceptVerdict{Word: "ABSTAIN", Reason: "toolchain"}
		}
	}
	if at == "-" {
		if m := acceptAtLine.FindStringSubmatch(first); m != nil {
			at = m[1] + ":" + m[2]
		}
	}
	return acceptReject(token, at)
}

// acceptTestFuncs is every top-level Test function declared at rev in the *_test.go
// files of dirs, name to source text. bad is the first test file that does not parse.
func acceptTestFuncs(ctx context.Context, wt, rev string, dirs map[string]bool) (map[string]string, string, error) {
	funcs := map[string]string{}
	for _, d := range acceptSorted(dirs) {
		args := []string{"ls-tree", "--name-only", rev}
		if d != "." {
			args = append(args, d+"/")
		}
		list, err := acceptGit(ctx, wt, args...)
		if err != nil {
			continue // the directory does not exist at rev
		}
		for _, file := range strings.Split(list, "\n") {
			if !strings.HasSuffix(file, "_test.go") {
				continue
			}
			src, err := acceptGit(ctx, wt, "show", rev+":"+file)
			if err != nil {
				return nil, "", err
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, file, src, 0)
			if err != nil {
				at := file
				if m := acceptAtLine.FindStringSubmatch(err.Error()); m != nil {
					at = m[1] + ":" + m[2]
				}
				return funcs, at, nil
			}
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && acceptTestName.MatchString(fn.Name.Name) {
					funcs[fn.Name.Name] = src[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset]
				}
			}
		}
	}
	return funcs, "", nil
}

func acceptSorted[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// acceptGo runs one go command in dir, once, and returns its combined output.
func acceptGo(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = goenv.Clean(acceptEnv())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// acceptGit runs git in dir with no user or system config, so a worker's config,
// hooks and filters are never the gate's programs, and returns trimmed stdout.
func acceptGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(acceptEnv(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		detail := ""
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s: %v %s", strings.Join(args, " "), err, detail)
	}
	return strings.TrimSpace(string(out)), nil
}

// acceptEnv is the environment of every child the gate runs: no secret-named variable,
// no GIT_* (a parent's GIT_DIR would point git back at the worker's clone), none of
// goenv's output-shape variables, no workspace file and no module proxy (rule 10: the
// gate makes no network call, so a module the cache does not hold is a toolchain
// abstain, not a download).
func acceptEnv() []string {
	var out []string
	for _, kv := range goenv.Clean(goenv.WithoutSecrets(os.Environ())) {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "GOPROXY=") || strings.HasPrefix(kv, "GOWORK=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GOPROXY=off", "GOWORK=off")
}

// acceptControlID is rule 8's id: the first twelve hex of SHA-256 over the gate's build
// identity, the fixture tree's digest and the bench certification id.
func acceptControlID(build, fixture, cert string) string {
	sum := sha256.Sum256([]byte(build + "\x00" + fixture + "\x00" + cert))
	return hex.EncodeToString(sum[:])[:12]
}

// acceptFixtureDigest is SHA-256 over the shipped fixture tree, path and bytes of every
// file in walk order, so a changed seed or fixture file is a new control id.
func acceptFixtureDigest() string {
	h := sha256.New()
	_ = fs.WalkDir(acceptFixtureFS, "testdata/accept", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := acceptFixtureFS.ReadFile(p)
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(raw))
		h.Write(raw)
		return err
	})
	return hex.EncodeToString(h.Sum(nil))
}

func cmdAccept(args []string, stdout, stderr io.Writer, now time.Time) int {
	start := time.Now()
	f := newFlags("accept")
	job := f.fs.String("job", "", "")
	cardPath := f.fs.String("card", "", "")
	base := f.fs.String("base", "", "")
	bench := f.fs.String("bench", "", "")
	cert := f.fs.String("cert", "", "")
	identity := f.fs.String("identity", "", "")
	timeout := f.fs.Int("timeout", 600, "")
	selftest := f.fs.Bool("selftest", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	if *selftest {
		fmt.Fprintln(stderr, "ACCEPT REFUSED: --selftest is not built yet (recut-2222 part 2 runs the twelve seeds through this gate)")
		return 2
	}
	f.want(*job, "job", "the job's git clone")
	f.want(*cardPath, "card", "the card file cut wrote")
	f.want(*base, "base", "the ref the card was cut at")
	f.want(*bench, "bench", "the bench name")
	f.want(*cert, "cert", "the bench's certification record")
	var ids []hygiene.Identity
	for _, one := range strings.Split(*identity, ",") {
		name, email, ok := strings.Cut(strings.TrimSpace(one), "<")
		if !ok || !strings.HasSuffix(email, ">") || strings.TrimSpace(name) == "" {
			f.add(fmt.Sprintf("--identity %q: want `Name <email>`, repeatable with commas", one))
			continue
		}
		ids = append(ids, hygiene.Identity{Name: strings.TrimSpace(name), Email: strings.TrimSuffix(email, ">")})
	}
	if *timeout <= 0 {
		f.add("--timeout must be positive")
	}
	if f.refused(stderr) {
		return 2
	}
	raw, err := os.ReadFile(*cardPath)
	if err != nil {
		fmt.Fprintf(stderr, "ACCEPT REFUSED: %s (give the card file cut wrote)\n", oneline.Err(err))
		return 2
	}
	card, err := parseAcceptCard(string(raw), path.Base(*cardPath))
	if err != nil {
		fmt.Fprintf(stderr, "ACCEPT REFUSED: %s (fix the card's header)\n", oneline.Err(err))
		return 2
	}
	abstain := func(reason string) int {
		fmt.Fprintf(stdout, "ACCEPT ABSTAIN label=%s kind=%s reason=%s bench=%s took=%s\n", oneline.Field(card.Label),
			oneline.Field(card.Kind), reason, oneline.Field(*bench), time.Since(start).Round(time.Millisecond))
		return 2
	}
	certRaw, err := os.ReadFile(*cert)
	if err != nil {
		fmt.Fprintf(stderr, "ACCEPT CERT void=unreadable (%s)\n", oneline.Err(err))
		return abstain("bench-uncertified")
	}
	want := acceptCertWant{Bench: *bench, Build: buildVersion(), Go: acceptHostGo(context.Background()),
		Code: acceptGatedKinds[card.Kind], Void: acceptCertVoids(*cert)}
	if why := acceptCertLive(string(certRaw), now, want); why != "" {
		fmt.Fprintf(stderr, "ACCEPT CERT void=%s (re-run nova-pulse certify for this bench; a record is never edited)\n", why)
		return abstain("bench-uncertified")
	}
	if !acceptGatedKinds[card.Kind] {
		return abstain("unknown-kind")
	}
	if card.Test == "" {
		fmt.Fprintln(stderr, "ACCEPT REFUSED: the card names no TEST: (a gated kind's card names its test)")
		return 2
	}
	certID := acceptCertID(string(certRaw))
	control := acceptControlID(buildVersion(), acceptFixtureDigest(), certID)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()
	v, err := acceptGate(ctx, acceptJob{Job: *job, Base: *base, Card: card, Identities: ids})
	if ctx.Err() != nil {
		return abstain("timeout")
	}
	if err != nil {
		fmt.Fprintf(stderr, "ACCEPT REFUSED: %s (the gate could not run; nothing was judged)\n", oneline.Err(err))
		return 2
	}
	took := time.Since(start).Round(time.Millisecond)
	switch v.Word {
	case "OK":
		fmt.Fprintf(stdout, "ACCEPT OK label=%s kind=%s head=%s base=%s tests=%d red_without=%d edits=- control=%s bench=%s cert=%s took=%s\n",
			oneline.Field(card.Label), oneline.Field(card.Kind), acceptShort(v.Head), acceptShort(v.Base), v.Tests, v.RedWithout,
			control, oneline.Field(*bench), certID, took)
		return 0
	case "ABSTAIN":
		if v.Reason == "toolchain" {
			if err := acceptCertVoid(*cert, certID, card.Label, now); err != nil {
				fmt.Fprintf(stderr, "ACCEPT CERT void-not-written: %s (the record stays live; re-run certify by hand)\n", oneline.Err(err))
			}
		}
		return abstain(v.Reason)
	}
	fmt.Fprintf(stdout, "ACCEPT REJECT label=%s kind=%s head=%s reason=%s at=%s control=%s bench=%s cert=%s took=%s\n",
		oneline.Field(card.Label), oneline.Field(card.Kind), acceptShort(v.Head), v.Reason, oneline.Field(v.At),
		control, oneline.Field(*bench), certID, took)
	return 1
}

// acceptShort is a sha as the verdict lines print it: twelve hex, or - when absent.
func acceptShort(sha string) string {
	if len(sha) < 12 {
		return "-"
	}
	return sha[:12]
}
