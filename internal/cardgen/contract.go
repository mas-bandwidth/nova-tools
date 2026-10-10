package cardgen

import (
	"embed"
	"fmt"
	"strings"
)

// THE LANE'S CONTRACT, BY REFERENCE (docs/SPEC-CARD-CONTRACT.md section 7). A brief is
// its header lines and one line, `Contract: docs/SPEC-CARD-CONTRACT.md <version>`; the
// frame every brief used to repeat (the child paragraph, the RULES, the ATTRIBUTION line,
// the six STEPs, AS A READ) lives once, versioned, in the contract file, and the lane
// reads it from the repository once. This build holds a copy of each published version
// (contract/<version>.txt), equal to the file's block (TestTheHeldContractIsTheDocsBlock),
// for the readers that cannot read the repository first: the lint, and the daemon that
// puts the text in a brief whose checkout does not hold it.

// ContractPath is the file the contract lives in, repository-relative.
const ContractPath = "docs/SPEC-CARD-CONTRACT.md"

// ContractVersion is the version a brief written now names.
const ContractVersion = "v2"

// ContractKey is the header key of a brief's contract reference.
const ContractKey = "Contract"

//go:embed contract/*.txt
var heldContracts embed.FS

// ContractLine is the one line a brief names its contract by: the file and the version.
func ContractLine() string { return ContractKey + ": " + ContractPath + " " + ContractVersion }

// HeldContract is this build's copy of the contract of version, and whether it holds one.
func HeldContract(version string) (string, bool) {
	if version == "" || strings.ContainsAny(version, "/\\.") {
		return "", false
	}
	raw, err := heldContracts.ReadFile("contract/" + version + ".txt")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(raw)), true
}

// ContractText is the contract of version in the contract file doc: the text between its
// `<!-- contract <version> -->` and `<!-- end contract <version> -->` lines. A version once
// published keeps its text; a change is a new version beside it (section 7).
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

// ContractRef is the version a brief's reference names and the reference's 1-based line;
// at is 0 for a brief with no reference. A reference is a Contract: line whose first word
// is ContractPath; a Contract: line naming anything else is the brief's own prose. version
// is "" for a reference that is not the file and one version.
func ContractRef(brief string) (version string, at int) {
	for i, line := range strings.Split(brief, "\n") {
		v, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), ContractKey+":")
		f := strings.Fields(v)
		if !ok || len(f) == 0 || f[0] != ContractPath {
			continue
		}
		if len(f) != 2 {
			return "", i + 1
		}
		return f[1], i + 1
	}
	return "", 0
}

// WithContract is a brief by reference with text in place of its reference line, after a
// blank line: the header lines stay first, so line 1 says what it said. A brief with no
// reference is returned as it is.
func WithContract(brief, text string) string {
	_, at := ContractRef(brief)
	lines := strings.Split(brief, "\n")
	if at == 0 || at > len(lines) {
		return brief
	}
	lines[at-1] = "\n" + strings.TrimSpace(text)
	return strings.Join(lines, "\n")
}

// AsRead is the brief a reader that cannot read the repository is handed: a brief by
// reference with the held contract of its version in place of the line, and why not
// ("" when it reads): the reference names no version, or one this build does not hold.
// A brief with no reference is read as it is.
func AsRead(brief string) (read, why string) {
	version, at := ContractRef(brief)
	switch {
	case at == 0:
		return brief, ""
	case version == "":
		return brief, "the Contract: line is not `" + ContractKey + ": " + ContractPath + " <version>`; write " + ContractLine()
	}
	text, ok := HeldContract(version)
	if !ok {
		return brief, fmt.Sprintf("the brief names contract %s, which this build does not hold; write %s", version, ContractLine())
	}
	return WithContract(brief, text), ""
}
