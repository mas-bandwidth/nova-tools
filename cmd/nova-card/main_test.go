package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/launch"
)

const testToken = "1.0123456789abcdef0123456789abcdef"

func noEnv(string) string { return "" }

func TestConfigCarriesTheLauncherDeadline(t *testing.T) {
	deadline := time.UnixMilli(1_900_000_000_123)
	env := map[string]string{
		"NOVA_CARD_REDIS": "127.0.0.1:6379", "NOVA_CARD_BENCH": "bench-one",
		"NOVA_CARD_HARNESS": "/bin/true", "NOVA_CARD_JOBS": "/jobs",
		"NOVA_CARD_RESULTS": "/results", "NOVA_CARD_CLOCK": "45m",
		launch.LaunchDeadlineEnv: strconv.FormatInt(deadline.UnixMilli(), 10),
	}
	cfg, err := config(launch.Line{Sprint: "s", Label: "card", Attempt: 1}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.LaunchDeadline.Equal(deadline) {
		t.Fatalf("launch deadline = %s, want %s", cfg.LaunchDeadline, deadline)
	}
}

// TestLaunchLineMustNameTheCard: the stdin line is the launcher's, argv is
// what ps shows; a wrapper whose two disagree runs nothing, and no refusal
// prints the token.
func TestLaunchLineMustNameTheCard(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
		code  int
		want  string
	}{
		{"bare", nil, "", 2, "run: nova-card help"},
		{"no stdin", []string{"s1/card-a/1"}, "", 2, "no launch line"},
		{"other card", []string{"s1/card-b/1"}, "s1 card-a 1 " + testToken + "\n", 2, "names s1/card-a/1, not s1/card-b/1"},
		{"two lines", []string{"s1/card-a/1"}, "s1 card-a 1 " + testToken + "\ns1 card-a 1 " + testToken + "\n", 2, "more than one"},
		{"bad token", []string{"s1/card-a/1"}, "s1 card-a 1 2.0123456789abcdef0123456789abcdef\n", 2, "another attempt"},
		{"no config", []string{"s1/card-a/1"}, "s1 card-a 1 " + testToken + "\n", 1, "missing or bad HOME, NOVA_CARD_BENCH, NOVA_CARD_CLOCK, NOVA_CARD_DEADLINE, NOVA_CARD_HARNESS_BIN, NOVA_CARD_JOBS, NOVA_CARD_REDIS, NOVA_CARD_RESULTS, NOVA_CARD_TOKENS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(tc.args, strings.NewReader(tc.stdin), &out, &errb, noEnv)
			all := out.String() + errb.String()
			if code != tc.code || !strings.Contains(all, tc.want) {
				t.Fatalf("exit %d output %q; want exit %d containing %q", code, all, tc.code, tc.want)
			}
			if strings.Contains(all, "0123456789abcdef0123456789abcdef") {
				t.Fatalf("output carries the token: %q", all)
			}
		})
	}
}
