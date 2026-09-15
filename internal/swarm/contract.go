package swarm

import (
	"fmt"
	"os"
	"strings"
)

// ONE RESULT CONTRACT, READ BY MACHINERY (slice 3 of PR #241).
//
// A job's RESULT.md is accepted or refused by comparing its first line to the card's
// contract line, byte for byte. Line 2 is the disposition and is returned verbatim; the
// rest is bounded to a small number of lines, so a coordinator's window never holds an
// unbounded transcript. A receipt written beside the report retains the label, the card's
// hash, the disposition, the line count, and — when the records exist — the run's wall time
// and exit code and the usage totals.

// DefaultContractLines is the bound on evidence lines when none is named.
const DefaultContractLines = 20

// Contract is one result-contract check of a job's RESULT.md.
type Contract struct {
	Label        string
	ContractLine string // line 1 of RESULT.md must equal this exactly
	MaxLines     int    // bound on evidence lines; 0 or negative means DefaultContractLines
	Card         []byte // the card text, hashed into the receipt
	HaveRun      bool   // a run record exists
	WallSeconds  int    // from the run record
	ExitCode     int    // from the run record
	HaveUsage    bool   // a usage file was given
	TokensIn     int
	TokensOut    int
	USD          string
}

// Outcome is what CheckResult decided: the one grammar line, the disposition, the bounded
// evidence and the report's whole line count.
type Outcome struct {
	OK        bool
	Line      string   // RESULT OK <label> line2=<...>  or  RESULT REFUSED <label> <reason>
	Line2     string   // the disposition line, verbatim
	Evidence  []string // the lines past line 2, bounded to MaxLines
	LineCount int
}

// CheckResult reads one RESULT.md and checks it against the contract. It returns an error
// only when the report cannot be read; a read report always yields an Outcome carrying the
// grammar line, accepted or refused.
func CheckResult(resultPath string, c Contract) (Outcome, error) {
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		return Outcome{}, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	out := Outcome{LineCount: len(lines)}
	if len(lines) == 0 {
		// An empty report has no line 1 to compare: refused, naming what was found.
		out.Line = fmt.Sprintf("RESULT REFUSED %s line 1 is empty", c.Label)
		return out, nil
	}
	out.Line2 = ""
	if len(lines) > 1 {
		out.Line2 = lines[1]
	}
	line1 := strings.TrimRight(lines[0], " \t")
	if line1 != c.ContractLine {
		found := line1
		if len(found) > 60 {
			found = found[:60]
		}
		out.Line = fmt.Sprintf("RESULT REFUSED %s line 1 is %q", c.Label, found)
		return out, nil
	}
	bound := c.MaxLines
	if bound <= 0 {
		bound = DefaultContractLines
	}
	evidence := lines[2:]
	if len(evidence) > bound {
		evidence = evidence[:bound]
	}
	out.Evidence = evidence
	out.OK = true
	out.Line = fmt.Sprintf("RESULT OK %s line2=%s", c.Label, out.Line2)
	return out, nil
}

// WriteReceipt writes the receipt beside the report: label, card sha-256, the disposition,
// the line count, and the run and usage records when they exist.
func WriteReceipt(path string, o Outcome, c Contract) error {
	var b strings.Builder
	fmt.Fprintf(&b, "label=%s\n", c.Label)
	fmt.Fprintf(&b, "card_sha256=%s\n", HashBytes(c.Card))
	fmt.Fprintf(&b, "line2=%s\n", o.Line2)
	fmt.Fprintf(&b, "lines=%d\n", o.LineCount)
	if c.HaveRun {
		if c.WallSeconds >= 0 {
			fmt.Fprintf(&b, "wall_seconds=%d\n", c.WallSeconds)
		}
		fmt.Fprintf(&b, "exit_code=%d\n", c.ExitCode)
	}
	if c.HaveUsage {
		fmt.Fprintf(&b, "tokens_in=%d\n", c.TokensIn)
		fmt.Fprintf(&b, "tokens_out=%d\n", c.TokensOut)
		fmt.Fprintf(&b, "usd=%s\n", c.USD)
	}
	return writeAtomic(path, []byte(b.String()), 0o644)
}
