package definition

import "strings"

// Render writes a definition as a card file: the contract line, the header in
// profile order, one blank line, then the brief exactly. Parse(Render(d)) reads
// back the same definition for any d that Validate accepts and whose brief does
// not begin with a line shaped like KEY: value (which the reader takes for a
// header line). Render decides nothing; Parse and Validate do.
func Render(d Definition) []byte {
	var b strings.Builder
	b.WriteString("RESULT: " + d.ID)
	if d.ContractSHA != "" {
		b.WriteString(" sha=" + d.ContractSHA)
	}
	if d.ContractNote != "" {
		b.WriteString(" " + d.ContractNote)
	}
	b.WriteString("\n")
	line := func(k, v string) { b.WriteString(k + ": " + v + "\n") }
	line(KeySchema, d.Schema)
	line(KeyID, d.ID)
	if d.Entry != "" {
		line(KeyEntry, d.Entry)
	}
	line(KeyTitle, d.Title)
	line(KeyKind, d.Kind)
	if len(d.Paths) == 0 {
		line(KeyPaths, "none")
	} else {
		line(KeyPaths, strings.Join(d.Paths, ", "))
	}
	if len(d.DependsOn) == 0 {
		line(KeyDependsOn, "-")
	} else {
		line(KeyDependsOn, strings.Join(d.DependsOn, ", "))
	}
	line(KeyTier, d.Tier)
	line(KeyTest, d.Test.String())
	line(KeyDoneWhen, d.DoneWhen)
	line(KeyDoors, d.Doors)
	line(KeyProbes, d.Probes)
	b.WriteString("\n")
	b.WriteString(d.Brief)
	return []byte(b.String())
}
