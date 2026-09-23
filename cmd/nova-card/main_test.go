package main

import (
	"bytes"
	"strings"
	"testing"
)

const testToken = "1.0123456789abcdef0123456789abcdef"

func noEnv(string) string { return "" }

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
		{"no config", []string{"s1/card-a/1"}, "s1 card-a 1 " + testToken + "\n", 1, "missing or bad NOVA_CARD_BENCH, NOVA_CARD_CLOCK, NOVA_CARD_HARNESS, NOVA_CARD_JOBS, NOVA_CARD_REDIS, NOVA_CARD_RESULTS"},
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
