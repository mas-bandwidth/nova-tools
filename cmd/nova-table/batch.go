package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// batchUsageDetails states the manifest grammar from SPEC-NOVA-TABLE's
// Batched member read and conditional write contract.
const batchUsageDetails = `manifest: one JSON object; required keys are schema (1), table, epoch,
expected_table_revision, operation_id and members. Epoch and revisions are
unsigned decimal strings. Optional keys: actor, props, prop_expect, prop_absent.
Each member names id and expect, with create, move, remove, set or unset for a
change; a member with only expect is a guard. A create expects absent=true.
Example manifest (demo has row build and column ready; revision 2 was observed):
{
  "schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"create-b1",
  "members":[{"id":"b1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":0}}]}
Read the current epoch/revision with nova-table show demo and existing members
with nova-table member read demo b1 before choosing expectations.
Use the same --redis or --seat for these reads and the batch.
Retry the same manifest with the same operation_id to replay its recorded
result; changing the manifest under that id is refused. --idem on individual
write verbs records receipt metadata only and does not deduplicate retries.`

func (app *application) cmdBatch(args []string, stdout, stderr io.Writer) int {
	const verb = "batch"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	epoch := fs.Uint64("epoch", app.defaults.Epoch, "the epoch this write observed; it must equal the manifest's epoch")
	actor := fs.String("actor", app.defaults.Actor, "actor recorded with the change; it must equal the manifest's actor when the manifest names one")
	receipt := fs.Bool("receipt", app.receipts, "print the committed event ID, epoch and revision")
	asJSON := fs.Bool("json", false, "print the receipt as one JSON object instead of the lines")
	// ignored: Set on a flag this function just defined, with a value its parser accepts
	_ = fs.Set("receipt", "true")
	if app.shared != nil && !app.receipts {
		// ignored: Set on a flag this function just defined, with a value its parser accepts
		_ = fs.Set("receipt", "false")
	}

	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one manifest: batch (<manifest-file> | - | '<json>'); run: nova-table help batch")
	}

	var raw []byte
	fromFile := false
	switch {
	case pos[0] == "-":
		in := app.in
		if in == nil {
			in = os.Stdin
		}
		var readErr error
		raw, readErr = io.ReadAll(in)
		if readErr != nil {
			return refuse(stderr, verb, fmt.Sprintf("cannot read the manifest from stdin: %v; changed=no; run: nova-table batch -h", readErr))
		}
	case strings.HasPrefix(strings.TrimSpace(pos[0]), "{"):
		raw = []byte(pos[0])
	default:
		content, readErr := os.ReadFile(pos[0])
		if readErr != nil {
			return refuse(stderr, verb, fmt.Sprintf("cannot read the manifest file %q: %v (a manifest is a file path, - for stdin, or JSON that starts with {); changed=no; run: nova-table batch -h", pos[0], readErr))
		}
		raw = content
		fromFile = true
	}

	manifest, err := ntable.ValidateBatchManifestRaw(raw)
	if err != nil {
		var (
			limit *ntable.LimitError
			rule  *ntable.RuleError
		)
		switch {
		case errors.As(err, &limit):
			what := limit.Error()
			if limit.Member != "" {
				what += fmt.Sprintf(" (member %q)", limit.Member)
			}
			return refused(stderr, verb, what+"; "+limit.Advice()+"; "+ntable.CheckedBeforeSending+"; code=LIMIT; changed=no; run: nova-table batch -h")
		case errors.As(err, &rule):
			// a manifest that reads as one and breaks a rule is refused, as the store refuses
			return refused(stderr, verb, fmt.Sprintf("%s; %s; code=%s; changed=no; run: nova-table batch -h", rule.Msg, ntable.CheckedBeforeSending, rule.Code))
		}
		// a manifest that cannot be read as one is a usage error
		return refuse(stderr, verb, fmt.Sprintf("invalid batch manifest: %v; %s; changed=no; run: nova-table batch -h", err, ntable.CheckedBeforeSending))
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
	// The manifest states its epoch and may state its actor. A flag given on
	// this command must agree with the manifest; a session default only fills
	// what the manifest leaves out.
	if epochSet && manifest.Epoch != fmt.Sprint(*epoch) {
		return refuse(stderr, verb, fmt.Sprintf("--epoch %d differs from the manifest's epoch %q; make them equal or drop --epoch; changed=no; run: nova-table batch -h", *epoch, manifest.Epoch))
	}
	if manifest.Actor == "" {
		manifest.Actor = *actor
	} else if actorSet && *actor != manifest.Actor {
		return refuse(stderr, verb, fmt.Sprintf("--actor %q differs from the manifest's actor %q; make them equal or drop --actor; changed=no; run: nova-table batch -h", *actor, manifest.Actor))
	}

	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()

	rcpt, err := ntable.ApplyBatch(ctx, c, *manifest)
	if errors.Is(err, ntable.ErrUnknownOutcome) {
		// the store did not confirm: the batch may or may not be applied. Send the same manifest again.
		text := err.Error()
		if i := strings.LastIndex(text, "; run: "); i >= 0 {
			text = text[:i]
		}
		next := "nova-table batch -h"
		if fromFile {
			next = "nova-table batch '" + strings.ReplaceAll(pos[0], "'", `'\''`) + "'"
		}
		return refuse(stderr, verb, text+"; run: "+next)
	}
	if err != nil {
		return st.refusal(stderr, verb, err)
	}

	var delta ntable.BatchDelta
	if rcpt.BatchDelta != nil {
		delta = *rcpt.BatchDelta
	}
	if *asJSON {
		return printJSON(stdout, stderr, verb, batchJSON(manifest.Table, rcpt, delta, trips.N()))
	}
	replay := "no"
	if rcpt.Replay {
		replay = "yes"
	}
	fmt.Fprintf(stdout, "TABLE BATCH table=%s operation=%s epoch=%d table_revision=%d->%d outcome=%s selected=%d guards=%d changed=%d replay=%s trips=%d\n",
		manifest.Table, field(delta.OperationID), rcpt.Epoch, rcpt.Before, rcpt.After, rcpt.Outcome,
		delta.SelectedCount, delta.GuardCount, delta.ChangedCount, replay, trips.N())

	if *receipt {
		fmt.Fprintf(stdout, "TABLE RECEIPT event=%s epoch=%d before=%d after=%d outcome=%s\n",
			rcpt.ID, rcpt.Epoch, rcpt.Before, rcpt.After, rcpt.Outcome)
	}

	for _, m := range delta.Members {
		fmt.Fprintf(stdout, "MEMBER %s place=%s->%s score=%s->%s member_revision=%s->%s fields=%s\n",
			field(m.ID), placeOrDash(m.BeforePlace), placeOrDash(m.AfterPlace),
			scoreOrDash(m.BeforeScoreText), scoreOrDash(m.AfterScoreText), m.BeforeRev, m.AfterRev, fieldChanges(m))
	}
	return 0
}

// batchJSON is the receipt as one object: revisions and scores are decimal
// strings, an absent place or score is null, fields are [before, after] pairs.
func batchJSON(table string, r ntable.Receipt, delta ntable.BatchDelta, trips int64) map[string]any {
	pair := func(before, after any) map[string]any { return map[string]any{"before": before, "after": after} }
	orNil := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	members := make([]map[string]any, 0, len(delta.Members))
	for _, m := range delta.Members {
		fields := changedFields(m)
		members = append(members, map[string]any{
			"id": m.ID, "place": pair(orNil(m.BeforePlace), orNil(m.AfterPlace)),
			"score":           pair(m.BeforeScoreText, m.AfterScoreText),
			"member_revision": pair(m.BeforeRev, m.AfterRev), "fields": fields,
		})
	}
	return map[string]any{
		"table": table, "operation_id": delta.OperationID, "epoch": strconv.FormatUint(r.Epoch, 10),
		"table_revision": pair(strconv.FormatUint(r.Before, 10), strconv.FormatUint(r.After, 10)),
		"outcome":        r.Outcome, "selected": delta.SelectedCount, "guards": delta.GuardCount, "changed": delta.ChangedCount,
		"event": r.ID, "replay": r.Replay, "trips": trips, "members": members,
	}
}

func placeOrDash(p string) string {
	if p == "" {
		return "-"
	}
	return p
}

// scoreOrDash is the score exactly as the store holds it, or - for none.
func scoreOrDash(v *string) string {
	if v == nil {
		return "-"
	}
	return *v
}

// changedFields is the member's changed application fields: name to [before,
// after], where a value is its string, null for absent, or {"bytes": n, "sha1":
// "..."} for a value too long for a receipt to record. A field whose two sides
// are equal strings, or both absent, changed nothing and is not listed. Two long
// sides are always listed with their digests: a digest identifies a value, it does
// not prove two values equal, so equality is decided from bytes only.
func changedFields(m ntable.BatchMemberDelta) map[string][2]any {
	side := func(text *string, n int, sha string) (any, string) {
		switch {
		case text != nil:
			return *text, "v:" + *text
		case n > 0:
			return map[string]any{"bytes": n, "sha1": sha}, fmt.Sprintf("l:%d:%s", n, sha)
		}
		return nil, "absent"
	}
	out := map[string][2]any{}
	for name, c := range m.Fields {
		before, bk := side(c.Before, c.BeforeBytes, c.BeforeSHA1)
		after, ak := side(c.After, c.AfterBytes, c.AfterSHA1)
		if bk == ak && !strings.HasPrefix(bk, "l:") {
			continue
		}
		out[name] = [2]any{before, after}
	}
	return out
}

// fieldChanges is changedFields as one JSON object on one line:
// {"role":["x","y"],"k":[null,"v"]}.
func fieldChanges(m ntable.BatchMemberDelta) string {
	b, err := json.Marshal(changedFields(m))
	if err != nil {
		return "{}"
	}
	return string(b)
}
