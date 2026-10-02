package release

// The release dogfood gate reads reports of real use (SPEC-RELEASE §12).
// An open edge is a reported failure with no later successful use closing it.
// Filing feedback alone does not close the edge.
//
// When both inputs are available, cut and build run the gate before forge
// reads or compilation. Missing inputs skip it; an explicit waiver requires
// a reason. Those outcomes are recorded separately from a passed gate.
// ReadDogfood uses the same parser and judgment as nova-check dogfood gate,
// in process, without starting a shell.

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

// DogfoodNote explains the gate's inputs and its distinct pass, skip and waiver outcomes.
var DogfoodNote = "cut and build check dogfood receipts before forge reads or compilation when both inputs are available.\n" +
	"The gate refuses an open edge: a reported failure with no later successful use closing it.\n" +
	"It checks reported failures; it does not require a receipt for every verb.\n\n" +
	"--cli names the command reference. If omitted, an existing docs/CLI.md is read from\n" +
	"the checkout named by --changelog (cut) or --source (build).\n" +
	"--receipts names the receipts directory; there is no default.\n" +
	"If no reference is resolved or no receipts directory is named, the release continues\n" +
	"with dogfood-gate=skipped and a note naming the missing input. Skipped does not mean passed.\n\n" +
	"To proceed despite an open edge, fix it or use " + DogfoodWaiveFlag + " " + DogfoodReasonFlag + " <why>.\n" +
	"The waiver appears in the release output and, for cut, in the CHANGELOG section."

// addDogfoodFlags puts the gate's flags on one verb's flag set. Both verbs
// that run the gate get them from here, so `cut` and `build` cannot drift
// apart in what they will accept.
func addDogfoodFlags(f *flag.FlagSet, o *options, cliDefault string) {
	f.StringVar(&o.cli, "cli", "", "the command reference the dogfood gate reads its verbs from (default: "+cliDefault+")")
	f.StringVar(&o.receipts, "receipts", "", "the dogfood receipts directory; without it the gate is skipped and the line says so")
	f.BoolVar(&o.noDogfood, "no-dogfood-gate", false, "waive the dogfood gate; "+DogfoodReasonFlag+" <why> is then required")
	f.StringVar(&o.reason, "reason", "", "why the gate was waived; it goes on the line and into the changelog")
}

// dogfoodFindingCap bounds the refusal. Tool output costs tokens: the COUNT is
// the answer, the first few edges are the orientation,
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
	// Shipped is how many tools the gate judged, and Outside how many
	// receipts it set aside because they name a tool the release does not
	// ship. Both are said on the line: evidence left out is counted.
	Shipped int
	Outside int
	// Findings are the gate's own lines, one per open edge, in the words
	// `nova-check dogfood gate` prints them. The release lane says what the
	// gate says rather than paraphrasing it: two spellings of one finding is
	// one of them going stale.
	Findings []string
}

// Dogfood is the seam. A nil Dogfood in Deps is the production one, which
// reads the reference, the receipts and the cmd/ directory off disk and
// reaches nothing else -- no forge, no network, no shell.
type Dogfood func(cli, receipts, cmd string) (DogfoodVerdict, error)

// ReadDogfood is that production gate. cmd is the checkout's cmd/ directory:
// the tools under it are the shipped set, and the gate judges only them. An
// empty cmd judges every tool the receipts name, which is the stricter read.
func ReadDogfood(cli, receipts, cmd string) (DogfoodVerdict, error) {
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
	// The release gate checks reported failures, without requiring a receipt for
	// every verb. When cmd is given, only shipped tools contribute to the verdict;
	// receipts for other tools are set aside and counted.
	var shipped, outside int
	if cmd != "" {
		set, err := dogfood.ReadShipped(cmd)
		if err != nil {
			return DogfoodVerdict{}, refuse("run from a nova-tools checkout whose cmd/ holds the tools the release ships",
				"cannot read the shipped set: %s", err)
		}
		var aside []dogfood.Receipt
		verbs, got, aside = set.Scope(verbs, got)
		shipped, outside = len(set.Tools()), len(aside)
	}
	findings, summary := dogfood.Gate(verbs, got, nil, false)
	v := DogfoodVerdict{Verbs: summary.Verbs, Open: len(findings), Shipped: shipped, Outside: outside}
	for _, f := range findings {
		v.Findings = append(v.Findings, f.Line())
	}
	return v, nil
}

// dogfoodPaths resolves what the gate reads.
//
// An explicit --cli is retained. Otherwise, the reference is derived from
// the caller's checkout only if that file exists. If neither supplies a
// reference, the gate is skipped. Receipts come only from --receipts; there
// is no home-directory fallback (SPEC-UPDATE, no guessed paths).
func dogfoodPaths(o options, checkout string) (cli, receipts, cmd string) {
	cli = o.cli
	if derived := filepath.Join(checkout, "docs", "CLI.md"); cli == "" && checkout != "" && exists(derived) {
		cli = derived
	}
	if cli == "" {
		return "", "", ""
	}
	// The shipped set is the checkout's own cmd/, beside the reference and the
	// changelog the verb was already given.
	if c := filepath.Join(checkout, "cmd"); checkout != "" && exists(c) {
		cmd = c
	}
	return cli, o.receipts, cmd
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dogfoodCheck is the gate as `cut` and `build` run it. It answers the token
// the receipt line carries -- ok, waived or skipped -- and an error, which is
// either an ordinary refusal or errDogfood for the one whose line is already
// written.
func dogfoodCheck(token string, o options, deps Deps, checkout string, out, errs io.Writer) (string, error) {
	if o.noDogfood {
		// An explicit waiver must record why it was requested.
		if strings.TrimSpace(o.reason) == "" {
			return "", refuse("say why: "+DogfoodWaiveFlag+" "+DogfoodReasonFlag+" <why>",
				"%s waives the definition of done and no reason was given", DogfoodWaiveFlag)
		}
		// ON STDOUT, above the receipt, for the same reason RELEASE CUT
		// SENSITIVE is: a release that went round the gate is a fact somebody
		// reads off the terminal now and out of a log in six months.
		fmt.Fprintf(out, "RELEASE %s DOGFOOD WAIVED reason=%s\n", token, field(o.reason))
		return "waived", nil
	}
	cli, receipts, cmd := dogfoodPaths(o, checkout)
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
	v, err := read(cli, receipts, cmd)
	if err != nil {
		return "", err
	}
	if cmd != "" {
		// NAMED, NEVER SILENT: what the gate judged, and how much it set
		// aside as being about tools this release does not ship.
		fmt.Fprintf(errs, "RELEASE %s NOTE dogfood-gate shipped=%d outside=%d cmd=%s\n", token, v.Shipped, v.Outside, field(cmd))
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
