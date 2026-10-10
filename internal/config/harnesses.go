package config

import (
	"context"
	"fmt"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/harness"
)

// A machine's harnesses are the harnesses the sprint's member on it can launch: the
// machine row's harnesses field (nova-config machine set <m> --harnesses
// claude,codex), opencode only by default. opencode is launched through the
// providers table with a provider key; a headless harness (claude, codex, grok) runs
// on the machine's own subscription login, which only some machines hold. A member
// can launch only the routes whose harness its machine lists (internal/swarm CanLaunch),
// so a machine with no claude login can launch no subscription-claude route
// (docs/SPEC-CONFIG.md, machine; docs/SPEC-SWARM.md, the headless harnesses).
// nova-config apply writes the list with the rest of the row to machine:<m>.

// HarnessesOf is the harness list a machine row's harnesses field names, in
// harness.Kinds order; a field that is empty or absent (a row before migration 0038)
// is the default, opencode only.
func HarnessesOf(field string) []string {
	words, _ := splitList(field) // a word with = is no harness, and dropped below
	var out []string
	for _, w := range words {
		if slices.Contains(harness.Kinds, w) && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return []string{harness.OpenCode}
	}
	slices.SortFunc(out, func(a, b string) int {
		return slices.Index(harness.Kinds, a) - slices.Index(harness.Kinds, b)
	})
	return out
}

// Harnesses is every machine row's harness list (HarnessesOf), by machine name,
// read from the machine rows alone.
func Harnesses(ctx context.Context, st Store) (map[string][]string, error) {
	machines, err := st.List(ctx, KindMachine)
	if err != nil {
		return nil, fmt.Errorf("harnesses: read machines: %w", err)
	}
	out := make(map[string][]string, len(machines))
	for _, m := range machines {
		out[m.Name] = HarnessesOf(m.Fields["harnesses"])
	}
	return out, nil
}
