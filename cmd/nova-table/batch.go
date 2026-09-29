package main

import (
	"context"
	"encoding/json"
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
	write, _ := app.writeFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one manifest file or JSON string: batch <manifest>")
	}
	raw := []byte(pos[0])
	if !strings.HasPrefix(strings.TrimSpace(pos[0]), "{") {
		content, err := os.ReadFile(pos[0])
		if err == nil {
			raw = content
		}
	}
	var manifest ntable.BatchManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return refuse(stderr, verb, fmt.Sprintf("invalid batch manifest: %v", err))
	}
	if write.Actor != "" && manifest.Actor == "" {
		manifest.Actor = write.Actor
	}
	if f := fs.Lookup("epoch"); f != nil && f.Value.String() != "0" && manifest.Epoch == "" {
		manifest.Epoch = fmt.Sprint(write.Epoch)
	}

	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()

	rcpt, err := ntable.ApplyBatch(ctx, c, manifest)
	if err != nil {
		return st.refusal(stderr, verb, err)
	}

	operationID := ""
	if rcpt.BatchDelta != nil {
		operationID = rcpt.BatchDelta.OperationID
	}
	fmt.Fprintf(stdout, "TABLE BATCH table=%s operation=%s epoch=%d before=%d after=%d outcome=%s trips=%d\n",
		manifest.Table, operationID, rcpt.Epoch, rcpt.Before, rcpt.After, rcpt.Outcome, trips.N())
	printReceipt := true
	for _, a := range args {
		if a == "--receipt=false" || a == "-receipt=false" {
			printReceipt = false
			break
		}
	}
	if printReceipt {
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
