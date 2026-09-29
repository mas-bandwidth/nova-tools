package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func (app *application) cmdBatch(args []string, stdout, stderr io.Writer) int {
	const verb = "batch"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	epoch := fs.Uint64("epoch", app.defaults.Epoch, "the epoch this write observed; it must equal the manifest's epoch")
	actor := fs.String("actor", app.defaults.Actor, "actor recorded with the change; it must equal the manifest's actor when the manifest names one")
	receipt := fs.Bool("receipt", app.receipts, "print the committed event ID, epoch and revision")
	_ = fs.Set("receipt", "true")
	if app.shared != nil && !app.receipts {
		_ = fs.Set("receipt", "false")
	}

	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one manifest: batch (<manifest-file> | - | '<json>')")
	}

	var raw []byte
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
	}

	manifest, err := ntable.ValidateBatchManifestRaw(raw)
	if err != nil {
		var limit *ntable.LimitError
		if errors.As(err, &limit) {
			what := limit.Error()
			if limit.Member != "" {
				what += fmt.Sprintf(" (member %q)", limit.Member)
			}
			return refused(stderr, verb, what+"; changed=no; run: nova-table batch -h")
		}
		return refuse(stderr, verb, fmt.Sprintf("invalid batch manifest: %v; changed=no; run: nova-table batch -h", err))
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
	if err != nil {
		return st.refusal(stderr, verb, err)
	}

	var delta ntable.BatchDelta
	if rcpt.BatchDelta != nil {
		delta = *rcpt.BatchDelta
	}
	fmt.Fprintf(stdout, "TABLE BATCH table=%s operation=%s epoch=%d before=%d after=%d outcome=%s selected=%d guards=%d changed=%d trips=%d\n",
		manifest.Table, field(delta.OperationID), rcpt.Epoch, rcpt.Before, rcpt.After, rcpt.Outcome,
		delta.SelectedCount, delta.GuardCount, delta.ChangedCount, trips.N())

	if *receipt {
		fmt.Fprintf(stdout, "TABLE RECEIPT event=%s epoch=%d before=%d after=%d outcome=%s\n",
			rcpt.ID, rcpt.Epoch, rcpt.Before, rcpt.After, rcpt.Outcome)
	}

	for _, m := range delta.Members {
		fmt.Fprintf(stdout, "MEMBER %s place=%s->%s score=%s->%s rev=%s->%s fields=%s\n",
			field(m.ID), placeOrDash(m.BeforePlace), placeOrDash(m.AfterPlace),
			scoreOrDash(m.BeforeScoreText), scoreOrDash(m.AfterScoreText), m.BeforeRev, m.AfterRev, fieldChanges(m))
	}
	return 0
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

// fieldChanges is the member's changed application fields as one JSON object,
// name to [before, after], null for absent: {"role":["x","y"],"k":[null,"v"]}.
// A field a set names with its present value, or an unset names when absent,
// changes nothing and is not listed.
func fieldChanges(m ntable.BatchMemberDelta) string {
	changes := map[string][2]*string{}
	for name, c := range m.Fields {
		before, after := c.Before, c.After
		if (before == nil && after == nil) || (before != nil && after != nil && *before == *after) {
			continue
		}
		changes[name] = [2]*string{before, after}
	}
	b, err := json.Marshal(changes)
	if err != nil {
		return "{}"
	}
	return string(b)
}
