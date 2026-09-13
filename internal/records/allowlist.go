package records

import "sort"

// Schema constants for the three retained record bodies.
const (
	SchemaObservation = "nova.tokens.observation/2"
	SchemaMapping     = "nova.tokens.mapping/2"
	SchemaCoverage    = "nova.tokens.coverage/2"
)

// SourceKinds is the allowlist of source.kind values, and it is exactly the harness shapes
// the two proposal documents name at 71ea08b -- claude_code and opencode and nova_swarm
// from the v1 readers and the wrapper-overlap paragraph, codex_desktop and antigravity and
// grok from the counter-semantics paragraph, v1_aggregate from "V1 imports ... are
// labelled aggregate observations". A kind outside it is a bad declaration by the caller
// rather than an unreadable record, the same distinction internal/tokens draws for a
// --provider label that names no parser.
var SourceKinds = []string{
	"antigravity", "claude_code", "codex_desktop", "grok", "nova_swarm", "opencode", "v1_aggregate",
}

// ReasonCodes is the bounded set an absent or unavailable field may give. Bounded is the
// requirement the format states; these are the seven distinctions the documents draw
// between a field a source never had, one it had and would not give, and one this
// collection could not read.
var ReasonCodes = []string{
	"not_supplied", "not_supported_by_source", "source_unavailable",
	"unsupported_semantics", "parse_failed", "redacted", "omitted_from_wire",
}

// Allowlists carries the two sets that belong to a MAPPING rather than to the format: the
// source field names raw_usage may carry, and the native locator fields a receipt may
// carry. The format calls both "mapping-allowlisted", so core cannot know them; it
// enforces that something named them. An empty set means no field is allowed, which is the
// safe direction: a record whose mapping was not supplied is refused, not accepted whole.
type Allowlists struct {
	RawUsageFields []string
	ReceiptFields  []string
}

func (a Allowlists) rawUsage() map[string]bool { return set(a.RawUsageFields) }
func (a Allowlists) receipt() map[string]bool  { return set(a.ReceiptFields) }

func set(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func sortedSet(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

// The enumerations the observation table fixes. Each is a closed set; a value outside it is
// unknown_enum naming the field, never a value mapped to the nearest neighbour.
var (
	observationKinds = set([]string{"request", "turn", "snapshot", "aggregate"})
	revisionBases    = set([]string{"source_order", "operator_correction", "none"})
	timeBases        = set([]string{"response_observation", "turn_completion", "measured_interval", "source_aggregate", "unknown"})
	originBases      = set([]string{"source", "owner_binding", "unknown"})
	modelBases       = set([]string{"provider_reported", "harness_reported", "requested", "mixed", "unknown"})
	repoBases        = set([]string{"source_binding", "explicit_policy", "unattributed"})
	presences        = set([]string{"present", "absent", "unavailable"})
	numberKinds      = set([]string{"integer", "decimal"})
	reasonCodes      = set(ReasonCodes)
	sourceKinds      = set(SourceKinds)
)
