package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// keyStreamRules holds each stream's rules by reference (nova-tools#5174 rule 6): a JSON
// object of stream -> the base name of the held rules file (swarm.HeldRules) the member
// injects into the stream's cards at stage time, written by `nova-sprint add` when its rule
// set is a file the members hold. One record for the whole sprint, as keyRules is: a clear
// keeps it and teardown removes it.
const keyStreamRules = "stream-rules"

// StreamRules is each stream's rules file by reference, by stream; empty when none is
// recorded or the backend keeps no keys.
func (st *Store) StreamRules(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	kv, ok := st.B.(KV)
	if !ok {
		return out, nil
	}
	v, found, err := kv.GetKey(ctx, keyStreamRules)
	if err != nil || !found || v == "" {
		return out, err
	}
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil, fmt.Errorf("the record %s is not a stream -> rules object: %w", keyStreamRules, err)
	}
	return out, nil
}

// SetStreamRules records name as the rules file by reference of each of streams, over what
// they recorded before; an empty name removes their record. A call that changes nothing
// writes nothing, so an add that records none on a backend with no keys is no error; one that
// records a name there is refused.
func (st *Store) SetStreamRules(ctx context.Context, streams []string, name string) error {
	all, err := st.StreamRules(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, s := range streams {
		if was, ok := all[s]; name == "" && ok {
			delete(all, s)
			changed = true
		} else if name != "" && was != name {
			all[s] = name
			changed = true
		}
	}
	if !changed {
		return nil
	}
	kv, ok := st.B.(KV)
	if !ok {
		return errNoKeys
	}
	raw, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyStreamRules, string(raw))
}
