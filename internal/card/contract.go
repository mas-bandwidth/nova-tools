package card

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// The lane's contract, by reference (docs/SPEC-CARD-CONTRACT.md section 7): a brief is
// its header lines and one Contract: line; the frame every brief used to repeat (the
// child paragraph, the RULES, the six STEPs) lives once, versioned, in the contract file.

// ContractPath is the file the contract lives in, repository-relative.
const ContractPath = "docs/SPEC-CARD-CONTRACT.md"

// ContractVersion is the version of the contract a brief written now names.
const ContractVersion = "v1"

// ContractKey is the header key of a brief's contract reference.
const ContractKey = "Contract"

// ContractLine is the one line a brief names its contract by: the file and the version.
func ContractLine() string {
	return ContractKey + ": " + ContractPath + " " + ContractVersion
}

// ContractRef is the contract a brief names, its file and version, and whether its
// Contract: line is there and well formed (two fields).
func ContractRef(brief string) (file, version string, ok bool) {
	for _, line := range strings.Split(brief, "\n") {
		v, found := strings.CutPrefix(line, ContractKey+":")
		if !found {
			continue
		}
		f := strings.Fields(v)
		if len(f) != 2 {
			return "", "", false
		}
		return f[0], f[1], true
	}
	return "", "", false
}

// ContractText is the contract of version in the contract file doc: the text between
// its `<!-- contract <version> -->` and `<!-- end contract <version> -->` lines, which
// a version once published keeps (section 7). An error says the file holds no such block.
func ContractText(doc []byte, version string) (string, error) {
	text := string(doc)
	begin, end := "<!-- contract "+version+" -->\n", "\n<!-- end contract "+version+" -->"
	i := strings.Index(text, begin)
	if i < 0 {
		return "", fmt.Errorf("%s holds no contract %s", ContractPath, version)
	}
	body, _, found := strings.Cut(text[i+len(begin):], end)
	if !found {
		return "", fmt.Errorf("%s: contract %s has no end line", ContractPath, version)
	}
	return strings.TrimSpace(body), nil
}

// ReadContract is the contract of version in the checkout root, read from its
// ContractPath: what a lane reads once from the repository.
func ReadContract(root, version string) (string, error) {
	doc, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ContractPath)))
	if err != nil {
		return "", err
	}
	return ContractText(doc, version)
}

// WithContract is a brief by reference with the contract text in place of its
// Contract: line, after a blank line; the header lines stay first, so line 1 says what
// it said. A brief with no Contract: line is handed as it is.
func WithContract(brief, contract string) string {
	lines := strings.Split(brief, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, ContractKey+":") {
			lines[i] = "\n" + strings.TrimSpace(contract)
			return strings.Join(lines, "\n")
		}
	}
	return brief
}

// Brief is the brief of c under h by reference: the header lines Render writes, the
// task on its STOP line, and the Contract: line last; no paragraph, no RULES, no STEP.
// Its START is Render's, so Paths reads the same packages from either.
func Brief(h cardgen.Header, c cardgen.Card) string {
	rendered := strings.Split(cardgen.Render(h, c), "\n")
	var b strings.Builder
	for _, line := range rendered {
		key, value, _ := strings.Cut(line, ":")
		switch key {
		case "RESULT", "REPO", "BASE", "KIND", "DEPENDS-ON", "PATHS", "NEW", "TEST", "START", "Deadline":
			b.WriteString(line + "\n")
		case "STOP":
			task := strings.Join(strings.Fields(c.Task), " ")
			fmt.Fprintf(&b, "STOP: %s Done when%s\n", task, value)
		}
	}
	b.WriteString(ContractLine() + "\n")
	return b.String()
}

// Compact is a brief on file cut to what it carries by reference: every line of the
// frame the contract holds out (frameLine; AS A READ with its paragraph), any Contract:
// line it had out, runs of blank lines folded to one, everything else kept as written,
// and the Contract: line of version last. It measures a brief written before the
// contract (section 7); a task written as prose stays, so the cut never overstates.
func Compact(brief, version string) string {
	var b strings.Builder
	blank, inRead := true, false
	for _, line := range strings.Split(brief, "\n") {
		if inRead && strings.TrimSpace(line) != "" {
			continue
		}
		inRead = line == "AS A READ"
		if inRead || frameLine(line) || strings.HasPrefix(line, ContractKey+":") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			if !blank {
				b.WriteString("\n")
			}
			blank = true
			continue
		}
		b.WriteString(line + "\n")
		blank = false
	}
	b.WriteString(ContractKey + ": " + ContractPath + " " + version + "\n")
	return b.String()
}

// stepRE is a STEP line of a brief's frame.
var stepRE = regexp.MustCompile(`^STEP \d+\. `)

// frameLine says line is one the contract carries for every brief: the child
// paragraph, Libraries considered, RULES and each rule sentence, ATTRIBUTION, a STEP.
func frameLine(line string) bool {
	switch {
	case line == "RULES.", stepRE.MatchString(line),
		strings.HasPrefix(line, "You are a child of the coordinator"),
		strings.HasPrefix(line, "Libraries considered:"),
		strings.HasPrefix(line, "ATTRIBUTION:"):
		return true
	}
	for _, r := range swarm.DefaultChildRules {
		if line == r.Sentence {
			return true
		}
	}
	return false
}

// Tokens is a text's token count by the measure the cost record carries: four bytes to
// a token, rounded up (section 7).
func Tokens(text string) int {
	return (len(text) + 3) / 4
}

// lintContract is the brief a lint reads for brief: a brief by reference with the
// contract of the version it names in place of the line, and the finding when that text
// cannot be had (section 7). A brief with no reference is read as it is.
func lintContract(id, brief string, o Options) (string, []cardgen.LintFinding) {
	file, version, ok := ContractRef(brief)
	if !ok {
		if strings.Contains(brief, "\n"+ContractKey+":") || strings.HasPrefix(brief, ContractKey+":") {
			return brief, []cardgen.LintFinding{{ID: id, Check: "contract-unread", Line: headerLine(brief, ContractKey), Excerpt: "the Contract: line is not `" + ContractKey + ": <file> <version>`"}}
		}
		return brief, nil
	}
	if file != ContractPath || o.Contract == nil {
		return brief, []cardgen.LintFinding{{ID: id, Check: "contract-unread", Line: headerLine(brief, ContractKey), Excerpt: "no contract text to read for " + file + " " + version + "; the lint reads a brief by reference as the lane does, with " + ContractPath}}
	}
	text, err := o.Contract(version)
	if err != nil {
		return brief, []cardgen.LintFinding{{ID: id, Check: "contract-version", Line: headerLine(brief, ContractKey), Excerpt: err.Error()}}
	}
	return WithContract(brief, text), nil
}
