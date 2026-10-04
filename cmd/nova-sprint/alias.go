package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A judgment's alias (sprint.JudgmentAliases): inbox prints j<n> beside each
// judgment, and every verb that takes a judgment id (--answers, --group, wait,
// ack, inbox --open) takes the alias in its place, read back to the id here
// before the step, so the core sees ids alone (the comfort list of 2026-10-03,
// item 10). The store is read once, and only when a word is an alias.

// answersWords is the --answers flag's help, every judgment verb's.
const answersWords = "the judgment notifications this answers, comma separated, each an id as inbox prints it or its alias (j<n>); coordinator-only; one invalid answer refuses the whole step, writing nothing"

// unalias is ids with each alias read back to the judgment id it names; an
// alias naming no judgment of the epoch is an error naming it.
func unalias(ctx context.Context, st *store.Store, ids []string) ([]string, error) {
	var byAlias map[string]string
	out := append([]string(nil), ids...)
	for i, id := range ids {
		if !sprint.IsAlias(id) {
			continue
		}
		if byAlias == nil {
			var err error
			if byAlias, err = st.B.Aliases(ctx, aliasesIn(ids)); err != nil {
				return nil, err
			}
		}
		full, ok := byAlias[id]
		if !ok {
			return nil, fmt.Errorf("no judgment %s in this epoch; run: nova-sprint inbox", id)
		}
		out[i] = full
	}
	return out, nil
}

// unaliasFlag reads the aliases of a comma-separated flag back to ids, in place
// (the verb's variable follows the flag's value): "" is no error.
func unaliasFlag(ctx context.Context, st *store.Store, fs flagSet, name string) error {
	f := fs.Lookup(name)
	if f == nil || f.Value.String() == "" {
		return nil
	}
	ids, err := unalias(ctx, st, sprint.Split(f.Value.String()))
	if err != nil {
		return err
	}
	return f.Value.Set(strings.Join(ids, ","))
}

// aliasesIn is the words of ids that are aliases.
func aliasesIn(ids []string) []string {
	var out []string
	for _, id := range ids {
		if sprint.IsAlias(id) {
			out = append(out, id)
		}
	}
	return out
}
