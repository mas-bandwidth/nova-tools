package typedrec

import (
	"fmt"
	"strings"
)

// ResultFormat is the RESULT-FORMAT paragraph a worker's brief carries for a
// typed card of kind (nova-tools#3651): every field and section ParseResult
// requires of a DONE file of that kind, read from Contract and
// RequiredSections -- the same tables the parser reads -- then the kind's
// fill-in Template. A DONE card whose worker was never told the grammar ended
// `missing`; the brief and the validator now read one declared list. It is ""
// for a kind outside Kinds.
func ResultFormat(kind string) string {
	if !IsKind(kind) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT-FORMAT (KIND %s, SCHEMA v2). Write RESULT.md in exactly this grammar; card end parses it and records a file that breaks it as missing, malformed or contradictory, and the card is refused.\n", kind)
	b.WriteString("- line 1: this card's line 1, verbatim.\n")
	b.WriteString("- line 2: `DONE`, `ABSTAIN <why>` or `BLOCKED <why>`.\n")
	b.WriteString("- then one `KEY: value` line per field, colon-delimited:\n")
	for _, e := range Contract.Entries {
		if e.Field == "line 1" || e.Field == "line 2" || e.Field == "sections" {
			continue
		}
		when := requirementWords(Contract.RequirementFor(e.Field, kind))
		if when == "" {
			continue
		}
		value := e.Type
		if e.Field == "KIND" {
			value = "`" + kind + "`, this card's KIND"
		}
		fmt.Fprintf(&b, "  - %s: %s (%s)\n", e.Field, value, when)
	}
	b.WriteString("  - no other field: a field this kind does not know is refused.\n")
	var heads []string
	for _, s := range RequiredSections(kind) {
		heads = append(heads, "`## "+s+"`")
	}
	rows := "with at least one `- ` row under it"
	if kind == KindRead {
		rows = "with exactly FINDINGS `- ` rows under it (FINDINGS: 0 is the heading with no rows)"
	}
	fmt.Fprintf(&b, "- then the sections %s on DONE, each heading exactly once, byte for byte, %s.\n", strings.Join(heads, ", "), rows)
	b.WriteString("Fill in this template:\n\n")
	b.WriteString(Template(kind))
	return b.String()
}

// requirementWords says a Contract requirement code in words; "" for a field
// the kind does not know.
func requirementWords(code string) string {
	switch code {
	case ReqRequired:
		return "required"
	case ReqDone:
		return "required on DONE"
	case ReqPass:
		return "required on DONE with CHECK: pass"
	case ReqOptional:
		return "optional, checked when present"
	}
	return ""
}
