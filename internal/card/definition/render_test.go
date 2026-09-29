package definition

import (
	"context"
	"strings"
)

// render writes a definition as a card file: the contract line, the header in
// profile order, one blank line, then the brief exactly. Parse(Render(d)) reads
// back the same definition for any d that Validate accepts and whose brief does
// not begin with a line shaped like KEY: value (which the reader takes for a
// header line). Render decides nothing; Parse and Validate do.
func render(d Definition) []byte {
	var b strings.Builder
	b.WriteString("RESULT: " + d.ID)
	if d.BaseCommit != "" {
		b.WriteString(" sha=" + d.BaseCommit)
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

// refusal lists as the tests read them: the slice of a *Refusals.
func list(r *Refusals) []Refusal {
	if r == nil {
		return nil
	}
	return r.List
}

func parseL(f []Source) ([]Definition, []Refusal) { d, r := parse(f); return d, list(r) }

func validateL(d []Definition) (Report, []Refusal) { rep, r := validate(d); return rep, list(r) }

func Lines(rs []Refusal) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.String()
	}
	return out
}

func pinL(ctx context.Context, dir, commit string, paths []string, opts ...PinOption) ([]pinned, []Refusal) {
	p, r := pinDir(ctx, dir, commit, paths, opts...)
	return p, list(r)
}

func pinG(ctx context.Context, g *gitRun, dir, commit string, paths []string, opts ...PinOption) ([]pinned, []Refusal) {
	p, r := pin(ctx, g, dir, commit, paths, opts...)
	return p, list(r)
}
