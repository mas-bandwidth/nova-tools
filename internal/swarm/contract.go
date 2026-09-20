package swarm

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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
	Label                    string
	ContractLine             string // line 1 of RESULT.md must equal this exactly
	MaxLines                 int    // bound on evidence lines; 0 or negative means DefaultContractLines
	Card                     []byte // the card text, hashed into the receipt
	HaveRun                  bool   // a run record exists
	WallSeconds              int    // from the run record
	ExitCode                 int    // from the run record
	HaveUsage                bool   // a usage file was given
	TokensIn                 int
	TokensOut                int
	USD                      string
	RequestedMaxOutputTokens int // Row 5: requested limit bound; 0 = unset/omitted
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
		out.Line = fmt.Sprintf("RESULT REFUSED %s line 1 is empty", oneline.Quote(c.Label))
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
		out.Line = fmt.Sprintf("RESULT REFUSED %s line 1 is %q", oneline.Quote(c.Label), found)
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
	out.Line = fmt.Sprintf("RESULT OK %s line2=%s", oneline.Quote(c.Label), oneline.Quote(out.Line2))
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
	if c.RequestedMaxOutputTokens > 0 {
		fmt.Fprintf(&b, "requested_max_output_tokens=%d\n", c.RequestedMaxOutputTokens)
	}
	return writeAtomic(path, []byte(b.String()), 0o644)
}

// Receipt is the parsed contents of a <result>.receipt file.
type Receipt struct {
	Label                    string
	CardSHA256               string
	Line2                    string
	Lines                    int
	HaveRun                  bool
	WallSeconds              int
	ExitCode                 int
	HaveUsage                bool
	TokensIn                 int
	TokensOut                int
	USD                      string
	RequestedMaxOutputTokens int
}

// ReadReceipt reads and parses a <result>.receipt file.
func ReadReceipt(path string) (Receipt, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, err
	}
	r := Receipt{WallSeconds: -1, ExitCode: -1}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "label":
			r.Label = v
		case "card_sha256":
			r.CardSHA256 = v
		case "line2":
			r.Line2 = v
		case "lines":
			r.Lines, _ = strconv.Atoi(v)
		case "wall_seconds":
			r.HaveRun = true
			r.WallSeconds, _ = strconv.Atoi(v)
		case "exit_code":
			r.HaveRun = true
			r.ExitCode, _ = strconv.Atoi(v)
		case "tokens_in":
			r.HaveUsage = true
			r.TokensIn, _ = strconv.Atoi(v)
		case "tokens_out":
			r.HaveUsage = true
			r.TokensOut, _ = strconv.Atoi(v)
		case "usd":
			r.HaveUsage = true
			r.USD = v
		case "requested_max_output_tokens":
			r.RequestedMaxOutputTokens, _ = strconv.Atoi(v)
		}
	}
	return r, nil
}
