package release

// THE DEFINITION OF DONE, IN FRONT OF THE TAG.
//
// Glenn, 2026-09-18: a tool is finished when it has been tested, dogfooded by
// somebody who did NOT write it on real work, the edges that found have been
// filed, and the fixes have been applied. `nova-check dogfood gate` made that
// mechanical -- receipts on disk, read against the command reference, an exit
// code (internal/dogfood). Nothing asked it before a release. The claim that a
// release was dogfooded was whatever the last person said it was, and a tag is
// the one thing in this repository that cannot be quietly amended and pushed
// again.
//
// So `cut` and `build` ask it FIRST -- before the forge is read, before a
// single tool is compiled -- and refuse on an open edge. An open edge is
// somebody having run a verb, it not having done what they needed, and nobody
// having run it since and said it did: feedback FILED is not feedback APPLIED,
// which is the third step of the definition and the one that used to go
// missing.
//
// The gate is the same read the CLI does, in process rather than through a
// shell: `nova-check dogfood gate --cli <cli> --receipts <dir>` is
// internal/dogfood.Gate over dogfood.ParseCLI and dogfood.ReadReceipts, and
// calling it directly is one process, one set of refusals, and no shell to get
// wrong (Glenn, 2026-09-17: no shell for coordination).

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
)

// DefaultReceiptsDir is where this fleet keeps its receipts, relative to the
// home directory of whoever is cutting. It is the ONLY path in this package
// with a default, and it is one on purpose: SPEC-UPDATE rule 1 says no path is
// guessed, and the reason the rule exists is that a guessed path makes two
// runs mean different things. A receipts directory is the exception because
// the alternative -- a release lane that silently skips the gate whenever
// somebody forgets a flag -- fails in the direction that lets a tool ship. It
// is used only when it EXISTS, and what was used is named on the line.
var DefaultReceiptsDir = filepath.Join("rowan-working", "dogfood")

// DogfoodWaiveFlag and DogfoodReasonFlag are the way past the gate, spelled in
// one place so the remedy a person is handed is the flag they then type.
const (
	DogfoodWaiveFlag  = "--no-dogfood-gate"
	DogfoodReasonFlag = "--reason"
)

// DogfoodRemedy is what the refusal tells somebody to do about it. Two ways
// out and both are work: fix the edges, or waive the gate and say why where
// the waiver will outlive the terminal.
const DogfoodRemedy = "fix the open edges or " + DogfoodWaiveFlag + " " + DogfoodReasonFlag + " <why>"

// DogfoodWaiverPrefix is how the CHANGELOG section names a waived gate. The
// waiver travels with the release, in the file a person reads to find out what
// a version is, because a waiver that lives only in one terminal's scrollback
// is a waiver nobody can weigh in six months.
const DogfoodWaiverPrefix = "Dogfood gate waived: "

// DogfoodNote is the gate said where a person will meet it: on `release help`,
// and on `--help` for the two verbs that run it.
var DogfoodNote = "cut and build run the dogfood gate FIRST -- `nova-check dogfood gate --cli <reference> --receipts <dir>`, in process -- and refuse on an OPEN EDGE: " +
	"a verb somebody ran, that did not do what they needed, and that nobody has run since and said it did. " +
	"Glenn, 2026-09-18: a tool is done when it is tested, dogfooded by a non-author on real work, and the feedback is APPLIED; feedback filed is not feedback applied. " +
	"--cli names the command reference and defaults to docs/CLI.md beside the checkout the verb was already given (--changelog for cut, --source for build). " +
	"--receipts names the receipts and defaults to ~/" + DefaultReceiptsDir + " when that directory exists. " +
	"A run with neither is NOT a run that passed: it prints `dogfood-gate=skipped` and names what was missing. " +
	"The way past an open edge is to fix it, or " + DogfoodWaiveFlag + " " + DogfoodReasonFlag + " <why> -- and the waiver is printed on the line AND written into the CHANGELOG section, because a waiver nobody can find later is a gate nobody has."

// addDogfoodFlags puts the gate's flags on one verb's flag set. Both verbs
// that run the gate get them from here, so `cut` and `build` cannot drift
// apart in what they will accept.
func addDogfoodFlags(f *flag.FlagSet, o *options, cliDefault string) {
	f.StringVar(&o.cli, "cli", "", "the command reference the dogfood gate reads its verbs from (default: "+cliDefault+")")
	f.StringVar(&o.receipts, "receipts", "", "the dogfood receipts directory (default: ~/"+DefaultReceiptsDir+" when it exists)")
	f.BoolVar(&o.noDogfood, "no-dogfood-gate", false, "release without the definition of done; "+DogfoodReasonFlag+" <why> is then required")
	f.StringVar(&o.reason, "reason", "", "why the gate was waived; it goes on the line and into the changelog")
}

// dogfoodFindingCap bounds the refusal. Tool output costs tokens (Glenn,
// 2026-09-11): the COUNT is the answer, the first few edges are the orientation,
// and a person who wants all of them runs the ledger.
const dogfoodFindingCap = 10

// errDogfood says the gate said no and that the refusal has ALREADY been
// printed, in the field-line shape the remedy needs. It is a sentinel for the
// same reason errTruncated is: this refusal does not read `CUT REFUSED:
// <prose>`, so that a person or a script meeting it in a log can tell
// `reason=dogfood-gate` from every other reason a release can refuse.
var errDogfood = errors.New("dogfood-gate")

// DogfoodVerdict is what the gate answers: what it read, and what it found.
type DogfoodVerdict struct {
	Verbs int
	Open  int
	// Findings are the gate's own lines, one per open edge, in the words
	// `nova-check dogfood gate` prints them. The release lane says what the
	// gate says rather than paraphrasing it: two spellings of one finding is
	// one of them going stale.
	Findings []string
}

// Dogfood is the seam. A nil Dogfood in Deps is the production one, which
// reads two files off disk and reaches nothing else -- no forge, no network,
// no shell.
type Dogfood func(cli, receipts string) (DogfoodVerdict, error)

// ReadDogfood is that production gate.
func ReadDogfood(cli, receipts string) (DogfoodVerdict, error) {
	verbs, err := dogfood.ParseCLI(cli)
	if err != nil {
		return DogfoodVerdict{}, refuse("name the command reference with --cli <file>, usually docs/CLI.md",
			"cannot read the command reference: %s", err)
	}
	got, failures, err := dogfood.ReadReceipts(receipts)
	if err != nil {
		return DogfoodVerdict{}, refuse("name the receipts directory with --receipts <dir>",
			"cannot read the receipts: %s", err)
	}
	// A BROKEN RECORD IS NOT AN ABSENT ONE. A receipt that will not parse is
	// evidence somebody tried to record something; reading past it would make
	// the gate report a shorter, greener truth than the one on disk.
	if len(failures) > 0 {
		return DogfoodVerdict{}, refuse("fix the named receipt, or remove it if it was never a receipt",
			"%s in %s will not parse: %s: %s", plural(len(failures), "receipt"), receipts,
			failures[0].Subject, failures[0].Reason)
	}
	// requireAll is FALSE. `cut` asks the question the release actually turns
	// on -- is there an edge somebody found and nobody fixed -- and not the
	// stronger one, whether every verb in the reference has been run by a
	// non-author. The stronger question is `dogfood gate --require-all`, and a
	// tag held hostage to the last unrun verb in a 200-verb reference is a tag
	// nobody ever cuts.
	findings, summary := dogfood.Gate(verbs, got, nil, false)
	v := DogfoodVerdict{Verbs: summary.Verbs, Open: len(findings)}
	for _, f := range findings {
		v.Findings = append(v.Findings, f.Line())
	}
	return v, nil
}

// dogfoodPaths resolves what the gate reads.
//
// THE REFERENCE IS RESOLVED FIRST, AND AN ABSENT ONE ENDS IT. `cut` and
// `build` are already told a path inside the checkout -- the changelog, the
// source tree -- so the reference beside it is derived rather than retyped;
// but a derived path that is not there means this is not a nova-tools checkout
// and there is nothing to gate against. Deciding that before the home
// directory is consulted is also what keeps this package's own tests honest:
// a test working in a temp directory can never reach a real fleet's receipts.
func dogfoodPaths(o options, derived string) (cli, receipts string) {
	cli = o.cli
	if cli == "" && derived != "" && exists(derived) {
		cli = derived
	}
	if cli == "" {
		return "", ""
	}
	receipts = o.receipts
	if receipts == "" {
		if home, err := os.UserHomeDir(); err == nil {
			if d := filepath.Join(home, DefaultReceiptsDir); exists(d) {
				receipts = d
			}
		}
	}
	return cli, receipts
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dogfoodCheck is the gate as `cut` and `build` run it. It answers the token
// the receipt line carries -- ok, waived or skipped -- and an error, which is
// either an ordinary refusal or errDogfood for the one whose line is already
// written.
func dogfoodCheck(token string, o options, deps Deps, derived string, out, errs io.Writer) (string, error) {
	if o.noDogfood {
		// A WAIVER WITHOUT A REASON IS NOT A WAIVER. It is the gate turned
		// off, which is the state this whole file exists to make impossible to
		// reach by accident.
		if strings.TrimSpace(o.reason) == "" {
			return "", refuse("say why: "+DogfoodWaiveFlag+" "+DogfoodReasonFlag+" <why>",
				"%s waives the definition of done and no reason was given", DogfoodWaiveFlag)
		}
		// ON STDOUT, above the receipt, for the same reason RELEASE CUT
		// SENSITIVE is: a release that went round the gate is a fact somebody
		// reads off the terminal today and out of a log in six months.
		fmt.Fprintf(out, "RELEASE %s DOGFOOD WAIVED reason=%s\n", token, field(o.reason))
		return "waived", nil
	}
	cli, receipts := dogfoodPaths(o, derived)
	if cli == "" || receipts == "" {
		// NAMED, NEVER SILENT. The gate could not run, which is not the same
		// as the gate passing, and the line says which of the two inputs was
		// missing so the remedy is one flag rather than a guess.
		fmt.Fprintf(errs, "RELEASE %s NOTE dogfood-gate=skipped cli=%s receipts=%s remedy=%q\n",
			token, field(cli), field(receipts),
			"name both with --cli <file> and --receipts <dir> so the definition of done is checked before the release")
		return "skipped", nil
	}
	read := deps.Dogfood
	if read == nil {
		read = ReadDogfood
	}
	progress(errs, "asking the dogfood gate about %s against the receipts in %s", cli, receipts)
	v, err := read(cli, receipts)
	if err != nil {
		return "", err
	}
	if v.Open == 0 {
		return "ok", nil
	}
	shown := v.Findings
	if len(shown) > dogfoodFindingCap {
		shown = shown[:dogfoodFindingCap]
	}
	for _, line := range shown {
		fmt.Fprintln(errs, line)
	}
	if len(shown) < len(v.Findings) {
		fmt.Fprintf(errs, "DOGFOOD GATE MORE open=%d shown=%d remedy=%q\n", v.Open, len(shown),
			fmt.Sprintf("read them all: nova-check dogfood ledger --cli %s --receipts %s", cli, receipts))
	}
	fmt.Fprintf(errs, "RELEASE %s REFUSED reason=dogfood-gate open=%d remedy=%q\n", token, v.Open, DogfoodRemedy)
	return "", errDogfood
}

// dogfoodWaiver is the waiver as the CHANGELOG carries it, and the empty
// string when the gate was not waived.
func dogfoodWaiver(state, reason string) string {
	if state != "waived" {
		return ""
	}
	return strings.TrimSpace(reason)
}
