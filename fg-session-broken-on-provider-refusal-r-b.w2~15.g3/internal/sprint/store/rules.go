package store

import (
	"context"
	"errors"
)

var errNoKeys = errors.New("this store keeps no machine records")

// keyRules holds the path of the sprint's child-rules file, set once by
// `nova-sprint init --rules <file>` and read by `add` when it holds a brief to the card
// lint (swarm.LintCardChildWith): one for the whole sprint, under its prefix, never per
// epoch, so a clear keeps it and teardown removes it, as it does the coordinator.
const keyRules = "rules"

// RulesPath is the child-rules file the sprint names, "" when none is named: the brief is
// then held to the built-in general rules. A backend with no keys (not a KV) names none.
func (st *Store) RulesPath(ctx context.Context) (string, error) {
	kv, ok := st.B.(KV)
	if !ok {
		return "", nil
	}
	v, _, err := kv.GetKey(ctx, keyRules)
	return v, err
}

// SetRulesPath records the child-rules file the sprint names (an empty path records none:
// the built-in rules hold again). It is refused when the backend keeps no keys.
func (st *Store) SetRulesPath(ctx context.Context, path string) error {
	kv, ok := st.B.(KV)
	if !ok {
		return errNoKeys
	}
	return kv.SetKey(ctx, keyRules, path)
}
