package swarm

// THE LEG'S GATE: the gate the HARVEST runs, outside the wall (nova-tools#2386).
//
// THE BREAK. card-tools22-fix-2035's RESULT.md said `gofmt -l internal/swarm/ -> (no
// output) OK`; the lane's gate on hulk at the same head printed
// `internal/swarm/issue2035_test.go`. Two more false greens were caught by the lane
// before any PR (parallel gates in one shared clone; `gofmt -l` exits 0 even when it
// lists files). A card can report a gate it did not get, and a reader trusts the PR body.
//
// THE ASK, and this file's share of it. harvest stages the returned branch outside the
// wall on a gate bench, runs the leg's gate (go vet, go test of the touched packages,
// `gofmt -l` with a nonzero exit when it lists, `nova-swarm lint --card`), and writes the
// verbatim gate line, bench and sha into the PR body under a fixed heading; a PR whose
// body carries a card-written gate line and no harvest gate line is refused by
// `nova-merge simulate`. The card's own claims stay in RESULT.md as data.
//
// This file is the gate itself and the PR-body shape, and NOTHING ELSE:
//
//   - RunLegGate runs the four commands through the Run seam and composes the verdict.
//     It never reads a RESULT.md: the card's own `## Gates` rows are data and are not an
//     input to the gate. A card's self-reported gate cannot make this gate green, and a
//     green self-report over a red tree is the exact false green this file exists to end.
//   - AppendHarvestGate composes the PR body: the card's own body first, verbatim and
//     untouched, then the harvest's gate section under HarvestGateHeading -- the one
//     heading a reader (and `nova-merge simulate`) may trust. The heading is the
//     HARVEST's: a `## Harvest gate` section a card typed into its RESULT is stripped
//     before the harvest's is written, so a card-written gate line never survives as the
//     harvest's.
//   - The staging of the branch on the gate bench, the call from the harvest fold, and
//     the `nova-merge simulate` refusal are the lane that owns `internal/pulse` and
//     `internal/merge`. Until those cards land, nothing in this package calls RunLegGate:
//     it is the seam they will call, the way `ReadTrustFixture` is the seam
//     `nova-pulse trust` lands against (lintheader.go, "the verb does not exist yet").
//
// WHY THE GATE IS HERE AND NOT IN A CARD. A card runs INSIDE the wall, and the wall's
// own approved-command list (wallterms.go) grants it `go` and `gofmt` but no `nova-*`
// binary at all -- `nova-swarm lint --card` cannot even be typed by a card the wall
// admits. And whatever a card did run, its report of it is a claim. The gate a reader
// trusts is the one the machinery runs itself, outside the wall, on the staged branch.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// HarvestGateHeading is the fixed PR-body heading the harvest's gate line is written
// under, the one heading a reader may trust. Everything under it is the harvest's own:
// the section is stripped and rewritten on every append, so a card-typed copy of the
// heading never survives a harvest pass (issue #2386: "a card's self-reported gate is
// data, never a gate").
const HarvestGateHeading = "## Harvest gate"

// The gate's own three verdict words, the report vocabulary's own (result.go's
// gateResults): pass, fail, not run. A check that could not run at all is fail, never
// not run -- a gate that cannot go green is not a gate.
const (
	gatePass   = "pass"
	gateFail   = "fail"
	gateNotRun = "not run"
)

// GateTimeout bounds one gate command. The gate runs the touched packages only, and the
// per-package time budget answers in two minutes at most; ten is the outer bound that
// keeps a hung bench from hanging the harvest with it.
const GateTimeout = 10 * time.Minute

// GateOutBytes bounds what one command's verbatim output contributes to the gate
// section. `go test` can print screens on a red package; the reader needs the verdict
// line and the first of the hurt, and the body has a budget like any other.
const GateOutBytes = 400

// GateSectionBytes is the PR-body budget the whole gate section is bounded to when the
// caller names none: the same 4096 the harvest bounds a body to.
const GateSectionBytes = 4096

// GateRun is one gate command's own answer: everything it printed, its exit code, and
// whether it could be run at all. Err is the could-not-run case -- a command that was
// not found, or a bench that refused it -- and it is never a red exit: a red exit is an
// answer the gate reads and reports.
type GateRun struct {
	Out  string
	Exit int
	Err  error
}

// GateRunner runs one gate command with dir as its working directory. The shipped one,
// ExecGateRunner, execs it on this machine through the PATH; a bench form runs it over
// the gate bench's own shell, the same seam `nova-pulse harvest --bench` reads; a test's
// fake records the argv and answers, so no test of this package runs go, gofmt or
// nova-swarm.
type GateRunner func(dir string, argv ...string) GateRun

// ExecGateRunner is the shipped GateRunner: one bounded child in dir, its combined
// output verbatim, its exit code, and Err only when it could not be run at all.
func ExecGateRunner(dir string, argv ...string) GateRun {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return GateRun{Err: errors.New("no command named")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), GateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	run := GateRun{Out: string(out)}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		run.Exit = exitErr.ExitCode()
		return run
	}
	if err != nil {
		run.Err = err
	}
	return run
}

// GateCommand is one gate check's record: the command as the gate ran it, the verdict,
// why a check did not run or failed, and what the command printed, verbatim and bounded.
type GateCommand struct {
	Name   string // vet | test | gofmt | lint
	Line   string // the command as the gate ran it, one line
	Result string // pass | fail | not run
	Why    string // the not-run reason, or the fail sentence; "" on a pass
	Out    string // what the command printed, verbatim, bounded
}

// LegGateInput is everything the gate needs. The harvest fills it from what it holds:
// the bench it staged the branch on, the branch's head, the staged tree itself, the
// card's own PATHS and the card file. It holds no RESULT.md and no self-reported gate:
// those are data and never an input.
type LegGateInput struct {
	Bench string     // the gate bench the branch was staged on
	SHA   string     // the staged branch's head, the sha the gate ran at
	Dir   string     // the staged branch, outside the wall; "" is refused below, never guessed
	Card  string     // the card file for `nova-swarm lint --card`; "" and the lint is not run
	Paths []string   // the card's PATHS globs; the touched packages come from them
	Run   GateRunner // the command seam; nil is ExecGateRunner
}

// LegGateResult is the gate's answer: the bench and sha it ran at, the four commands'
// records in the issue's order (go vet, go test, gofmt -l, nova-swarm lint --card), and
// the verdict. OK is true only when no command failed: a check that did not run because
// the leg names no Go package, or no card was handed, is the leg's own shape and not a
// failure; a check that COULD not run is a failure and never a pass.
type LegGateResult struct {
	Bench    string
	SHA      string
	Commands []GateCommand
	OK       bool
}

// RunLegGate runs the leg's gate itself on the staged branch and composes the verdict.
// It reads nothing the card wrote about its own gate: the tree is the only witness.
func RunLegGate(in LegGateInput) LegGateResult {
	run := in.Run
	if run == nil {
		run = ExecGateRunner
	}
	r := LegGateResult{Bench: in.Bench, SHA: in.SHA}
	add := func(c GateCommand) {
		if c.Result == gateFail {
			r.OK = false
		}
		r.Commands = append(r.Commands, c)
	}
	r.OK = true

	// No guessed paths: a gate with no staged tree runs nothing in the working
	// directory by accident. The tree checks are refused, and only a card-named lint
	// could still run -- though a caller that staged nothing has no card to name either.
	pkgs := gatePackages(in.Dir, in.Paths)
	if strings.TrimSpace(in.Dir) == "" {
		add(GateCommand{Name: "vet", Line: "go vet", Result: gateFail,
			Why: "the staged branch is not named; the gate refuses to guess a directory"})
		add(GateCommand{Name: "test", Line: "go test", Result: gateFail,
			Why: "the staged branch is not named; the gate refuses to guess a directory"})
	} else if len(pkgs) == 0 {
		// The leg's own shape: a card whose PATHS name no Go package has no packages
		// to vet or test. The words are the report's own (`not run`), so a reader of
		// the body knows the gate looked and had nothing to run.
		add(GateCommand{Name: "vet", Line: "go vet", Result: gateNotRun,
			Why: "no touched packages: the card's PATHS name no Go file or package"})
		add(GateCommand{Name: "test", Line: "go test", Result: gateNotRun,
			Why: "no touched packages: the card's PATHS name no Go file or package"})
	} else {
		vet := append([]string{"go", "vet"}, pkgs...)
		test := append([]string{"go", "test", "-count=1"}, pkgs...)
		add(gateCheck(run, in.Dir, "vet", vet))
		// -count=1: a cached `ok` is a read of a stamp, not a run, and this gate
		// runs (the issue's whole hurt is a gate that was reported but not run).
		add(gateCheck(run, in.Dir, "test", test))
	}

	// `gofmt -l` WITH A NONZERO EXIT WHEN IT LISTS. gofmt's own exit is 0 even when it
	// lists files, and that is the exact hole the false green came through: the card
	// reported `(no output) OK` off the same command. The gate reads the listing
	// itself -- anything listed is a red gate, whatever gofmt's exit code said.
	gofmt := GateCommand{Name: "gofmt", Line: "gofmt -l ."}
	if strings.TrimSpace(in.Dir) == "" {
		gofmt.Result, gofmt.Why = gateFail, "the staged branch is not named; the gate refuses to guess a directory"
		add(gofmt)
	} else {
		out := run(in.Dir, "gofmt", "-l", ".")
		gofmt.Out = oneline.Cap(strings.TrimRight(out.Out, "\n"), GateOutBytes)
		switch {
		case out.Err != nil:
			gofmt.Result, gofmt.Why = gateFail, "could not run: "+oneline.Err(out.Err)
		case strings.TrimSpace(out.Out) != "":
			gofmt.Result, gofmt.Why = gateFail,
				fmt.Sprintf("gofmt -l listed %d file(s); a listing is a red gate whatever gofmt's own exit was (it exited %d)", len(strings.Fields(out.Out)), out.Exit)
		case out.Exit != 0:
			gofmt.Result, gofmt.Why = gateFail, fmt.Sprintf("exit %d", out.Exit)
		default:
			gofmt.Result = gatePass
		}
		add(gofmt)
	}

	// `nova-swarm lint --card`. The wall a card runs inside grants no exec right on a
	// nova-* binary (#2162), so this is the one check a card could never have run for
	// itself even honestly; the gate bench runs it with the card the harvest holds.
	// The lint reads the card, not the staged tree, so it still runs when no tree was
	// named -- in the card's own directory, never in a guessed one.
	lint := GateCommand{Name: "lint"}
	if strings.TrimSpace(in.Card) == "" {
		lint.Line, lint.Result, lint.Why = "nova-swarm lint --card", gateNotRun, "no card named"
	} else {
		lint.Line = "nova-swarm lint --card " + in.Card
		lintDir := in.Dir
		if strings.TrimSpace(lintDir) == "" {
			lintDir = filepath.Dir(in.Card)
		}
		lintCheck := run(lintDir, "nova-swarm", "lint", "--card", in.Card)
		lint.Out = oneline.Cap(strings.TrimRight(lintCheck.Out, "\n"), GateOutBytes)
		switch {
		case lintCheck.Err != nil:
			lint.Result, lint.Why = gateFail, "could not run: "+oneline.Err(lintCheck.Err)
		case lintCheck.Exit != 0:
			lint.Result, lint.Why = gateFail, fmt.Sprintf("exit %d", lintCheck.Exit)
		default:
			lint.Result = gatePass
		}
	}
	add(lint)

	return r
}

// gateCheck runs one command-shaped check and reads its verdict off its exit: exit 0 is
// pass, anything else is fail, and could-not-run is fail too -- never a pass.
func gateCheck(run GateRunner, dir, name string, argv []string) GateCommand {
	c := GateCommand{Name: name, Line: strings.Join(argv, " ")}
	out := run(dir, argv...)
	c.Out = oneline.Cap(strings.TrimRight(out.Out, "\n"), GateOutBytes)
	switch {
	case out.Err != nil:
		c.Result, c.Why = gateFail, "could not run: "+oneline.Err(out.Err)
	case out.Exit != 0:
		c.Result, c.Why = gateFail, fmt.Sprintf("exit %d", out.Exit)
	default:
		c.Result = gatePass
	}
	return c
}

// gatePackages is the touched packages of a card's PATHS, read off the staged tree. A
// PATHS entry that names a Go file (`internal/swarm/issue2035_test.go`) names the
// package its directory is; an entry that names a directory whole (`internal/swarm/**`)
// names that directory. A directory only counts while it is on the staged tree and
// holds a Go file -- `docs/**` contributes nothing, so a text card's gate is gofmt and
// lint and not a red `go vet ./docs` that names no Go at all.
func gatePackages(dir string, paths []string) []string {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
		p = strings.TrimSuffix(p, "/**")
		if p == "" || strings.ContainsAny(p, "*?[") || strings.Contains(p, "..") {
			continue
		}
		if base := path.Base(p); strings.Contains(base, ".") && !strings.HasPrefix(base, ".") {
			p = path.Dir(p)
		}
		if p == "" || p == "." {
			continue
		}
		if !holdsGoFile(filepath.Join(dir, filepath.FromSlash(p))) {
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, "./"+p)
		}
	}
	sort.Strings(out)
	return out
}

// holdsGoFile reports whether dir is on this tree and holds at least one Go file. A
// directory that is not there, or holds no Go, is not a package this gate can test.
func holdsGoFile(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// LegGateLine is the gate's one line: the harvest, the bench it ran on, the sha it ran
// at, the verdict, and the counts. It is the line the PR body carries under
// HarvestGateHeading and the line a reader may trust; a card's own gate claims never
// appear on it.
func LegGateLine(r LegGateResult) string {
	failed := 0
	for _, c := range r.Commands {
		if c.Result == gateFail {
			failed++
		}
	}
	verdict := "green"
	if failed > 0 {
		verdict = "red"
	}
	sha := Short(strings.TrimSpace(r.SHA))
	if sha == "" {
		sha = "-"
	}
	return fmt.Sprintf("HARVEST GATE bench=%s sha=%s result=%s checks=%d failed=%d",
		oneline.Field(r.Bench), oneline.Field(sha), verdict, len(r.Commands), failed)
}

// AppendHarvestGate composes the PR body the harvest publishes: the card's own body
// first, VERBATIM and untouched -- a card's self-reported gate is data, and it stays in
// the body as the card wrote it -- then the harvest's gate section under
// HarvestGateHeading, bounded to max bytes (GateSectionBytes when max is 0 or negative).
//
// THE HEADING IS THE HARVEST'S. A `## Harvest gate` section already in the body -- one
// the card typed into its RESULT, or one an earlier harvest wrote when the PR was
// updated in place -- is stripped before the harvest's is written, so the section is
// rewritten and never stacked, and a card-written gate line never survives as the
// harvest's.
func AppendHarvestGate(body string, r LegGateResult, max int) string {
	if max <= 0 {
		max = GateSectionBytes
	}
	kept := strings.TrimRight(stripHarvestGateSection(body), "\n \t")
	var b strings.Builder
	b.WriteString(HarvestGateHeading)
	b.WriteString("\n")
	line := LegGateLine(r)
	b.WriteString(line)
	b.WriteString("\n")
	for _, c := range r.Commands {
		row := c.Line + ": " + c.Result
		if c.Why != "" {
			row += " (" + oneline.Escape(c.Why) + ")"
		}
		if len(row) > max-len(b.String()) {
			break
		}
		b.WriteString(row)
		b.WriteString("\n")
		if c.Out != "" {
			for _, l := range strings.Split(c.Out, "\n") {
				if len(l) > max-len(b.String()) {
					break
				}
				b.WriteString(l)
				b.WriteString("\n")
			}
		}
	}
	section := strings.TrimRight(b.String(), "\n")
	if len(section) > max {
		section = oneline.Cap(section, max)
	}
	if kept == "" {
		return section + "\n"
	}
	return kept + "\n\n" + section + "\n"
}

// stripHarvestGateSection removes the harvest's own section from a body: the heading
// line and every line after it up to the next `## ` heading, or the end. The section is
// heading-delimited, so a card that typed the heading mid-body loses exactly its typed
// section and no more.
func stripHarvestGateSection(body string) string {
	norm := strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	var out []string
	skipping := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == HarvestGateHeading {
			skipping = true
			continue
		}
		if skipping && strings.HasPrefix(t, "## ") {
			skipping = false
		}
		if !skipping {
			out = append(out, l)
		}
	}
	// The blank lines the stripped section used to sit between are not left behind as
	// a gap a reader wonders about.
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
