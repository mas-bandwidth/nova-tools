package main

import (
	"context"
	"strings"
	"testing"
)

// TestCardMoveDispatch: the table moves own deal, work, land, cancel,
// expire, table, consumers and render; end and beat are theirs with --id,
// --ids or --as and the bench attempt form's with a label and --token; fsck
// is theirs with no --sprint.
func TestCardMoveDispatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"deal", "--to", "bench:b", "--n", "3"}, true},
		{[]string{"work", "--as", "friend:f", "--fill"}, true},
		{[]string{"end", "--id", "p~1", "--ok"}, true},
		{[]string{"end", "lbl", "--sprint", "s", "--token", "t"}, false},
		{[]string{"beat", "--as", "bench:b", "--id", "p~1"}, true},
		{[]string{"beat", "lbl", "--sprint", "s", "--token", "t"}, false},
		{[]string{"fsck"}, true},
		{[]string{"fsck", "--sprint", "s"}, false},
		{[]string{"push", "--sprint", "s"}, false},
		{[]string{"land", "--stream", "s", "--sha", "x"}, true},
		{[]string{"run", "--as", "friend:f", "--id", "p~1", "--model", "m", "--harness", "h"}, true},
		{[]string{"run", "lbl", "--sprint", "s"}, false},
		{[]string{"wait", "--as", "friend:f"}, true},
	} {
		if got := isCardMove(tc.args[0], tc.args[1:]); got != tc.want {
			t.Errorf("isCardMove(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestCardRunWaitUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name     string
		sub      string
		args     []string
		wantCode int
		wantSub  string
	}{
		{
			name:     "run missing as",
			sub:      "run",
			args:     []string{"--id", "c0~1", "--model", "m1", "--harness", "h1"},
			wantCode: 2,
			wantSub:  "run wants --as friend:<f> --id <copy> --model <m> --harness <h>",
		},
		{
			name:     "run bench as",
			sub:      "run",
			args:     []string{"--as", "bench:b1", "--id", "c0~1", "--model", "m1", "--harness", "h1"},
			wantCode: 2,
			wantSub:  "run wants --as friend:<f> --id <copy> --model <m> --harness <h>",
		},
		{
			name:     "run missing id",
			sub:      "run",
			args:     []string{"--as", "friend:worker-a", "--model", "m1", "--harness", "h1"},
			wantCode: 2,
			wantSub:  "run wants --as friend:<f> --id <copy> --model <m> --harness <h>",
		},
		{
			name:     "run missing model",
			sub:      "run",
			args:     []string{"--as", "friend:worker-a", "--id", "c0~1", "--harness", "h1"},
			wantCode: 2,
			wantSub:  "run wants --as friend:<f> --id <copy> --model <m> --harness <h>",
		},
		{
			name:     "run missing harness",
			sub:      "run",
			args:     []string{"--as", "friend:worker-a", "--id", "c0~1", "--model", "m1"},
			wantCode: 2,
			wantSub:  "run wants --as friend:<f> --id <copy> --model <m> --harness <h>",
		},
		{
			name:     "wait missing as",
			sub:      "wait",
			args:     []string{},
			wantCode: 2,
			wantSub:  "wait wants --as friend:<f> [--timeout <dur>]",
		},
		{
			name:     "wait bench as",
			sub:      "wait",
			args:     []string{"--as", "bench:b1"},
			wantCode: 2,
			wantSub:  "wait wants --as friend:<f> [--timeout <dur>]",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errOut strings.Builder
			code := runCardMove(ctx, tc.sub, tc.args, &out, &errOut)
			if code != tc.wantCode {
				t.Fatalf("code = %d, want %d; stderr=%q", code, tc.wantCode, errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.wantSub) {
				t.Fatalf("stderr = %q, want substring %q", errOut.String(), tc.wantSub)
			}
		})
	}
}

