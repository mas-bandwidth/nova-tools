package typedrec

// Requirement codes in the contract.
const (
	ReqRequired = "R" // Required always
	ReqDone     = "D" // Required when status is DONE
	ReqPass     = "P" // Required when DONE and CHECK=pass, optional otherwise
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

// Contract is the canonical Go table defining RESULT v2.
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
		{Field: "PATHS", Type: "1-256 whitespace-separated repo-relative paths; no `..`, no leading `/`, no duplicates", Fix: "D", Recut: "D", Port: "D", DocsGuard: "D", Report: "O", Read: "-"},
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
