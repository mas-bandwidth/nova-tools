package typedrec

import (
	"fmt"
	"strings"
)

// ResultFormat is the RESULT-FORMAT paragraph a worker's brief carries for a
// typed card of kind. Since nova-tools#3689 it is the two-line contract: the
// model writes line 1 (this card's line 1, verbatim) and line 2 (`DONE`,
// `ABSTAIN <why>` or `BLOCKED <why>`) and an optional note; the card wrapper
// writes every field it knows or computes (WrapperOwned, the Gates rows) into
// the record it validates (Synthesize). A kind whose DONE record needs
// something only the model can know (JudgementFields) names those too. It is
// "" for a kind outside Kinds.
func ResultFormat(kind string) string {
	if !IsKind(kind) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT-FORMAT (KIND %s). When the work is done, write RESULT.md:\n", kind)
	b.WriteString("- line 1: this card's line 1, verbatim.\n")
	b.WriteString("- line 2: `DONE`, `ABSTAIN <why>` or `BLOCKED <why>`.\n")
	fields, sections := JudgementFields(kind)
	if len(fields) > 0 || len(sections) > 0 {
		var want []string
		for _, f := range fields {
			want = append(want, "`"+f+": <value>`")
		}
		for _, s := range sections {
			want = append(want, "`## "+s+"` with `- ` rows")
		}
		fmt.Fprintf(&b, "- on DONE, then: %s.\n", strings.Join(want, ", "))
	}
	b.WriteString("- then, optionally, a short note (anything left owed).\n")
	b.WriteString("Nothing else: the wrapper writes every other field from the card, the branch, the diff and its own run of the card's TEST line.\n")
	return b.String()
}

// JudgementFields are what a DONE record of kind needs that the wrapper cannot
// know: the Contract fields the kind requires that are not WrapperOwned, and
// its required sections other than Gates and Left owed.
func JudgementFields(kind string) (fields, sections []string) {
	owned := map[string]bool{}
	for _, f := range WrapperOwned {
		owned[f] = true
	}
	for _, f := range Contract.FieldKeys() {
		switch Contract.RequirementFor(f, kind) {
		case ReqRequired, ReqDone, ReqPass:
			if !owned[f] {
				fields = append(fields, f)
			}
		}
	}
	for _, s := range RequiredSections(kind) {
		if s != "Gates" && s != "Left owed" {
			sections = append(sections, s)
		}
	}
	return fields, sections
}
