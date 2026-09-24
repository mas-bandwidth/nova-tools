package typedrec

import (
	"fmt"
	"strings"
)

// Requirement codes in the contract.
const (
	ReqRequired = "R" // Required always
	ReqDone     = "D" // Required when status is DONE
	ReqPass     = "P" // Required when DONE and CHECK=pass, optional otherwise
	ReqOptional = "O" // Optional, type-checked when present
	ReqUnknown  = "-" // Unknown for this kind, refused
)

// Card kinds supported by v2 typedrec.
const (
	KindFix       = "fix"
	KindRecut     = "recut"
	KindPort      = "port"
	KindDocsGuard = "docs-guard"
	KindReport    = "report"
	KindRead      = "read"
)

// Kinds is the list of all 6 valid card kinds in canonical order.
var Kinds = []string{KindFix, KindRecut, KindPort, KindDocsGuard, KindReport, KindRead}

// IsKind reports whether k is one of the declared Kinds. Every reader of a
// card or RESULT KIND checks it here before using it as a key.
func IsKind(k string) bool { return isValidKind(k) }

// FieldEntry specifies one row of the contract table.
type FieldEntry struct {
	Field     string
	Type      string
	Fix       string
	Recut     string
	Port      string
	DocsGuard string
	Report    string
	Read      string
}

// ContractDef represents the single source of truth for typedrec RESULT v2.
type ContractDef struct {
	Entries []FieldEntry
}

// Contract is the canonical Go table defining RESULT v2 and DISPOSITION v1.
var Contract = ContractDef{
	Entries: []FieldEntry{
		{Field: "line 1", Type: "card line 1 verbatim (else contradictory)", Fix: "R", Recut: "R", Port: "R", DocsGuard: "R", Report: "R", Read: "R"},
		{Field: "line 2", Type: "`DONE` | `ABSTAIN <why>` | `BLOCKED <why>`; why is 1-512 B", Fix: "R", Recut: "R", Port: "R", DocsGuard: "R", Report: "R", Read: "R"},
		{Field: "SCHEMA", Type: "literal `v2`", Fix: "R", Recut: "R", Port: "R", DocsGuard: "R", Report: "R", Read: "R"},
		{Field: "KIND", Type: "enum of the 6; must equal the card's KIND", Fix: "R", Recut: "R", Port: "R", DocsGuard: "R", Report: "R", Read: "R"},
		{Field: "ATTEMPT", Type: "int 1-99; must equal the card's attempt", Fix: "R", Recut: "R", Port: "R", DocsGuard: "R", Report: "R", Read: "R"},
		{Field: "CHECK", Type: "`pass` | `fail` | `not-run`", Fix: "R", Recut: "R", Port: "R", DocsGuard: "R", Report: "R", Read: "R"},
		{Field: "REPO", Type: "`owner/name`, `^[a-z0-9-]+/[a-z0-9._-]+$`; must equal the card's repo", Fix: "R", Recut: "R", Port: "R", DocsGuard: "R", Report: "R", Read: "R"},
		{Field: "BRANCH", Type: "git ref (check-ref-format), ≤200 B; must equal the card's branch when it has one", Fix: "D", Recut: "D", Port: "D", DocsGuard: "D", Report: "O", Read: "-"},
		{Field: "PATHS", Type: "1-256 space-separated repo-relative paths; no `..`, no leading `/`, no duplicates", Fix: "D", Recut: "D", Port: "D", DocsGuard: "D", Report: "O", Read: "-"},
		{Field: "RED", Type: "text 1-4096 B", Fix: "D", Recut: "D", Port: "D", DocsGuard: "-", Report: "-", Read: "-"},
		{Field: "GREEN", Type: "text 1-4096 B", Fix: "P", Recut: "P", Port: "P", DocsGuard: "-", Report: "-", Read: "-"},
		{Field: "PRIOR", Type: "`#<int> @<hex12>`", Fix: "-", Recut: "D", Port: "-", DocsGuard: "-", Report: "-", Read: "-"},
		{Field: "PR", Type: "int", Fix: "-", Recut: "-", Port: "-", DocsGuard: "-", Report: "-", Read: "D"},
		{Field: "HEAD", Type: "hex40; must equal the card's `pr_head`", Fix: "-", Recut: "-", Port: "-", DocsGuard: "-", Report: "-", Read: "D"},
		{Field: "FINDINGS", Type: "int 0-999; must equal the number of `## Findings` rows", Fix: "-", Recut: "-", Port: "-", DocsGuard: "-", Report: "-", Read: "D"},
		{Field: "FLOOR", Type: "`HIGH` | `MEDIUM` | `LOW` | `NONE`; `NONE` iff FINDINGS=0", Fix: "-", Recut: "-", Port: "-", DocsGuard: "-", Report: "-", Read: "D"},
		{Field: "SUGGEST", Type: "`APPROVE` | `HOLD`: a suggestion, never a disposition", Fix: "-", Recut: "-", Port: "-", DocsGuard: "-", Report: "-", Read: "D"},
		{Field: "PROBES", Type: "int ≥1; must equal the number of `## Probes` rows", Fix: "-", Recut: "-", Port: "-", DocsGuard: "-", Report: "D", Read: "-"},
		{Field: "sections", Type: "required `## ` headings, each with at least one row", Fix: "Gates, Left owed", Recut: "Gates, Left owed", Port: "Gates, Left owed", DocsGuard: "Verification, Gates", Report: "Probes, Summary", Read: "Findings (rows = FINDINGS, the one zero-row case)"},
	},
}

// FieldKeys returns the 16 typed field keys defined by the Contract.
func (c *ContractDef) FieldKeys() []string {
	var keys []string
	for _, e := range c.Entries {
		if e.Field != "line 1" && e.Field != "line 2" && e.Field != "sections" {
			keys = append(keys, e.Field)
		}
	}
	return keys
}

// StatusWords returns the line-2 terminal words plus DISPOSITION.
func (c *ContractDef) StatusWords() []string {
	return []string{"DONE", "ABSTAIN", "BLOCKED", "DISPOSITION"}
}

// SectionNames returns the unique section names from the contract.
func (c *ContractDef) SectionNames() []string {
	return []string{"Gates", "Left owed", "Verification", "Probes", "Summary", "Findings"}
}

// SectionHeadings returns the exact section headings `## <Name>`.
func (c *ContractDef) SectionHeadings() []string {
	var headings []string
	for _, s := range c.SectionNames() {
		headings = append(headings, "## "+s)
	}
	return headings
}

// RequirementFor returns the requirement code for a given field key and kind.
func (c *ContractDef) RequirementFor(field, kind string) string {
	for _, e := range c.Entries {
		if e.Field == field {
			switch kind {
			case KindFix:
				return e.Fix
			case KindRecut:
				return e.Recut
			case KindPort:
				return e.Port
			case KindDocsGuard:
				return e.DocsGuard
			case KindReport:
				return e.Report
			case KindRead:
				return e.Read
			}
		}
	}
	return ReqUnknown
}

// RequiredSections returns the required section names for kind.
func RequiredSections(kind string) []string {
	switch kind {
	case KindFix, KindRecut, KindPort:
		return []string{"Gates", "Left owed"}
	case KindDocsGuard:
		return []string{"Verification", "Gates"}
	case KindReport:
		return []string{"Probes", "Summary"}
	case KindRead:
		return []string{"Findings"}
	default:
		return nil
	}
}

// Fields returns the template field lines for a given kind.
// These are the exact lines emitted in the typed field block of the kind's fill-in template.
func Fields(kind string) []string {
	switch kind {
	case KindFix:
		return []string{
			"SCHEMA: v2",
			"KIND: fix",
			"ATTEMPT: 1",
			"CHECK: <pass | fail | not-run>",
			"REPO: <owner>/<name>",
			"BRANCH: <branch>",
			"PATHS: <paths>",
			"RED: <command and failing output summary>",
			"GREEN: <command and passing output summary>",
		}
	case KindRecut:
		return []string{
			"SCHEMA: v2",
			"KIND: recut",
			"ATTEMPT: 1",
			"CHECK: <pass | fail | not-run>",
			"REPO: <owner>/<name>",
			"BRANCH: <branch>",
			"PATHS: <paths>",
			"RED: <command and failing output summary>",
			"GREEN: <command and passing output summary>",
			"PRIOR: #<int> @<hex12>",
		}
	case KindPort:
		return []string{
			"SCHEMA: v2",
			"KIND: port",
			"ATTEMPT: 1",
			"CHECK: <pass | fail | not-run>",
			"REPO: <owner>/<name>",
			"BRANCH: <branch>",
			"PATHS: <paths>",
			"RED: <command and failing output summary>",
			"GREEN: <command and passing output summary>",
		}
	case KindDocsGuard:
		return []string{
			"SCHEMA: v2",
			"KIND: docs-guard",
			"ATTEMPT: 1",
			"CHECK: <pass | fail | not-run>",
			"REPO: <owner>/<name>",
			"BRANCH: <branch>",
			"PATHS: <paths>",
		}
	case KindReport:
		return []string{
			"SCHEMA: v2",
			"KIND: report",
			"ATTEMPT: 1",
			"CHECK: <pass | fail | not-run>",
			"REPO: <owner>/<name>",
			"BRANCH: <branch>",
			"PATHS: <paths>",
			"PROBES: <n>",
		}
	case KindRead:
		return []string{
			"SCHEMA: v2",
			"KIND: read",
			"ATTEMPT: 1",
			"CHECK: <pass | fail | not-run>",
			"REPO: <owner>/<name>",
			"PR: <number>",
			"HEAD: <sha40>",
			"FINDINGS: <n>",
			"FLOOR: <floor>",
			"SUGGEST: <APPROVE | HOLD>",
		}
	default:
		return nil
	}
}

// Markdown returns the complete generated markdown for the "Typed records" section
// between <!-- typedrec:begin --> and <!-- typedrec:end -->.
func (c *ContractDef) Markdown() string {
	var b strings.Builder
	b.WriteString("| field | type | fix | recut | port | docs-guard | report | read |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, e := range c.Entries {
		ty := strings.ReplaceAll(e.Type, "|", `\|`)
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			e.Field, ty, e.Fix, e.Recut, e.Port, e.DocsGuard, e.Report, e.Read)
	}
	b.WriteString("\nKey: R = required. D = required when the status is DONE (and the `sections` row applies only on DONE). P = required when DONE and CHECK=pass, optional otherwise. O = optional, and type-checked when present. `-` = unknown for this kind, so the file is refused. On ABSTAIN or BLOCKED only the R rows are required; any other field present is still type-checked. A DONE with CHECK=fail is a valid Returned attempt that stays unverified.\n\n")

	b.WriteString("### Evidence rows\n\n")
	b.WriteString("This one grammar covers every section in every kind.\n")
	b.WriteString("- **Lines.** The evidence region is split on `\\n`.\n")
	b.WriteString("- **Fences.** A fence is a line that starts with three backticks, and each one toggles the fenced state. Fence lines and every line inside a fence are neither rows nor headings.\n")
	b.WriteString("- **Headings.** A heading is any line starting `## ` outside a fence.\n")
	b.WriteString("- **Sections.** A section is the lines after its heading, up to the next heading or the end of the file. A heading is a contract section only when the whole line is exactly `## <Name>`, byte for byte. So `## Findings` counts, while `## findings`, `##Findings`, `## Findings:` and `## Findings ` (trailing space) do not.\n")
	b.WriteString("- **Other headings.** Any other heading, such as `## Notes`, is evidence. It ends the section above it and is otherwise ignored. A `### ` line does not start with `## `, so it neither ends a section nor counts as a row.\n")
	b.WriteString("- **Rows.** A row is a section line that starts at column 0 with `- ` (hyphen, space) and then has at least one byte that is not a space or tab. Nothing else is a row: blank lines, prose, indented lines (nested bullets, continuations), `* ` and `+ ` bullets, numbered items, table lines, `### ` subheadings, `-x` and a bare `- `. They all stay in the file as evidence and are never counted.\n")
	b.WriteString("- **General rule.** Every section named in the kind's `sections` cell must be present exactly once and must have at least one row. There is exactly one exception. On kind=read, `## Findings` must have exactly FINDINGS rows, so FINDINGS=0 means the heading is present with zero rows. Prose such as \"none\" is allowed there, and any row is `contradictory`. FINDINGS and PROBES each equal the row count of their section.\n")
	b.WriteString("- **Section defects,** each named by field:\n")
	b.WriteString("  - A heading that is absent gives `field=## <Name> defect=missing line=0`.\n")
	b.WriteString("  - A heading with zero rows, outside the exception, gives `field=## <Name> defect=missing line=<heading line>`.\n")
	b.WriteString("  - A second identical heading gives `defect=duplicate line=<second heading line>`.\n")
	b.WriteString("  - A count that differs from its rows gives `field=FINDINGS|PROBES defect=contradictory line=<field line>`.\n")
	return b.String()
}
