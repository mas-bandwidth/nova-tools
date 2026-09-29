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
		{[]string{"tell", "--id", "p~1", "--text", "msg"}, true},
		{[]string{"report", "--id", "p~1"}, true},
		{[]string{"ls", "--as", "friend:f", "--live"}, true},
		{[]string{"ls", "--unplaced"}, false},
	} {
		if got := isCardMove(tc.args[0], tc.args[1:]); got != tc.want {
			t.Errorf("isCardMove(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestCardTellReportLsUsage(t *testing.T) {
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
			name:     "tell missing id",
			sub:      "tell",
			args:     []string{"--text", "hello"},
			wantCode: 2,
			wantSub:  "tell wants --id <copy> and --text <message>",
		},
		{
			name:     "tell missing text",
			sub:      "tell",
			args:     []string{"--id", "c0~1"},
			wantCode: 2,
			wantSub:  "tell wants --id <copy> and --text <message>",
		},
		{
			name:     "report missing id",
			sub:      "report",
			args:     []string{},
			wantCode: 2,
			wantSub:  "report wants --id <copy>",
		},
		{
			name:     "ls missing as",
			sub:      "ls",
			args:     []string{"--live"},
			wantCode: 2,
			wantSub:  "ls wants --as <consumer> [--live]",
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

