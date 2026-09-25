package card

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// ModelEnd is the model's own word at end (nova-tools#3919): a harness that
// exited DONE whose RESULT.md line 2 is `ABSTAIN <why>` ends ABSTAIN, and
// `BLOCKED <why>` ends BLOCKED, with line 2 as the end's why. ns_card_end
// then moves an ABSTAIN to done/abstain and a BLOCKED to done/fail, where a
// DONE end would have been done/ok with nothing done. The reason is the
// first word of <why> when the reason table has it for that outcome (scope;
// env, base-moved, deps, spec, access), else other. Anything else, or no
// RESULT.md, leaves the end as it was (ok false).
func ModelEnd(kind, out string, end WrapperEnd) (WrapperEnd, bool) {
	if end.Outcome != "DONE" {
		return end, false
	}
	raw, err := os.ReadFile(filepath.Join(out, "RESULT.md"))
	if err != nil {
		return end, false
	}
	m := typedrec.SplitModel(raw, kind)
	_, why, _ := strings.Cut(m.Line2, " ")
	first, _, _ := strings.Cut(strings.TrimSpace(why), " ")
	switch m.Status {
	case typedrec.StatusAbstain:
		end.Outcome, end.Reason = "ABSTAIN", "other"
		if first == "scope" {
			end.Reason = "scope"
		}
	case typedrec.StatusBlocked:
		end.Outcome, end.Reason = "BLOCKED", "other"
		switch first {
		case "env", "base-moved", "deps", "spec", "access":
			end.Reason = first
		}
	default:
		return end, false
	}
	end.Why = oneField(m.Line2)
	return end, true
}
