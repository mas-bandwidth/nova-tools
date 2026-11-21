package main

import (
	"cmp"
	"context"
	"slices"
	"strings"
)

// speaks is the wire protocol each harness speaks, and spokenBy the harnesses that speak each api (the default
// first): a model's harness speaks its provider's api.
var (
	speaks   = map[string]string{"claude": "anthropic", "opencode": "openai", "codex": "openai"}
	spokenBy = map[string][]string{"anthropic": {"claude"}, "openai": {"opencode", "codex"}}
)

// rowNames are the rows' names, comma-separated, or (none).
func rowNames(rows []Row) string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return cmp.Or(strings.Join(out, ","), "(none)")
}

// related refuses a write that breaks a rule between kinds, each refusal naming the fix: a model names an existing
// provider whose api its harness speaks and, when it names one, an existing machine; a provider keeps the api its
// models' harnesses speak and goes only once no model names it; a machine goes only once it serves no model. The store
// holds the provider and machine a model names (foreign keys) whoever writes.
func related(ctx context.Context, st Store, k *kind, op, name string, after Row) error {
	if k.name == "model" && op != "remove" {
		providers, err := st.List(ctx, kinds["provider"])
		if err != nil {
			return err
		}
		p, _, _ := strings.Cut(name, "/") // the provider a model names: the part before the first "/"
		i := slices.IndexFunc(providers, func(r Row) bool { return r.Name == p })
		if i < 0 {
			return refused("model %s: no provider %s; the providers are %s; run: nova-config provider add %s --url <url> --api <openai|anthropic> --actor <you>", name, p, rowNames(providers), p)
		}
		if h, api := after.F["harness"], providers[i].F["api"]; speaks[h] != api {
			ok := spokenBy[api]
			return refused("model %s: harness %s speaks %s and provider %s speaks %s; give --harness %s; run: nova-config model %s %s --harness %s --actor <you>", name, h, speaks[h], p, api, strings.Join(ok, " or "), op, name, ok[0])
		}
		if m := after.F["machine"]; m != "" {
			machines, err := st.List(ctx, kinds["machine"])
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(machines, func(r Row) bool { return r.Name == m }) {
				return refused("model %s: no machine %s; the machines are %s; run: nova-config machine add %s --user <user>", name, m, rowNames(machines), m)
			}
		}
		return nil
	}
	if k.name != "provider" && (k.name != "machine" || op != "remove") || op == "add" {
		return nil
	}
	models, err := st.List(ctx, kinds["model"])
	if err != nil {
		return err
	}
	var named []Row
	for _, m := range models {
		switch {
		case k.name == "machine" && m.F["machine"] == name:
		case k.name == "provider" && strings.HasPrefix(m.Name, name+"/") && (op == "remove" || speaks[m.F["harness"]] != after.F["api"]):
		default:
			continue
		}
		named = append(named, m)
	}
	switch {
	case len(named) == 0:
		return nil
	case k.name == "machine":
		return refused("machine %s still serves models %s; clear their machine first; run: nova-config model set %s --machine '' --actor <you>", name, rowNames(named), named[0].Name)
	case op == "remove":
		return refused("provider %s is still named by models %s; remove them first; run: nova-config model remove %s --actor <you>", name, rowNames(named), named[0].Name)
	}
	api := speaks[named[0].F["harness"]]
	return refused("provider %s keeps api %s while models %s run on it with a harness that speaks %s; remove them first; run: nova-config model remove %s --actor <you>", name, api, rowNames(named), api, named[0].Name)
}
