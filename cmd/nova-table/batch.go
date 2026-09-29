package main

import (
	"context"
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
	epoch := fs.Uint64("epoch", app.defaults.Epoch, "the epoch this write observed (default 0)")
	actor := fs.String("actor", app.defaults.Actor, "actor recorded with the change")
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
		return refuse(stderr, verb, "wants one manifest file or JSON string: batch <manifest>")
	}

	var raw []byte
	if pos[0] == "-" {
		in := app.in
		if in == nil {
			in = os.Stdin
		}
		var readErr error
		raw, readErr = io.ReadAll(in)
		if readErr != nil {
			return refuse(stderr, verb, fmt.Sprintf("read stdin: %v", readErr))
		}
	} else if !strings.HasPrefix(strings.TrimSpace(pos[0]), "{") {
		content, readErr := os.ReadFile(pos[0])
		if readErr == nil {
			raw = content
		} else {
			raw = []byte(pos[0])
		}
	} else {
		raw = []byte(pos[0])
	}

	manifest, err := ntable.ValidateBatchManifestRaw(raw)
	if err != nil {
		return refuse(stderr, verb, fmt.Sprintf("invalid batch manifest: %v", err))
	}

	actorSet := false
	epochSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "actor" {
			actorSet = true
		}
		if f.Name == "epoch" {
			epochSet = true
		}
	})

	if (actorSet || *actor != "") && manifest.Actor == "" {
		manifest.Actor = *actor
	}
	if (epochSet || app.defaults.Epoch != 0) && manifest.Epoch == "" {
		manifest.Epoch = fmt.Sprint(*epoch)
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

	operationID := ""
	if rcpt.BatchDelta != nil {
		operationID = rcpt.BatchDelta.OperationID
	}
	fmt.Fprintf(stdout, "TABLE BATCH table=%s operation=%s epoch=%d before=%d after=%d outcome=%s trips=%d\n",
		manifest.Table, operationID, rcpt.Epoch, rcpt.Before, rcpt.After, rcpt.Outcome, trips.N())

	if *receipt {
		fmt.Fprintf(stdout, "TABLE RECEIPT event=%s epoch=%d before=%d after=%d outcome=%s\n",
			rcpt.ID, rcpt.Epoch, rcpt.Before, rcpt.After, rcpt.Outcome)
	}

	if rcpt.BatchDelta != nil {
		for _, m := range rcpt.BatchDelta.Members {
			bp := m.BeforePlace
			if bp == "" {
				bp = "-"
			}
			ap := m.AfterPlace
			if ap == "" {
				ap = "-"
			}
			fmt.Fprintf(stdout, "MEMBER %s place=%s->%s rev=%s->%s\n", m.ID, bp, ap, m.BeforeRev, m.AfterRev)
		}
	}
	return 0
}
