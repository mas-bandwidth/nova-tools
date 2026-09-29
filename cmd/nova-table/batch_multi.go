package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func (app *application) cmdBatchMulti(raw []byte, source string, fromFile bool, fs *flag.FlagSet, addr string, epoch uint64, actor string, receipt, asJSON bool, stdout, stderr io.Writer) int {
	const verb = "batch"
	manifest, err := ntable.ValidateMultiBatchManifestRaw(raw)
	if err != nil {
		var limit *ntable.LimitError
		var rule *ntable.RuleError
		switch {
		case errors.As(err, &limit):
			what := limit.Error()
			if limit.Member != "" {
				what += fmt.Sprintf(" (member %q)", limit.Member)
			}
			return refused(stderr, verb, what+"; "+limit.Advice()+"; "+ntable.CheckedBeforeSending+"; code=LIMIT; changed=no; run: nova-table batch -h")
		case errors.As(err, &rule):
			return refused(stderr, verb, fmt.Sprintf("%s; %s; code=%s; changed=no; run: nova-table batch -h", rule.Msg, ntable.CheckedBeforeSending, rule.Code))
		default:
			return refuse(stderr, verb, fmt.Sprintf("invalid batch manifest: %v; %s; changed=no; run: nova-table batch -h", err, ntable.CheckedBeforeSending))
		}
	}

	actorSet, epochSet := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "actor":
			actorSet = true
		case "epoch":
			epochSet = true
		}
	})
	if epochSet {
		want := strconv.FormatUint(epoch, 10)
		for _, table := range manifest.Tables {
			if table.Epoch != want {
				return refuse(stderr, verb, fmt.Sprintf("--epoch %d differs from table %q's epoch %q; make them equal or drop --epoch; %s; changed=no; run: nova-table batch -h", epoch, table.Name, table.Epoch, ntable.CheckedBeforeSending))
			}
		}
	}
	if manifest.Actor == "" {
		manifest.Actor = actor
	} else if actorSet && actor != manifest.Actor {
		return refuse(stderr, verb, fmt.Sprintf("--actor %q differs from the manifest's actor %q; make them equal or drop --actor; %s; changed=no; run: nova-table batch -h", actor, manifest.Actor, ntable.CheckedBeforeSending))
	}

	ctx := context.Background()
	st, c, code := app.client(ctx, verb, addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	rcpt, err := ntable.ApplyMultiBatch(ctx, c, *manifest)
	if errors.Is(err, ntable.ErrUnknownOutcome) {
		text := err.Error()
		if i := strings.LastIndex(text, "; run: "); i >= 0 {
			text = text[:i]
		}
		next := "nova-table batch -h"
		if fromFile {
			next = "nova-table batch '" + strings.ReplaceAll(source, "'", `'\''`) + "'"
		}
		return refuse(stderr, verb, text+fmt.Sprintf("; outcome unknown; retry the identical manifest with scope %q and operation_id %q; run: %s", manifest.Scope, manifest.OperationID, next))
	}
	if err != nil {
		return st.refusal(stderr, verb, err)
	}

	var delta ntable.MultiBatchDelta
	if rcpt.Delta != nil {
		delta = *rcpt.Delta
	}
	if asJSON {
		return printJSON(stdout, stderr, verb, multiBatchJSON(delta, rcpt, trips.N()))
	}
	replay := "no"
	if rcpt.Replay {
		replay = "yes"
	}
	fmt.Fprintf(stdout, "TABLE BATCH scope=%s operation=%s outcome=%s selected=%d guards=%d changed=%d replay=%s trips=%d\n",
		field(delta.Scope), field(delta.OperationID), delta.Outcome, delta.SelectedCount, delta.GuardCount, delta.ChangedCount, replay, trips.N())
	if receipt {
		fmt.Fprintf(stdout, "TABLE RECEIPT event=%s scope=%s operation=%s outcome=%s replay=%s\n", rcpt.ID, field(delta.Scope), field(delta.OperationID), delta.Outcome, replay)
	}
	for _, table := range delta.Tables {
		fmt.Fprintf(stdout, "TABLE %s epoch=%s table_revision=%s->%s\n", field(table.Name), table.Epoch, table.RevBefore, table.RevAfter)
	}
	for _, m := range delta.Members {
		fmt.Fprintf(stdout, "MEMBER %s record_table=%s member_revision=%s->%s fields=%s\n",
			field(m.ID), field(m.RecordTable), m.BeforeRev, m.AfterRev, fieldChanges(ntable.BatchMemberDelta{Fields: m.Fields}))
		for _, p := range m.Placements {
			fmt.Fprintf(stdout, "PLACEMENT %s table=%s place=%s->%s score=%s->%s\n",
				field(m.ID), field(p.Table), placeOrDash(p.BeforePlace), placeOrDash(p.AfterPlace), scoreOrDash(p.BeforeScoreText), scoreOrDash(p.AfterScoreText))
		}
	}
	return 0
}

func multiBatchJSON(delta ntable.MultiBatchDelta, receipt ntable.MultiBatchReceipt, trips int64) map[string]any {
	tables := make([]map[string]any, 0, len(delta.Tables))
	for _, t := range delta.Tables {
		tables = append(tables, map[string]any{
			"name": t.Name, "epoch": t.Epoch,
			"table_revision": map[string]string{"before": t.RevBefore, "after": t.RevAfter},
		})
	}
	members := make([]map[string]any, 0, len(delta.Members))
	for _, m := range delta.Members {
		placements := make([]map[string]any, 0, len(m.Placements))
		for _, p := range m.Placements {
			placements = append(placements, map[string]any{
				"table": p.Table,
				"place": map[string]any{"before": orNilString(p.BeforePlace), "after": orNilString(p.AfterPlace)},
				"score": map[string]any{"before": p.BeforeScoreText, "after": p.AfterScoreText},
			})
		}
		members = append(members, map[string]any{
			"id": m.ID, "record_table": m.RecordTable,
			"member_revision": map[string]string{"before": m.BeforeRev, "after": m.AfterRev},
			"fields":          changedFields(ntable.BatchMemberDelta{Fields: m.Fields}),
			"fields_set":      m.FieldsSet, "fields_unset": m.FieldsUnset,
			"placements": placements,
		})
	}
	return map[string]any{
		"schema": delta.Schema, "scope": delta.Scope, "operation_id": delta.OperationID,
		"digest": delta.Digest, "actor": delta.Actor, "outcome": delta.Outcome,
		"event": receipt.ID, "replay": receipt.Replay, "trips": trips,
		"selected": delta.SelectedCount, "guards": delta.GuardCount, "changed": delta.ChangedCount,
		"tables": tables, "members": members,
	}
}

func orNilString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
