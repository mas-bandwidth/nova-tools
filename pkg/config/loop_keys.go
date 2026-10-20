package config

import (
	"sort"
	"strings"
)

// EffectiveKeys is the secret names a loop row's unit loads: the row's own
// keys plus, for a loop whose argv runs a nova-swarm member or reader, the
// key_env of every enabled provider row. It is tla/ProviderKey.tla's
// UnitsLoadEveryEnabledKey and AddLoop: a member or reader serves provider
// routes, so a provider row needs no loop row, unit or play edit to have its
// key loaded (docs/SPEC-CONFIG.md, "loop"). The answer is sorted and
// deduplicated, the shape a TypeKeys field stores.
func EffectiveKeys(loop Row, providers []Row) []string {
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if k = strings.TrimSpace(k); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, k := range strings.Split(loop.Fields["keys"], ",") {
		add(k)
	}
	if _, route := memberAt(Argv(loop.Fields["argv"])); route {
		for _, p := range providers {
			if p.Fields["enabled"] == "false" {
				continue
			}
			add(p.Fields["key_env"])
		}
	}
	sort.Strings(out)
	return out
}
