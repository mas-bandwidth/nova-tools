package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

func cardRunEnv(extra map[string]string) func(string) string {
	env := map[string]string{
		"HOME": "/home/bench", "NOVA_CARD_BENCH": "bench-a", "NOVA_CARD_DEADLINE": "20m",
		"NOVA_CARD_HARNESS_BIN": "/opt/harness/opencode", "NOVA_CARD_TOKENS": "200000", "NOVA_CARD_REDIS": "127.0.0.1:1",
		"NOVA_CARD_OUT": "/jobs/s/l/1/out", "NOVA_CARD_JOB": "/jobs/s/l/1",
	}
	for k, v := range extra {
		env[k] = v
	}
	return func(k string) string { return env[k] }
}

func TestCardRunRefusesABadArgv(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"no flags", nil, nil, "wants --sprint, --ids <label> and --attempt (a positive integer); usage: nova-sprint card run --sprint <S>"},
		{"attempt zero", []string{"--sprint", "s", "--ids", "l", "--attempt", "0"}, nil, "wants --sprint, --ids <label> and --attempt (a positive integer)"},
		{"positional", []string{"--sprint", "s", "--ids", "l", "--attempt", "1", "extra"}, nil, "takes flags, not positional arguments"},
		{"unknown flag", []string{"--sprint", "s", "--ids", "l", "--attempt", "1", "--model", "x"}, nil, "--model is not a flag of nova-sprint card run"},
		{"no card.env", []string{"--sprint", "s", "--ids", "l", "--attempt", "1"},
			map[string]string{"NOVA_CARD_HARNESS_BIN": "", "NOVA_CARD_TOKENS": "", "NOVA_CARD_REDIS": "", "NOVA_CARD_OUT": "", "NOVA_CARD_JOB": ""},
			"missing NOVA_CARD_HARNESS_BIN, NOVA_CARD_TOKENS, NOVA_CARD_REDIS (or --redis), NOVA_CARD_OUT (or --out), NOVA_CARD_JOB (or --job)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := runCardRunEnv(context.Background(), tc.args, &out, &errOut, cardRunEnv(tc.env))
			if code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("code %d stdout %q stderr %q; want 1, nothing, %q", code, out.String(), errOut.String(), tc.want)
			}
		})
	}
	// The verb is reachable through card: `nova-sprint card run` with no flags is the same refusal.
	var out, errOut bytes.Buffer
	if code := runCard(context.Background(), []string{"run"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "nova-sprint card run: ") {
		t.Fatalf("card run: code %d stderr %q", code, errOut.String())
	}
}

func TestCardRunRefusesARedisItCannotReach(t *testing.T) {
	t.Parallel()

	// Open sends nothing (#3277): the card read is the first command, after
	// the out dir, so the dirs are the test's own.
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	code := runCardRunEnv(context.Background(), []string{"--sprint", "s", "--ids", "l", "--attempt", "1"}, &out, &errOut,
		cardRunEnv(map[string]string{"NOVA_CARD_OUT": dir + "/out", "NOVA_CARD_JOB": dir + "/job", "HOME": dir}))
	if code != card.RunExitRefused || !strings.HasPrefix(out.String(), `REFUSED card run s/l/1 code=2 why="redis: `) {
		t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errOut.String())
	}
}
