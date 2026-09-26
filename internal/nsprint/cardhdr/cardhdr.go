// Package cardhdr is the card header's vocabulary: the kinds and routes a
// card's KIND and ROUTE lines may carry, and the one reader of a `KEY: value`
// header line. It is a leaf (standard library only) so both the card session
// (internal/nsprint/card, which lints and runs cards) and the task record
// store (internal/nsprint/taskcard, which renders a card from a record) read
// the header the same way without importing each other: card drives taskcard
// for copy sessions, so taskcard must not import card (PR #3916 landing).
package cardhdr

import (
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Runner kinds: a card hash kind that sets no RESULT expectation (card_run.lua
// says the same of them). A card with no KIND line is KindModel.
const (
	KindModel  = "model"
	KindScript = "script"
)

// RunnerKinds are the card kinds besides typedrec.Kinds that card push accepts.
var RunnerKinds = []string{KindModel, KindScript}

// KindMap is the one declared mapping from a classification kind to the RESULT
// kind the card is pushed as (nova-tools#3651). The cutter and the feed apply
// it at cut time (card.MapKind), and card push --map-kind applies it at push.
// A kind already in typedrec.Kinds or RunnerKinds is never in this table and
// is never rewritten.
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

// IsRunnerKind reports whether kind is one of RunnerKinds.
func IsRunnerKind(kind string) bool {
	for _, k := range RunnerKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// The routes a card may carry: the rung of internal/nsprint/route/routes.yaml
// the bench harness picks its model from (nova-sprint routes --tier <route>,
// first allowed route). A card with no ROUTE line is flash.
const (
	RouteFlash = "flash"
	RoutePro   = "pro"
)

// KeyRE is the header line's shape: a key word at column 0, then a colon.
var KeyRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*):\s*(.*)$`)

// KeyValue reads one `KEY: value` line by the header's own rule, so the issue
// parser that fills a task record (taskcard.ParseIssue) and card push read a
// line the same way.
func KeyValue(line string) (key, value string, ok bool) {
	m := KeyRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), true
}
