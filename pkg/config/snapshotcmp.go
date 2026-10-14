package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This file is compare's pure core: it judges what changed between two
// snapshots of the eight kinds by asking the store's own history who made the
// change. The release executor's snapcmp.sh reads only the two snapshots, so
// it cannot tell an operator's deliberate set from the adopt corrupting
// working state and reverts on the first difference; the tool that owns the
// history can name the act behind a changed row (docs/SPEC-CONFIG.md,
// "History"). The verb that renders this value is cmd/nova-config/compare.go.

// MigrationActor is the actor nova-config records when it applies a migration
// itself: migrate makes the seed rows (the fleet, the sprint, the tiers, the
// providers a legacy route declares) and writes no history for them, so a
// difference only a migration can explain has no operator act behind it. It is
// the default --ignore-actor, read from the code and named in the report.
const MigrationActor = "migrate"

// The three verdicts, worst last: SAME is an identical snapshot, OPERATOR a
// difference a person's, a seat's or a friend's recorded set explains (reported
// and never a failure), CRIT a difference with no such act in the window.
const (
	CompareSame     = "SAME"
	CompareOperator = "OPERATOR-DIFF"
	CompareCrit     = "CRIT-DIFF"
)

// liveFields are the fields snapcmp.sh never compares: a provider's balance and
// a machine's beat are live readings of the outside world, not working state a
// person edits.
var liveFields = map[string]bool{"balance_usd": true, "balance_as_of": true, "beat": true}

// CmpRow is one row in a snapshot: its name and every field, the same
// values `nova-config {kind} list --json` prints.
type CmpRow struct {
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields"`
}

// CmpKind is one kind's rows at one instant.
type CmpKind struct {
	Kind string   `json:"kind"`
	Rows []CmpRow `json:"rows"`
}

// CmpSnap is what `nova-config snapshot --out {dir}` wrote: the eight kinds at
// one instant, and when.json's time (RFC 3339 UTC) and tool (the version).
type CmpSnap struct {
	At    string             `json:"at"`
	Tool  string             `json:"tool"`
	Kinds map[string]CmpKind `json:"kinds"`
}

// whenFile is the shape of {dir}/when.json.
type whenFile struct {
	At   string `json:"at"`
	Tool string `json:"tool"`
}

// FieldDiff is one compared field's before and after.
type FieldDiff struct {
	Field  string
	Before string
	After  string
}

// RowDiff is one row that differs: its class, the compared fields that differ
// and, for an OPERATOR-DIFF, the operator entry that explains them.
type RowDiff struct {
	Kind   string
	Name   string
	Class  string
	Fields []FieldDiff
	Actor  string
	At     string
	Reason string
	// Added names the fields the after row has and the before row did not:
	// the new build added them, so they are listed apart, never compared.
	Added []string
	// Unexplained names the differing fields no operator act in the window
	// explains: a CRIT names them apart from the explained ones.
	Unexplained []string
}

// KindDiff is one kind's verdict and the rows that differ.
type KindDiff struct {
	Kind  string
	Class string
	Rows  []RowDiff
	// Added names the kinds' fields the new build added across its rows.
	Added []string
}

// CompareResult is the whole comparison: one KindDiff per kind in the after
// snapshot, and the worst class of them all.
type CompareResult struct {
	Kinds []KindDiff
	Class string
}

// CompareOptions are compare's knobs.
type CompareOptions struct {
	// IgnoreActors are the actors whose history entries are the tool writing
	// the store, not an operator act: a difference only they explain is CRIT.
	// Empty means the default, MigrationActor.
	IgnoreActors []string
}

// CompareSnapshots judges each kind of after against before: for every row
// that differs it asks st's history for that row between the before and the
// after instant and names the act behind it (docs/SPEC-CONFIG.md, "History").
// A nil store cannot answer, so every difference with no history is CRIT.
func CompareSnapshots(ctx context.Context, st Store, before, after CmpSnap, opts CompareOptions) (CompareResult, error) {
	ignore := opts.IgnoreActors
	if len(ignore) == 0 {
		ignore = []string{MigrationActor}
	}
	ignoreSet := map[string]bool{}
	for _, a := range ignore {
		ignoreSet[a] = true
	}
	from, err := parseInstant(before.At)
	if err != nil {
		return CompareResult{}, fmt.Errorf("before snapshot: %w", err)
	}
	to, err := parseInstant(after.At)
	if err != nil {
		return CompareResult{}, fmt.Errorf("after snapshot: %w", err)
	}
	res := CompareResult{Class: CompareSame}
	for _, kind := range sortedKinds(after.Kinds) {
		kd := KindDiff{Kind: kind, Class: CompareSame}
		bKind := before.Kinds[kind]
		bRows := map[string]CmpRow{}
		for _, r := range bKind.Rows {
			bRows[r.Name] = r
		}
		aRows := append([]CmpRow(nil), after.Kinds[kind].Rows...)
		sort.Slice(aRows, func(i, j int) bool { return aRows[i].Name < aRows[j].Name })
		for _, a := range aRows {
			b, ok := bRows[a.Name]
			if !ok {
				// a row the after snapshot has and the before did not: a new
				// row with no history in the window is an unexplained change.
				rd := RowDiff{Kind: kind, Name: a.Name, Class: CompareCrit}
				kd.Rows = append(kd.Rows, rd)
				kd.Class = worse(kd.Class, CompareCrit)
				continue
			}
			var diffs []FieldDiff
			var added []string
			for _, f := range sortedFields(b.Fields) {
				if liveFields[f] {
					continue
				}
				if a.Fields[f] != b.Fields[f] {
					diffs = append(diffs, FieldDiff{Field: f, Before: b.Fields[f], After: a.Fields[f]})
				}
			}
			for _, f := range sortedFields(a.Fields) {
				if liveFields[f] {
					continue
				}
				if _, ok := b.Fields[f]; !ok {
					added = append(added, f)
				}
			}
			if len(added) > 0 {
				kd.Added = append(kd.Added, added...)
			}
			if len(diffs) == 0 {
				if len(added) > 0 {
					kd.Rows = append(kd.Rows, RowDiff{Kind: kind, Name: a.Name, Class: CompareSame, Added: added})
				}
				continue
			}
			rd, err := classifyRow(ctx, st, kind, a.Name, diffs, added, ignoreSet, from, to)
			if err != nil {
				return CompareResult{}, err
			}
			kd.Rows = append(kd.Rows, rd)
			kd.Class = worse(kd.Class, rd.Class)
		}
		res.Kinds = append(res.Kinds, kd)
		res.Class = worse(res.Class, kd.Class)
	}
	return res, nil
}

// classifyRow is the rule per row: an operator entry in the window that
// explains every differing field is OPERATOR (the last such entry names the
// actor, the time and the reason); a difference only the migration actor
// explains, or no history at all, is CRIT; a row differing in two fields, one
// explained and one not, is CRIT naming the unexplained field.
func classifyRow(ctx context.Context, st Store, kind, name string, diffs []FieldDiff, added []string, ignoreSet map[string]bool, from, to time.Time) (RowDiff, error) {
	rd := RowDiff{Kind: kind, Name: name, Class: CompareCrit, Fields: diffs, Added: added}
	var history []Change
	if st != nil {
		h, err := st.History(ctx, kind, name)
		if err != nil {
			return RowDiff{}, err
		}
		history = h
	}
	var operators []Change
	for _, c := range history {
		at, err := parseInstant(c.At)
		if err != nil {
			continue
		}
		if at.Before(from) || at.After(to) {
			continue
		}
		if !ignoreSet[c.Actor] {
			operators = append(operators, c)
		}
	}
	if len(operators) == 0 {
		for _, d := range diffs {
			rd.Unexplained = append(rd.Unexplained, d.Field)
		}
		return rd, nil
	}
	for _, d := range diffs {
		if !explainedBy(d.Field, operators) {
			rd.Unexplained = append(rd.Unexplained, d.Field)
		}
	}
	if len(rd.Unexplained) > 0 {
		return rd, nil
	}
	last := operators[len(operators)-1]
	rd.Class = CompareOperator
	rd.Actor = last.Actor
	rd.At = last.At
	rd.Reason = last.Reason
	return rd, nil
}

// explainedBy reports whether any operator entry changed field, by its own
// before and after maps: an add has no before and a remove no after.
func explainedBy(field string, operators []Change) bool {
	for _, c := range operators {
		switch c.Op {
		case OpAdd:
			if _, ok := c.After[field]; ok {
				return true
			}
		case OpRemove:
			if _, ok := c.Before[field]; ok {
				return true
			}
		default:
			bv, bok := c.Before[field]
			av, aok := c.After[field]
			if !bok || !aok || bv != av {
				return true
			}
		}
	}
	return false
}

// worse returns the more serious of two classes.
func worse(a, b string) string {
	rank := map[string]int{CompareSame: 0, CompareOperator: 1, CompareCrit: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Exit is the exit code the class carries: 0 SAME or OPERATOR, 1 CRIT.
func (r CompareResult) Exit() int {
	if r.Class == CompareCrit {
		return 1
	}
	return 0
}

func sortedKinds(m map[string]CmpKind) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedFields(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parseInstant reads an RFC 3339 UTC instant, "" as the zero time.
func parseInstant(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("instant %q is not RFC 3339", s)
	}
	return t.UTC(), nil
}

// --- snapshot files ---------------------------------------------------------

// WriteSnapshot writes dir/{kind}.json for every kind in snap and
// dir/when.json, the shape LoadSnapshot reads back. It replaces snap.sh.
func WriteSnapshot(dir string, snap CmpSnap) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("snapshot: make %s: %w", dir, err)
	}
	for _, kind := range sortedKinds(snap.Kinds) {
		raw, err := json.MarshalIndent(snap.Kinds[kind], "", "  ")
		if err != nil {
			return fmt.Errorf("snapshot: encode %s: %w", kind, err)
		}
		if err := os.WriteFile(filepath.Join(dir, kind+".json"), append(raw, '\n'), 0o644); err != nil {
			return fmt.Errorf("snapshot: write %s: %w", kind, err)
		}
	}
	when, err := json.MarshalIndent(whenFile{At: snap.At, Tool: snap.Tool}, "", "  ")
	if err != nil {
		return fmt.Errorf("snapshot: encode when: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "when.json"), append(when, '\n'), 0o644); err != nil {
		return fmt.Errorf("snapshot: write when.json: %w", err)
	}
	return nil
}

// LoadSnapshot reads a directory WriteSnapshot wrote: when.json and {kind}.json
// for each of the eight kinds the files name. A missing or unreadable file is
// an error the verb turns into exit 2.
func LoadSnapshot(dir string) (CmpSnap, error) {
	snap := CmpSnap{Kinds: map[string]CmpKind{}}
	raw, err := os.ReadFile(filepath.Join(dir, "when.json"))
	if err != nil {
		return CmpSnap{}, fmt.Errorf("snapshot: read when.json: %w", err)
	}
	var when whenFile
	if err := json.Unmarshal(raw, &when); err != nil {
		return CmpSnap{}, fmt.Errorf("snapshot: when.json: %w", err)
	}
	snap.At, snap.Tool = when.At, when.Tool
	entries, err := os.ReadDir(dir)
	if err != nil {
		return CmpSnap{}, fmt.Errorf("snapshot: read %s: %w", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		kind, ok := strings.CutSuffix(name, ".json")
		if !ok || kind == "when" {
			continue
		}
		if _, known := Lookup(kind); !known {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return CmpSnap{}, fmt.Errorf("snapshot: read %s: %w", name, err)
		}
		var ks CmpKind
		if err := json.Unmarshal(data, &ks); err != nil {
			return CmpSnap{}, fmt.Errorf("snapshot: %s: %w", name, err)
		}
		if ks.Kind == "" {
			ks.Kind = kind
		}
		snap.Kinds[kind] = ks
	}
	return snap, nil
}

// TakeSnapshot reads every kind's rows from st into a CmpSnap stamped at the
// given instant: the read-only core of `nova-config snapshot`.
func TakeSnapshot(ctx context.Context, st Store, at, tool string) (CmpSnap, error) {
	snap := CmpSnap{At: at, Tool: tool, Kinds: map[string]CmpKind{}}
	for _, k := range Kinds {
		rows, err := st.List(ctx, k.Name)
		if err != nil {
			return CmpSnap{}, fmt.Errorf("snapshot: list %s: %w", k.Name, err)
		}
		ks := CmpKind{Kind: k.Name}
		for _, r := range rows {
			ks.Rows = append(ks.Rows, CmpRow{Name: r.Name, Fields: r.Clone().Fields})
		}
		sort.Slice(ks.Rows, func(i, j int) bool { return ks.Rows[i].Name < ks.Rows[j].Name })
		snap.Kinds[k.Name] = ks
	}
	return snap, nil
}
