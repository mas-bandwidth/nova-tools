// nova-check runs checks over markdown records and repositories (SPEC-CHECK.md):
// quickstart (links, then nocode), boot attestation, link integrity, the
// kernel size budget, the self/machinery separation (nocode, and nocode
// --staged over the git index), the floor-set parity of a derived door and its
// source, the protected corpus, branch hygiene, the dogfood ledger (record,
// ledger, gate), convergence and spelling. Exit 0 pass, 1 check failed, 2 could
// not run.
//
// Every path and every budget comes from a flag. There are no defaults: a
// missing flag is a refusal, never a guess. Three verbs write, each only when
// asked and each with --dry-run: dogfood record appends a receipt, spelling
// --write edits files, convergence --state stores its streak.
package main

import (
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// seams are the process-wide resources a run reads: the git program the
// --staged advisory runs through, and the clock, git runner and help runner of
// the dogfood verbs. The zero value is the production default; a test fills the
// field it needs and runs beside its neighbours instead of assigning a package
// variable, which would race every parallel test reading it.
type seams struct {
	staged  stagedSeams
	dogfood dogfoodSeams
}

func main() { os.Exit(novaCheck(seams{}).Main()) }

// exitCodes is the one exit table of every verb (docs/STANDARD.md section 2).
const exitCodes = "0 pass, 1 check failed, 2 could not run (bad invocation)"

// novaCheck is the tool: its verbs and their flags, run by internal/tool, which
// holds the dispatch, the banner, -h, --json, --max with its MORE line, the
// refusal grammar and the exit table.
func novaCheck(s seams) *tool.Tool {
	return &tool.Tool{
		Name:      "nova-check",
		What:      "checks over markdown records and repositories, each finding named by file and line",
		Stamp:     version,
		ExitTable: exitCodes + "\n\nsetup:\n  mkdir -p ./self/docs\n  printf '# Kernel\\n' > ./self/docs/SEED-CORE.md",
		How: "most verbs inspect named paths and keep no state; three write, each only when asked:\n" +
			"dogfood record appends a receipt, spelling --write edits files in place, convergence --state\n" +
			"stores its streak, and --dry-run writes none of it. convergence reads forge data through gh\n" +
			"and an optional checkout through git; the other checks read the manifests and ledgers you name.\n" +
			"first run: create the small markdown tree below, then run the example commands.",
		NoJSON: "the same result as one JSON object on stdout; spelling and convergence take it too, through their own " +
			"printers (convergence prints its reading object), and quickstart and dogfood print typed lines",
		UsageNote: "  nova-check <verb> -h, nova-check help <verb>   the verb's flags, its effect and exit codes\n" +
			"dogfood gate exits 1 with the verbs no non-author has run and the edges nobody has cleared.\n" +
			"An edge is what the run found; a receipt records it: --not-ok, or an Edge: or Edges: in the notes.\n" +
			"The remedy is one nova-check dogfood record --ok per verb named, and per edge --closes <id> or\n" +
			"the finder running it again.\n" +
			"convergence exits 1 after two consecutive widening ticks. A widening tick is a tick whose <stream>\n" +
			"moved the wrong way against its before: --state's last for LEDGER and FLEET, --since's for the rest.\n" +
			"The exit-1 line prints trend=widening on the CONVERGENCE line, and the next run is\n" +
			"nova-check convergence --state <file> again once the source moves, or nova-check dogfood record the\n" +
			"finding the stream names.",
		Verbs: []tool.Verb{
			quickstartVerb(),
			attestVerb(),
			linksVerb(),
			kernelVerb(),
			nocodeVerb(s),
			floorsVerb(),
			corpusVerb(),
			hygieneVerb(),
			dogfoodVerb("ledger", s),
			dogfoodVerb("record", s),
			dogfoodVerb("gate", s),
			convergenceVerb(),
			spellingVerb(),
		},
	}
}

func linksFlags(f *tool.Flags) {
	f.Required("dir", dirHint)
	f.Var(&repeatable{}, "file", "one markdown file to scan, narrowing the walk to just these (repeatable; --dir is still the resolution root)")
	f.Var(&repeatable{}, "exclude", "path prefix not scanned, and links into it not checked (repeatable; empty by default)")
	addAllowEmpty(f)
	addMax(f)
}

// The hints below turn this binary's most-hit refusals into a next step. The
// no-guessing law is unchanged — a missing flag is still exit 2 and still says
// "refusing to guess" — but a refusal that names only what was wrong leaves a
// first-time caller to guess what the flag wanted, which is the same guessing
// the tool refuses to do, moved onto the reader. Each hint says what the flag
// IS and what a first run should put there.
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

// repeatable collects a flag given more than once. Every scope narrowing is
// the caller's, stated per run, and starts empty.
type repeatable []string

func (r *repeatable) String() string     { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error { *r = append(*r, v); return nil }

// Get returns the collected values, so the flag is a flag.Getter and the
// skeleton's Call.Get reads it.
func (r *repeatable) Get() any { return []string(*r) }

// effectiveDenyList resolves the floor list, a replacement, or an extension,
// and reports which of the three produced it.
func effectiveDenyList(replace, add string) ([]string, string, error) {
	if replace != "" {
		exts, err := check.ParseDenyList(replace)
		if err != nil {
			return nil, "", err
		}
		return exts, check.DenyReplaced, nil
	}
	floor, err := check.FloorDenyExts()
	if err != nil {
		return nil, "", err
	}
	if add == "" {
		return floor, check.DenyFloor, nil
	}
	extra, err := check.ParseDenyList(add)
	if err != nil {
		return nil, "", err
	}
	seen := make(map[string]bool, len(floor)+len(extra))
	for _, e := range floor {
		seen[e] = true
	}
	for _, e := range extra {
		seen[e] = true
	}
	return slices.Sorted(maps.Keys(seen)), check.DenyExtended, nil
}

// sortedNames returns the name-floor keys in a stable order, so that
// --print-deny-list output can be diffed between runs and between versions. It
// is never nil: the JSON rendering prints an empty floor as [], not null.
func sortedNames(m map[string]bool) []string {
	out := slices.AppendSeq(make([]string, 0, len(m)), maps.Keys(m))
	slices.Sort(out)
	return out
}
