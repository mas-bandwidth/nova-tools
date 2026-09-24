package pulse

// THE KINDS TABLE, AND THE FIRST-CARD RULE THAT READS IT (SPEC-TOOLWORK.md §5 rules 2-5,
// nova-tools #2215). §5 rule 3: "A kind's gate is declared in the tool, in one table, and
// printed. `internal/pulse/kinds.go` holds the table above as data." The table above is
// rule 2's — kind, what the card does, what PATHS: may hold, the gate's steps, the control
// that must be seen red, the kind's own reject tokens — and it lives here as data, one row
// per declared kind, in rule 2's order.
//
// THE NAME SET IS kinds.txt'S, NOT A SECOND ONE. internal/hygiene/kinds.txt is the name
// set three tools read (`nova-check hygiene`, `nova-merge batch`, the bench lint), and its
// own header says why the table reads its names from there rather than the reverse: the
// name set is the part a branch check needs, the table is the part a gate needs, and
// internal/pulse sits above internal/hygiene. KindRuleFor asks the declared-name check
// (kinds.KindDeclared, under the alias below), so the name authority stays one file, and
// issue2215_test.go holds this table's names to kinds.txt's and to the spec's §5 table
// besides — a table that drifted from either is a table a card was refused by for no
// reason a reader could find.
//
// THE CELLS ARE THE SPEC'S OWN WORDS. Backticks are stripped and whitespace folded,
// because both are the markdown around the words, not the words; the emphasis markers the
// spec's table carries (`**range equality**`, `**only**`) are kept, because the class test
// compares the spec's text against the table's and not two normalizations against each
// other — and because `testdata/firstrun/**` in a PATHS cell is a glob, not emphasis. A
// cell that is only an em dash (guard and the read family carry no tokens) is "-".
//
// WHAT IS NOT HERE YET. The `accept --kinds` print and the unknown-kind refusal at `cut`
// (rule 3's other half) land with the accept verb, which is the print's only caller; the
// rebase, sweep and mutation-kill controls (rule 2's control column) are the accept
// gate's to run; rule 4's eligibility is `cut`'s. This card adds the table and rule 5's
// launch refusal, the two halves the issue's title names, and the table's first production
// reader: launch, below.
//
// THE FIRST-CARD RULE (§5 rule 5): "One card of a new template runs alone before the batch
// widens." `launch` refuses a batch wider than one while any (kind, template sha12) pair
// in it has no `ACCEPT OK` on file in <root>/accept/first.tsv, printing the spec's own
// refusal line. The pair is read off the card's typed header: `KIND:` is rule 1's typed
// line; `TEMPLATE:` names the sha-12 of the template file the card was rendered from, the
// line a future cutter writes inside the header block (an unknown key the grammar reads
// past, SPEC-CARD clause 2) and inside the contract hash. A card that carries no `KIND:`
// line is a pre-§5 card, and the rule does not reach it: every card cut before §5 launches
// exactly as before, which is also what keeps this check dormant for the queue's whole
// existing stock.
//
// The rule bites the batch, not the pair: a wide batch is refused while ANY pair in it is
// unproven, because "runs alone" is about the card, not about the company of its own kind
// — two different new templates are two first cards, and neither has run alone yet. A
// single card is never refused on this rule; it IS the first card.
//
// first.tsv is the accept verb's file to write (§1, still to land); this card is the
// reader. Its rows are `kind<TAB>template<TAB>verdict`, and only the verdict `ACCEPT OK`
// proves a pair — a first card that came back REJECT or ABSTAIN leaves the pair unproven,
// which is rule 5's own sentence.
//
// A card whose KIND: names a kind the table does not hold is refused outright, whatever
// the batch width. Rule 3: "there is no default kind" — a card of an unknown kind was not
// cut by this toolchain (`cut` refuses the kind), can never be accepted (`accept`
// abstains on it), and would spend a slot and a deadline to learn that. Refusing at launch
// is the same refusal `cut` has, one step later.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	// The alias: package pulse already holds a `hygiene` type (hygiene.go), so the name
	// set's package comes in under the name of the thing it holds.
	kinds "github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// KindRule is one row of SPEC-TOOLWORK.md §5 rule 2's table, as data: what a card of the
// kind does, what its PATHS: may hold, the steps of its gate, the control that must be
// seen red before the gate's green counts, and the kind's own reject tokens ("-" when it
// carries none). The accept verb (§1) runs the gate a row declares; launch reads the
// table only to know a kind when it sees one.
type KindRule struct {
	Name    string
	Does    string
	Paths   string
	Gate    string
	Control string
	Tokens  string
}

// kindRules is the table, one row per declared kind in §5 rule 2's order: fix-red,
// transcript-test, rebase, sweep, mutation-kill, guard, then the read family. The read
// family is one row in the spec's table; here it is five, because a kind is a name a card
// declares, and each of the five is a name. issue2215_test.go holds these rows to the
// spec's table and to the declared name set (kinds.Kinds()).
var kindRules = []KindRule{
	{Name: "fix-red", Does: "fixes one defect, red test first (WORKER-CARDS 4 and 23; SPEC-SWARM P7)", Paths: "the named source and test files", Gate: "hygiene, shape, positive, mutate", Control: "the change reverted: nova-review mutate range form PASS, and TEST: is among the tests that went red", Tokens: "no-test, vacuous-test, named-test-not-red"},
	{Name: "transcript-test", Does: "makes one docs/TESTS.md section executed line for line (§7)", Paths: "cmd/<tool>/firstrun_test.go, cmd/<tool>/testdata/firstrun/** — **never docs/TESTS.md**, and never the rest of testdata/ (for nova-pulse that holds the gate's own fixtures, testdata/accept/)", Gate: "hygiene, shape, positive", Control: "three seeds applied to a **copy** of the tool's section, each one edit: one line dropped, one value altered, one line moved; the new test goes red on each", Tokens: "doc-edited, transcript-not-read (a seed stayed green: the test does not read the document)"},
	{Name: "rebase", Does: "replays one of our own open PRs onto the base, changing nothing", Paths: "the PR's own changed files, computed by cut from the PR at its pinned head", Gate: "hygiene, positive, and **range equality**: git range-diff pairs every commit, and each pair's patch-id is equal except in files git reported conflicted during the replay; those files are exempt from equality, so they are **listed by name on the read card** and are what the reader reads", Control: "one line changed in a file that did **not** conflict: the gate rejects rebase-drift", Tokens: "rebase-drift, commit-dropped, commit-added"},
	{Name: "sweep", Does: "applies one mechanical class fix at every site the class test names", Paths: "up to 8 globs, plus FILES: <n>, the most files the diff may touch", Gate: "hygiene, positive, and the seed form **only**: the class test landed first and the sweep changes no test file, so the shape check would say no-test and range mutate would exit 2 no-tests-changed — neither is declared for this kind", Control: "**two** seeds, each one edit: the first and the last changed site (path order) reverted alone; the class test in TEST: goes red **naming that site** — a class test that samples is found out", Tokens: "site-not-seen, over-files"},
	{Name: "mutation-kill", Does: "writes the test that kills one surviving mutant", Paths: "test files only", Gate: "hygiene, shape, positive, and the diff is test-only", Control: "the card's own SEED: patch (the mutant, written by the card writer into the card, edits=1 asserted at cut) applied with mutate --seed: the new test is red with it and green without", Tokens: "mutant-survives, non-test-change"},
	{Name: "guard", Does: "reverts one commit's non-test files and records whether the named tests go red (#2042)", Paths: "the commit's own files", Gate: "none: the control IS the card", Control: "nova-review guard: GUARDED when tests go red, UNGUARDED when they stay green, COMPILER-HELD when the revert does not compile, NOT-APPLICABLE when the file is excluded on this OS", Tokens: "-"},
	{Name: "read", Does: "read and report", Paths: "none: PATHS: none", Gate: "none", Control: "none; the HARVEST row says gate=none", Tokens: "-"},
	{Name: "probe", Does: "read and report", Paths: "none: PATHS: none", Gate: "none", Control: "none; the HARVEST row says gate=none", Tokens: "-"},
	{Name: "text", Does: "read and report", Paths: "none: PATHS: none", Gate: "none", Control: "none; the HARVEST row says gate=none", Tokens: "-"},
	{Name: "tone", Does: "read and report", Paths: "none: PATHS: none", Gate: "none", Control: "none; the HARVEST row says gate=none", Tokens: "-"},
	{Name: "report", Does: "read and report", Paths: "none: PATHS: none", Gate: "none", Control: "none; the HARVEST row says gate=none", Tokens: "-"},
}

// KindRuleFor is the table lookup: the row a kind names, or false when the table does not
// hold it. The name set is internal/hygiene/kinds.txt's — one authority, not two — so a
// name the table spells but kinds.txt does not declare answers false here, and the class
// test holds the two lists to the same names so that belt never has to tighten.
func KindRuleFor(name string) (KindRule, bool) {
	if !kinds.KindDeclared(name) {
		return KindRule{}, false
	}
	for _, r := range kindRules {
		if r.Name == name {
			return r, true
		}
	}
	return KindRule{}, false
}

// acceptFirstTSV is §5 rule 5's first-card file, under the pulse root: <root>/accept/
// first.tsv, one row per verdict a first card earned. The accept verb (§1) is its only
// writer; launch only reads it, so a root without one is not an error — it is the
// every-pair-unproven state the rule asks about.
const acceptFirstTSV = "accept/first.tsv"

// acceptOK is the one verdict that proves a (kind, template) pair: `ACCEPT OK`, spelled
// as the accept verb's own line spells it. REJECT and ABSTAIN rows are read and prove
// nothing (rule 5: "A first card that is REJECT or ABSTAIN leaves the pair unproven").
const acceptOK = "ACCEPT OK"

// refuseWideUnprovenFirstBatch is launch's §5 rule 5 refusal, and the kinds table's first
// production reader. It walks the cards the caller handed in: a card it cannot read is
// refused (a check that could not see what it was asked to read must not report clean,
// SPEC-SWARM.md's unread-diff rule); a typed card whose kind the table does not hold is
// refused by name; and a batch wider than one is refused while any (kind, template) pair
// in it has no ACCEPT OK on file. It returns 0 when the batch may run and 2 when it may
// not, and writes nothing on the paths that pass — a launch of legacy cards, or of pairs
// already proven, prints exactly what it printed before this rule.
func refuseWideUnprovenFirstBatch(stderr io.Writer, root string, cards []CardRow) int {
	var proved map[string]bool
	if len(cards) > 1 {
		pairs, err := acceptedFirstPairs(root)
		if err != nil {
			return refusal(stderr, "PULSE", err)
		}
		proved = pairs
	}
	badKind, badTmpl := "", ""
	unproven := false
	for _, c := range cards {
		kind, tmpl, typed, err := cardKindTemplate(c.Card)
		if err != nil {
			return refusal(stderr, "PULSE", err)
		}
		if !typed {
			continue // a card with no typed header is a pre-§5 card; rule 5 reaches kinds, not legacy shapes
		}
		if _, ok := KindRuleFor(kind); !ok {
			fmt.Fprintf(stderr, "PULSE REFUSED: kind=%s is not a kind the kinds table holds (there is no default kind: SPEC-TOOLWORK.md §5 rule 3)\n", oneline.Field(kind))
			return 2
		}
		if len(cards) > 1 && !proved[kind+"\t"+tmpl] && !unproven {
			unproven = true
			badKind, badTmpl = kind, tmpl
		}
	}
	if unproven {
		fmt.Fprintf(stderr, "PULSE REFUSED: no accepted first card for kind=%s template=%s (launch one card first)\n",
			oneline.Field(badKind), oneline.Field(badTmpl))
		return 2
	}
	return 0
}

// acceptedFirstPairs reads <root>/accept/first.tsv into the set of (kind, template) pairs
// an ACCEPT OK row proves, keyed "kind<TAB>template". A missing file is the empty set —
// nothing has run alone yet — and a row that is not three fields proves nothing, because
// a row the reader cannot read as a verdict is a row no verdict was written on.
func acceptedFirstPairs(root string) (map[string]bool, error) {
	path := filepath.Join(root, filepath.FromSlash(acceptFirstTSV))
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("%s: %w", oneline.Field(path), err)
	}
	pairs := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[0] == "" {
			continue
		}
		if strings.TrimSpace(f[2]) == acceptOK {
			pairs[f[0]+"\t"+f[1]] = true
		}
	}
	return pairs, nil
}

// cardKindTemplate reads a card's typed header for the two lines rule 5 keys on: KIND:
// (the kind) and TEMPLATE: (the template's sha-12, which the cutter writes and the
// contract hash covers). The grammar is the typed header's own — the contiguous run of
// `KEY: value` lines from line 2, blank lines skipped, ended by the first non-empty line
// that is not one (internal/swarm/lintheader.go cardHeaderBlock, SPEC-CARD clause 2) —
// restated here because that parser is the bench lint's and unexported; the day
// internal/pulse's own cardheader.go (#1721) lands, this should call it instead of
// holding a second copy. The key names are upper case exactly as the spec writes them, so
// a lowercase `kind:` continues the block and declares nothing. The FIRST of two KIND:
// lines is the one read; the lint names the duplicate on the bench, and launch refusing a
// card the lint already refused twice buys nothing.
func cardKindTemplate(path string) (kind, tmpl string, typed bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", false, fmt.Errorf("card %s: %w", oneline.Field(path), err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		if i == 0 {
			continue // line 1 is the contract line
		}
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := typedKeyRE.FindStringSubmatch(line)
		if m == nil {
			break // the prose begins here, exactly as the gate's parser has it
		}
		if kind == "" && m[1] == "KIND" {
			kind = strings.TrimSpace(m[2])
		}
		if tmpl == "" && m[1] == "TEMPLATE" {
			tmpl = strings.TrimSpace(m[2])
		}
	}
	return kind, tmpl, kind != "", nil
}

// typedKeyRE is what makes a line a `KEY: value` line of the typed header: one word, a
// letter first, then letters, digits and hyphens, then a colon, at column 0, ANY case —
// the same shape internal/swarm/lintheader.go's headerKeyRE holds, widened there for the
// lowercase `base-repo:` and `base-sha:` every darwin launcher reads.
var typedKeyRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*):\s*(.*)$`)
