package tool

import (
	"slices"
	"strings"
)

// Need is what one nova tool, or one of its verbs, depends on and where it
// works: the stores its binary links a client for, the programs it runs, and
// the platforms it works on. The facts are declared once, in Needs, and every
// surface that states them reads them from there: the banner's `needs:` line
// and the effect line of `<verb> -h` (needsLine), and the docs test that holds
// Stores to the client packages the binary links and the catalog and README
// rows to Stores (internal/docs, TestLiveDocsNameOneBackendPerFact).
type Need struct {
	Tool      string   // nova-<name>
	Verb      string   // "" for the tool's row; a verb's row states only how it differs
	Stores    []string // the stores a client is linked for: Redis, PostgreSQL
	Programs  []string // programs some verb runs, each named by its command
	Platforms []string // GOOS values it works on; nil is every platform it builds for
}

// Store names, as the docs spell them.
const (
	Redis      = "Redis"
	PostgreSQL = "PostgreSQL"
)

// Stores is every store a Need may name.
var Stores = []string{Redis, PostgreSQL}

// Needs is the one declaration, a row per tool and a row per verb that
// differs from its tool. Stores is exactly what the binary links (the docs
// test reads `go list -deps`); a tool with no row needs no store, no program
// and works on every platform.
var Needs = []Need{
	{Tool: "nova-bus", Stores: []string{Redis}},
	{Tool: "nova-ci", Stores: []string{Redis}, Programs: []string{"go"}},
	{Tool: "nova-config", Stores: []string{PostgreSQL, Redis}},
	{Tool: "nova-friend", Stores: []string{Redis}},
	{Tool: "nova-friend", Verb: "install", Stores: []string{Redis}, Programs: []string{"launchctl"}, Platforms: []string{"darwin"}},
	{Tool: "nova-friend", Verb: "uninstall", Stores: []string{Redis}, Programs: []string{"launchctl"}, Platforms: []string{"darwin"}},
	{Tool: "nova-local", Programs: []string{"ollama"}},
	{Tool: "nova-redis", Stores: []string{Redis}},
	{Tool: "nova-redis", Verb: "serve", Stores: []string{Redis}, Programs: []string{"redis-server"}},
	{Tool: "nova-sandbox", Verb: "run", Platforms: []string{"darwin", "windows"}},
	{Tool: "nova-secrets", Programs: []string{"age-keygen", "sops"}},
	{Tool: "nova-sprint", Stores: []string{PostgreSQL, Redis}},
	{Tool: "nova-table", Stores: []string{Redis}},
	{Tool: "nova-tokens", Stores: []string{Redis}, Programs: []string{"sqlite3"}},
	{Tool: "nova-update", Stores: []string{Redis}},
	{Tool: "nova-version", Stores: []string{Redis}, Programs: []string{"go"}},
	{Tool: "nova-work", Programs: []string{"gh"}},
}

// NeedOf is the row for tool and verb: the verb's own row when it has one,
// else the tool's; ok is false when neither is declared.
func NeedOf(tool, verb string) (Need, bool) {
	at := slices.IndexFunc(Needs, func(n Need) bool { return n.Tool == tool && n.Verb == verb && verb != "" })
	if at < 0 {
		at = slices.IndexFunc(Needs, func(n Need) bool { return n.Tool == tool && n.Verb == "" })
	}
	if at < 0 {
		return Need{}, false
	}
	return Needs[at], true
}

// needsLine is the one line the banner and `<verb> -h` print for n:
// "needs: Redis; runs: redis-server; platforms: darwin". "" when n declares
// nothing.
func needsLine(n Need) string {
	var parts []string
	if len(n.Stores) > 0 {
		parts = append(parts, strings.Join(n.Stores, ", "))
	}
	if len(n.Programs) > 0 {
		parts = append(parts, "runs: "+strings.Join(n.Programs, ", "))
	}
	if len(n.Platforms) > 0 {
		parts = append(parts, "platforms: "+strings.Join(n.Platforms, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "needs: " + strings.Join(parts, "; ")
}
