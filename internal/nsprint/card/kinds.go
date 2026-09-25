package card

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// THE CARD KIND IS DECIDED AT PUSH, NOT AT CARD END (nova-tools#3651).
//
// ns_card_result (internal/nsprint/fn/lua/card_run.lua) persists a RESULT.md
// whose KIND is outside typedrec.Kinds as malformed, so a card pushed with a
// classification kind (go-verb, spec, ...) ran to the end, spent its model
// tokens, and was refused only then. card push now refuses any KIND that is
// neither one of typedrec.Kinds (the six RESULT kinds the validator reads)
// nor a runner kind, and names the set. KindMap is the one declared mapping
// from the classification kinds a cutter carries to a RESULT kind; MapKind
// applies it to a card's bytes, and `nova-sprint card push --map-kind` applies
// it before the payload sha is taken.

// Runner kinds: a card hash kind that sets no RESULT expectation (card_run.lua
// says the same of them). A card with no KIND line is KindModel.
const (
	KindModel  = "model"
	KindScript = "script"
)

// RunnerKinds are the card kinds besides typedrec.Kinds that card push accepts.
var RunnerKinds = []string{KindModel, KindScript}

// KindMap is the one declared mapping from a classification kind to the RESULT
// kind the card is pushed as. The cutter and the feed apply it at cut time
// (MapKind), and card push --map-kind applies it at push. A kind already in
// typedrec.Kinds or RunnerKinds is never in this table and is never rewritten.
var KindMap = map[string]string{
	"go-verb":  typedrec.KindFix,
	"go-fix":   typedrec.KindFix,
	"lua-fn":   typedrec.KindFix,
	"bats":     typedrec.KindFix,
	"security": typedrec.KindFix,
	"fleet":    typedrec.KindFix,
	"retire":   typedrec.KindFix,
	"docs":     typedrec.KindDocsGuard,
	"spec":     typedrec.KindReport,
	"probe":    typedrec.KindReport,
}

func isRunnerKind(kind string) bool {
	for _, k := range RunnerKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// checkKind is the push-time KIND check. An absent KIND is a model card.
func checkKind(kind string) error {
	if kind == "" || typedrec.IsKind(kind) || isRunnerKind(kind) {
		return nil
	}
	hint := ""
	if to, ok := KindMap[kind]; ok {
		hint = fmt.Sprintf("; card push --map-kind pushes it as %s", to)
	}
	return fmt.Errorf("KIND: %s is not a RESULT kind; one of %s (runner kinds: %s)%s",
		kind, strings.Join(typedrec.Kinds, ", "), strings.Join(RunnerKinds, ", "), hint)
}

// MappedKinds lists KindMap's classification kinds, sorted.
func MappedKinds() []string {
	out := make([]string, 0, len(KindMap))
	for k := range KindMap {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MapKind rewrites the card's header KIND line from a classification kind to
// its RESULT kind by KindMap, leaving every other byte as it was (a CRLF line
// keeps its CR). A card with no KIND line, a KIND already accepted, or a KIND
// the table does not name is returned unchanged, and push's lint decides it.
// from and to are the KIND before and after; they are equal when nothing
// changed.
func MapKind(body []byte) (out []byte, from, to string) {
	_, entries := scanHeader(body)
	for _, e := range entries {
		if e.key != "KIND" {
			continue
		}
		mapped, ok := KindMap[e.value]
		if !ok {
			return body, e.value, e.value
		}
		lines := strings.Split(string(body), "\n")
		if e.index >= len(lines) {
			return body, e.value, e.value // scanHeader's index is into this split; never reached
		}
		line := "KIND: " + mapped
		if strings.HasSuffix(lines[e.index], "\r") {
			line += "\r"
		}
		lines[e.index] = line
		return []byte(strings.Join(lines, "\n")), e.value, mapped
	}
	return body, "", ""
}
