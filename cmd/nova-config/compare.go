// compare.go holds the snapshot and compare verbs: they read the store (never
// write) to name the recorded act behind a changed row, where the release
// executor's snapcmp.sh reverts on the first difference because it cannot know
// WHY a row changed (docs/SPEC-CONFIG.md, "History"). The judgment is compare's
// pure core in pkg/config/snapshotcmp.go; these verbs render it.

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// actorList is a repeatable string flag: --ignore-actor <name> may be given
// more than once.
type actorList struct{ vals []string }

func (a *actorList) String() string { return strings.Join(a.vals, ",") }
func (a *actorList) Set(v string) error {
	a.vals = append(a.vals, v)
	return nil
}

// runSnapshot writes {kind}.json for the eight kinds and when.json into dir,
// exactly as the release executor's snap.sh does: the read-only core of the
// verb is config.TakeSnapshot, the files config.WriteSnapshot.
func runSnapshot(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "snapshot"
	fs := verbflag.New(verb)
	c := seatStoreFlags(fs)
	out := fs.String("out", "", "the `dir` to write {kind}.json and when.json into")
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "snapshot takes no arguments")
	}
	var problems []string
	if *out == "" {
		problems = append(problems, "--out is required: the directory to write the snapshot into")
	}
	dsn, err := c.dsn(ctx, d)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = st.Close() }() // ignored: every file the verb writes is already read, so a close error changes nothing
	snap, err := config.TakeSnapshot(ctx, st, d.now().UTC().Format(time.RFC3339), version)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if err := config.WriteSnapshot(*out, snap); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if *asJSON {
		o := tool.Done().Fact("out", *out).Fact("kinds", len(snap.Kinds)).Fact("at", snap.At).Fact("tool", snap.Tool)
		o.Verb = verb
		return emit(stdout, o)
	}
	fmt.Fprintf(stdout, "CONFIG SNAPSHOT out=%s kinds=%d at=%s tool=%s\n", config.Value(*out), len(snap.Kinds), snap.At, snap.Tool)
	return 0
}

// runCompare judges the after snapshot against the before for every kind: for
// each row that differs it asks the store's history between the two when.json
// instants and names the act behind it (config.CompareSnapshots). It writes
// nothing and reverts nothing; the caller decides what a CRIT means.
func runCompare(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "compare"
	fs := verbflag.New(verb)
	c := seatStoreFlags(fs)
	before := fs.String("before", "", "the `dir` of the before snapshot")
	after := fs.String("after", "", "the `dir` of the after snapshot")
	ign := &actorList{}
	fs.Var(ign, "ignore-actor", "an actor whose history entries are the tool writing the store, not an operator act (repeatable); default: "+config.MigrationActor)
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return refuse(stderr, verb, "compare takes no arguments")
	}
	var problems []string
	if *before == "" {
		problems = append(problems, "--before is required: the directory of the before snapshot")
	}
	if *after == "" {
		problems = append(problems, "--after is required: the directory of the after snapshot")
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	bSnap, err := config.LoadSnapshot(*before)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	aSnap, err := config.LoadSnapshot(*after)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	// The store answers why a row changed: without it a difference has no
	// history and every verdict would be a sterile CRIT, so the verb refuses
	// a store it cannot open the way any other read does.
	dsn, err := c.dsn(ctx, d)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = st.Close() }() // ignored: the verdict the verb needs is already read, so a close error changes nothing
	res, err := config.CompareSnapshots(ctx, st, bSnap, aSnap, config.CompareOptions{IgnoreActors: ign.vals})
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if *asJSON {
		o := tool.Done().Fact("class", res.Class)
		o.Verb = verb
		for _, kd := range res.Kinds {
			o.Item("kind", "kind", kd.Kind, "class", kd.Class)
			for _, rd := range kd.Rows {
				item := []any{"kind", kd.Kind, "name", rd.Name, "class", rd.Class}
				if len(rd.Fields) > 0 {
					item = append(item, "fields", rd.Fields)
				}
				if rd.Removed {
					item = append(item, "removed", true)
				}
				if rd.Actor != "" {
					item = append(item, "actor", rd.Actor, "at", rd.At, "reason", tool.Text(rd.Reason))
				}
				if len(rd.Unexplained) > 0 {
					item = append(item, "unexplained", rd.Unexplained)
				}
				if len(rd.Added) > 0 {
					item = append(item, "added", rd.Added)
				}
				o.Item("row", item...)
			}
		}
		// A CRIT is exit 1 in both renderings (the one value, two renderings
		// rule): the JSON carries the same class and the same exit as the lines.
		if res.Class == config.CompareCrit {
			o.Status = tool.Failed
			o.Exit = 1
		}
		return emit(stdout, o)
	}
	compareLines(stdout, res)
	return res.Exit()
}

// compareLines renders the verdict: one line per row that differs (an
// OPERATOR-DIFF or OPERATOR-ADD names the act behind it, a CRIT-DIFF the
// unexplained fields), the added fields listed apart, then one COMPARE line per
// kind and a last COMPARE line naming the worst verdict (SAME, OPERATOR or
// CRIT). Each row's line is printed once.
func compareLines(stdout io.Writer, res config.CompareResult) {
	for _, kd := range res.Kinds {
		if len(kd.Rows) == 0 {
			fmt.Fprintf(stdout, "COMPARE %s %s\n", kd.Kind, config.Verdict(kd.Class))
			continue
		}
		for _, rd := range kd.Rows {
			removed := ""
			if rd.Removed {
				removed = " removed"
			}
			switch rd.Class {
			case config.CompareOperator:
				fmt.Fprintf(stdout, "OPERATOR-DIFF %s %s%s by %s at %s: %s (%s)\n",
					rd.Kind, rd.Name, removed, rd.Actor, rd.At, rd.Reason, diffSummary(rd.Fields))
			case config.CompareAdded:
				fmt.Fprintf(stdout, "OPERATOR-ADD %s %s by %s at %s: %s\n",
					rd.Kind, rd.Name, rd.Actor, rd.At, rd.Reason)
			case config.CompareCrit:
				if len(rd.Unexplained) > 0 {
					fmt.Fprintf(stdout, "CRIT-DIFF %s %s%s unexplained: %s\n", rd.Kind, rd.Name, removed, strings.Join(rd.Unexplained, ", "))
				} else {
					fmt.Fprintf(stdout, "CRIT-DIFF %s %s%s\n", rd.Kind, rd.Name, removed)
				}
			default:
				fmt.Fprintf(stdout, "SAME %s %s\n", rd.Kind, rd.Name)
			}
			if len(rd.Added) > 0 {
				fmt.Fprintf(stdout, "ADDED %s %s: %s\n", rd.Kind, rd.Name, strings.Join(rd.Added, ", "))
			}
		}
		fmt.Fprintf(stdout, "COMPARE %s %s\n", kd.Kind, config.Verdict(kd.Class))
	}
	fmt.Fprintf(stdout, "COMPARE %s\n", config.Verdict(res.Class))
}

// diffSummary is the differing fields of one row as `field before -> after`
// joined by commas.
func diffSummary(diffs []config.FieldDiff) string {
	parts := make([]string, 0, len(diffs))
	for _, df := range diffs {
		parts = append(parts, fmt.Sprintf("%s %s -> %s", df.Field, df.Before, df.After))
	}
	return strings.Join(parts, ", ")
}
