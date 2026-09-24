package land

import (
	"regexp"
	"strings"
)

// Refusal is one row of the lander's refusal table (Issue #3139 rev 7 §8.1): every refusal is a
// named line with a remedy, and this table is the one place they are named. A reason is the text
// after REFUSED that a land function returns (land.lua, the publisher, benching); Pattern matches
// it, Remedy is the command that moves it ({1}, {2} are Pattern's groups), and Example is a
// reason the walk (TestRefusalTableWalk) resolves back to this row.
type Refusal struct {
	Name    string
	Pattern *regexp.Regexp
	Remedy  string
	Example string
}

// ReinstateFresh is how young a bench-conform PASS must be for land bench --reinstate (§8.3).
const reinstateFreshText = "15m"

// BenchedReason is the refusal a benched bench gets on a gate (§8.3, L20).
func BenchedReason(bench string) string { return "benched bench=" + bench }

// ReinstateReason is the refusal of --reinstate without a bench-conform PASS younger than 15 min.
func ReinstateReason(bench string) string {
	return "reinstate bench=" + bench + " needs bench-conform PASS within " + reinstateFreshText
}

// NotBenchedReason is the refusal of --reinstate on a bench that is not benched.
func NotBenchedReason(bench string) string { return "bench=" + bench + " not benched" }

const (
	remedyStatus = "nova-sprint land status"
	remedyWhy1   = "nova-sprint why {1}"
)

func row(name, pattern, remedy, example string) Refusal {
	return Refusal{Name: name, Pattern: regexp.MustCompile("^" + pattern + "$"), Remedy: remedy, Example: example}
}

// refusalTable is every refusal the landing path can print. Order does not matter: the walk
// requires each example to match exactly one row.
var refusalTable = []Refusal{
	// ns_unit_eval
	row("unit-landed", `landed`, "nova-sprint why <unit>", "landed"),
	row("unit-dropped-at-head", `dropped at head`, "nova-sprint why <unit>", "dropped at head"),
	// ns_batch_plan shape (§4, L3, L24, L27)
	row("chain-max", `chain_max`, remedyStatus, "chain_max"),
	row("parent-not-in-chain", `parent=(\S+) not in chain`, remedyStatus, "parent=b7 not in chain"),
	row("no-members", `no members`, remedyStatus, "no members"),
	row("member-no-unit", `member=(\S+) no unit`, remedyWhy1, "member=12@abc no unit"),
	row("member-landed", `member=(\S+) already landed`, remedyWhy1, "member=gh/o/r/1 already landed"),
	row("member-batched", `member=(\S+) already batched in (\S+)`, remedyStatus, "member=gh/o/r/1 already batched in b3"),
	row("member-not-landable", `member=(\S+) not landable`, remedyWhy1, "member=gh/o/r/1 not landable"),
	row("member-twice", `member=(\S+) twice`, remedyWhy1, "member=gh/o/r/1 twice"),
	row("class-mismatch", `class=(\S+) (\S+)!=(\S+)`, remedyWhy1, "class=gh/o/r/1 lisp!=go"),
	row("alone", `alone=(\S+)`, remedyWhy1, "alone=gh/o/r/1"),
	row("overlap", `overlap=(\S+)`, remedyStatus, "overlap=internal/nsprint/land/land.go"),
	row("roadmap-class", `roadmap-class=(\S+)`, remedyStatus, "roadmap-class=cmd/nova-sprint/main.go"),
	row("roadmap-path", `roadmap-path=(\S+)`, remedyStatus, "roadmap-path=docs/roadmaps/nova-work.sexp"),
	row("stack-parent", `stack-parent=(\S+) member=(\S+)`, "nova-sprint why {2}", "stack-parent=gh/o/r/1 member=gh/o/r/2"),
	// writer generation and lease (§2.3, §10.2, L30)
	row("writer-owner", `writer owner not nova-sprint`, remedyStatus, "writer owner not nova-sprint"),
	row("lease-mismatch", `lease mismatch`, remedyStatus, "lease mismatch"),
	row("lease-gen-mismatch", `lease gen mismatch`, remedyStatus, "lease gen mismatch"),
	// ns_land_intent (§7.1, L23, L28, L32)
	row("unresolved-pub", `unresolved pub (\S+)`, remedyStatus, "unresolved pub b9"),
	row("batch-not-green", `batch not green`, remedyStatus, "batch not green"),
	row("from-tip-mismatch", `from_tip mismatch`, remedyStatus, "from_tip mismatch"),
	row("invalid-member", `invalid member (\S+)`, remedyStatus, "invalid member x"),
	row("head-mismatch", `head mismatch on (\S+)`, remedyWhy1, "head mismatch on gh/o/r/1"),
	row("hold", `hold on (\S+)`, remedyWhy1, "hold on gh/o/r/1"),
	row("policy-mismatch", `policy mismatch`, "nova-sprint land preflight", "policy mismatch"),
	row("inbound-stale", `inbound-stale(?: since \S+)?`, remedyStatus, "inbound-stale since 1727200000"),
	// the publisher (publish.go)
	row("stale", `stale`, remedyStatus, "stale"),
	row("nondeterministic", `nondeterministic b(\S+)(?: .*)?`, "nova-sprint land requeue {1}", "nondeterministic b42"),
	// benching (§8.3, L20)
	row("benched", `benched bench=(\S+)`, "nova-sprint land bench {1} --reinstate", "benched bench=studio"),
	row("reinstate-conform", `reinstate bench=(\S+) needs bench-conform PASS within 15m`, "bench-conform --publish", "reinstate bench=studio needs bench-conform PASS within 15m"),
	row("not-benched", `bench=(\S+) not benched`, remedyStatus, "bench=studio not benched"),
}

// Refusals returns the refusal table (a copy).
func Refusals() []Refusal { return append([]Refusal(nil), refusalTable...) }

// LookupRefusal returns the row naming reason; ok is false for a reason no row names.
func LookupRefusal(reason string) (Refusal, bool) {
	reason = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reason), "REFUSED "))
	for _, r := range refusalTable {
		if r.Pattern.MatchString(reason) {
			return r, true
		}
	}
	return Refusal{}, false
}

// RemedyFor returns the remedy command for reason, groups filled in; an unnamed reason gets
// land status, the one verb that shows every refusal.
func RemedyFor(reason string) string {
	reason = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reason), "REFUSED "))
	r, ok := LookupRefusal(reason)
	if !ok {
		return remedyStatus
	}
	remedy := r.Remedy
	m := r.Pattern.FindStringSubmatch(reason)
	for i := 1; i < len(m); i++ {
		remedy = strings.ReplaceAll(remedy, "{"+string(rune('0'+i))+"}", m[i])
	}
	return remedy
}

// RefusedLine is the one line every refusal prints: REFUSED <reason> remedy=<cmd> (§7.7).
func RefusedLine(reason string) string {
	reason = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reason), "REFUSED "))
	return "REFUSED " + reason + " remedy=" + RemedyFor(reason)
}
