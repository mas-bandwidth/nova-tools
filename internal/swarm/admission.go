package swarm

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
)

// AdmissionContract is the one sentence nova-swarm lint and nova-sprint add
// print, so both helps name the same checks. docs/SPEC-SWARM.md, Card lint.
const AdmissionContract = "Admission (nova-sprint add) holds a brief to the coordinator's child rules, the same checks as nova-swarm lint --card --child-rules (the sentences of --child-rules-file, or of add --rules, or of the file init --rules recorded, else the six general rules): each rule-<name>, the step-<what> scans (step-go-clean and step-go-test-timeout only when that set carries those rules), rule-libraries-considered when the set carries [libraries-considered], a tree card's step checks, and the brief's model lines. A drift on any other token of nova-swarm lint --card does not bind admission, so that lint can exit 1 while add admits the same text. A drift on a token that admission holds is the same check, and add still refuses it."

// AdmissionBinds reports whether nova-sprint add refuses a brief on this lint
// token. The coordinator's child rules (rule-<name>, step-<what>, the empty
// card, libraries-considered) and a tree card's step checks bind. A shape
// token of a bare nova-swarm lint --card does not.
func AdmissionBinds(check string) bool {
	if _, ok := cardtree.Remedies[check]; ok {
		return true
	}
	if check == EmptyCardCheck || check == LibrariesConsideredRule {
		return true
	}
	return strings.HasPrefix(check, "rule-") || strings.HasPrefix(check, "step-")
}
