package main

import (
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// maxRemedy is the second half of every MORE line this binary prints; the
// skeleton's Out.Cap renders it. A cap with no remedy is censorship; a cap
// with one is an index.
const maxRemedy = tool.MaxRemedy

// addMax registers --max and its one-release alias --fail-max.
func addMax(f *tool.Flags) {
	f.Max()
	maxFlag := f.Lookup("max")
	f.Var(maxFlag.Value, "fail-max", "the old spelling of --max, accepted for one release; it sets the same value")
	f.Check(func(c *tool.Call) {
		if c.Given("fail-max") && !c.Bool("json") {
			fmt.Fprintln(c.Stderr, "NOTE --fail-max is --max")
		}
	})
}

// The hints below are each flag's `wants` string: what a required flag IS and
// what a first run should put there. The skeleton folds each into the refusal
// "`--<flag> is required; it wants <wants>; refusing to guess`", so the
// no-guessing law is unchanged -- a missing flag is still exit 2 -- but a
// refusal that names only what was wrong leaves a first-time caller to guess
// what the flag wanted, which is the same guessing the tool refuses to do,
// moved onto the reader.
const (
	dirHint      = `--dir <dir> is the tree to walk, your self repo's root or a directory inside it; it is never guessed from the working directory, so write it out every run`
	homeHint     = `--home <dir> is your memory-home directory: the tree the manifest's paths are relative to, and the only place attest reads`
	manifestHint = `--manifest <file> is a text file listing the paths a full boot must read, one per line, relative to --home (blank lines and # comments ignored); this tool ships none, because what a full boot reads is yours`
	fileHint     = `--file <file> is the one kernel file to measure — the file whose size you are holding to a budget, not the directory it lives in`
	coreHint     = `--core <file> is the door: the derived copy, usually SEED-CORE.md, whose floor set is checked against the source's`
	sourceHint   = `--source <file> is the source the door was derived from, usually SEED.md; the check is that the copy still agrees with it`
	ledgerHint   = `--ledger <file> is your ledger of protected material: a markdown file whose table rows are | fragment | home file | given | by |, written in advance and by you — this tool ships no corpus`
	rootHint     = `--root <dir> is the repo the ledger's home paths are relative to; it is never guessed from the working directory or from where the ledger happens to sit`
	anchorsHint  = `--min-anchors <n> is the fewest rows the ledger may hold, a positive number you state: the ledger lives inside the tree it protects, so its own shrinking has to be red`
	budgetHint   = `state the unit: --max-bytes <n> for a byte budget, or --max-tokens <n> --bytes-per-token <r> for the unit a context window actually spends (the divisor is one you measured on your own writing; there is no default)`
)
