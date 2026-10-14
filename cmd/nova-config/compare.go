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
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// actorList is a repeatable string flag: --ignore-actor <name> may be given
// more than once.
type actorList struct{ vals []string }

func (a *actorList) String() string { return strings.Join(a.vals, ",") }
func (a *actorList) Get() any       { return a.vals }
func (a *actorList) Set(v string) error {
	a.vals = append(a.vals, v)
	return nil
}

// runSnapshot writes {kind}.json for the eight kinds and when.json into dir,
// exactly as the release executor's snap.sh does: the read-only core of the
// verb is config.TakeSnapshot, the files config.WriteSnapshot.
func runSnapshot(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	return compareTool(d).RunContext(ctx, append([]string{"snapshot"}, args...), os.Stdin, stdout, stderr)
}

// compareTool lets pkg/tool own both verbs' flags, help, parse and exit.
func compareTool(d deps) *tool.Tool {
	return &tool.Tool{
		Name:      toolName,
		What:      "configuration snapshots and recorded changes",
		How:       "snapshot captures the eight kinds; compare attributes changes to store history.",
		ExitTable: "0 same or operator, 1 critical difference, 2 could not run",
		Verbs: []tool.Verb{
			{Name: "snapshot", Usage: "snapshot --out <dir> [--pg <dsn> | --file <path>] [--json]", Effect: tool.LocalWrite,
				Detail: "Captures the eight configuration kinds and their capture time into a directory.\nexample: " + toolExamples["snapshot"], Flags: snapshotFlags,
				Run: func(c *tool.Call) *tool.Out { return tool.Exit(snapshotBody(c, d)) }},
			{Name: "compare", Usage: "compare --before <dir> --after <dir> [--ignore-actor <name>...] [--max <n>] [--json]", Effect: tool.Inspection,
				Detail: "Compares two captures and names recorded acts behind changed rows.\nexample: " + toolExamples["compare"], Flags: compareFlags,
				Run: func(c *tool.Call) *tool.Out { return tool.Exit(compareBody(c, d)) }},
		},
	}
}

func snapshotFlags(f *tool.Flags) {
	f.Prints()
	seatStoreFlags(f.FlagSet)
	f.String("out", "", "the `dir` to write {kind}.json and when.json into")
	f.Bool("json", false, "print the result as one JSON object instead of lines")
}

func compareFlags(f *tool.Flags) {
	f.Prints()
	seatStoreFlags(f.FlagSet)
	f.String("before", "", "the `dir` of the before snapshot")
	f.String("after", "", "the `dir` of the after snapshot")
	f.Int("max", bounded.Default, "maximum comparison rows to print; 0 prints all")
	f.Var(&actorList{}, "ignore-actor", "an actor whose history entries are the tool writing the store, not an operator act (repeatable); default: "+config.MigrationActor)
	f.Bool("json", false, "print the result as one JSON object instead of lines")
}

func snapshotBody(call *tool.Call, d deps) int {
	const verb = "snapshot"
	ctx, stdout, stderr := call.Ctx, call.Stdout, call.Stderr
	pg, file, seat := call.Str("pg"), call.Str("file"), call.Str("seat")
	store := conn{pg: &pg, file: &file, seat: &seat}
	out, asJSON := call.Str("out"), call.Bool("json")
	var problems []string
	if out == "" {
		problems = append(problems, "--out is required: the directory to write the snapshot into")
	}
	dsn, err := store.dsn(ctx, d)
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
	snap, err := config.TakeSnapshot(ctx, st, d.now().UTC().Format(time.RFC3339), buildinfo.Version(version))
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if err := config.WriteSnapshot(out, snap); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if asJSON {
		o := tool.Done().Fact("out", out).Fact("kinds", len(snap.Kinds)).Fact("at", snap.At).Fact("tool", snap.Tool)
		o.Verb = verb
		return emit(stdout, o)
	}
	fmt.Fprintf(stdout, "CONFIG SNAPSHOT out=%s kinds=%d at=%s tool=%s\n", config.Value(out), len(snap.Kinds), snap.At, snap.Tool)
	return 0
}

// runCompare judges the after snapshot against the before for every kind: for
// each row that differs it asks the store's history between the two when.json
// instants and names the act behind it (config.CompareSnapshots). It writes
// nothing and reverts nothing; the caller decides what a CRIT means.
func runCompare(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	return compareTool(d).RunContext(ctx, append([]string{"compare"}, args...), os.Stdin, stdout, stderr)
}

func compareBody(call *tool.Call, d deps) int {
	const verb = "compare"
	ctx, stdout, stderr := call.Ctx, call.Stdout, call.Stderr
	pg, file, seat := call.Str("pg"), call.Str("file"), call.Str("seat")
	store := conn{pg: &pg, file: &file, seat: &seat}
	before, after, max := call.Str("before"), call.Str("after"), call.Int("max")
	ign := call.Get("ignore-actor").([]string)
	asJSON := call.Bool("json")
	if max < 0 {
		return refuse(stderr, verb, "--max must be zero or more; run: "+toolName+" compare -h")
	}
	var problems []string
	if before == "" {
		problems = append(problems, "--before is required: the directory of the before snapshot")
	}
	if after == "" {
		problems = append(problems, "--after is required: the directory of the after snapshot")
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	bSnap, err := config.LoadSnapshot(before)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	aSnap, err := config.LoadSnapshot(after)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	// The store answers why a row changed: without it a difference has no
	// history and every verdict would be a sterile CRIT, so the verb refuses
	// a store it cannot open the way any other read does.
	dsn, err := store.dsn(ctx, d)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = st.Close() }() // ignored: the verdict the verb needs is already read, so a close error changes nothing
	res, err := config.CompareSnapshots(ctx, st, bSnap, aSnap, config.CompareOptions{IgnoreActors: ign})
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	res, shown, total := capCompareRows(res, max)
	if asJSON {
		o := tool.Done().Fact("class", res.Class).Fact("rows", total)
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
		if shown < total {
			o.More = append(o.More, tool.More{Kind: "row", Shown: shown, Total: total, Remedy: tool.MaxRemedy})
		}
		return emit(stdout, o)
	}
	compareLines(stdout, res, shown, total)
	return res.Exit()
}

// capCompareRows limits presentation only: each kind and the final result
// retain the verdict computed over every row (docs/STANDARD.md, section 2).
func capCompareRows(res config.CompareResult, max int) (config.CompareResult, int, int) {
	limited := res
	limited.Kinds = append([]config.KindDiff(nil), res.Kinds...)
	shown, total := 0, 0
	for i := range limited.Kinds {
		rows := limited.Kinds[i].Rows
		total += len(rows)
		keep := len(rows)
		if max > 0 && keep > max-shown {
			keep = max - shown
			if keep < 0 {
				keep = 0
			}
		}
		limited.Kinds[i].Rows = rows[:keep]
		shown += keep
	}
	return limited, shown, total
}

// compareLines renders the verdict: one line per row that differs (an
// OPERATOR-DIFF or OPERATOR-ADD names the act behind it, a CRIT-DIFF the
// unexplained fields), the added fields listed apart, then one COMPARE line per
// kind and a last COMPARE line naming the worst verdict (SAME, OPERATOR or
// CRIT). Each row's line is printed once.
func compareLines(stdout io.Writer, res config.CompareResult, shown, total int) {
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
	if shown < total {
		fmt.Fprintf(stdout, "COMPARE MORE kind=row shown=%d total=%d %s\n", shown, total, tool.MaxRemedy)
	}
	fmt.Fprintf(stdout, "COMPARE %s\n", config.Verdict(res.Class))
}

// diffSummary is the differing fields of one row as `field before -> after`
// joined by commas.
func diffSummary(diffs []config.FieldDiff) string {
	parts := make([]string, 0, len(diffs))
	for _, df := range diffs {
		after := df.After
		if df.AfterMissing {
			after = "<absent>"
		}
		parts = append(parts, fmt.Sprintf("%s %s -> %s", df.Field, df.Before, after))
	}
	return strings.Join(parts, ", ")
}
