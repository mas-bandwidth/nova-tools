//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package main

import (
	"strconv"
	"strings"
	"testing"
)

// SLOW: 5.2 s on hetzner at dev 64b9bec48, over the five-second line.
// TestL17b is control L17b (nova-tools#3139 rev 7 section 11, 7.7): every
// outcome of `why` (both forms) and `land status [<unit>]` exits with exactly
// its code: 0 printed, 1 no record, 2 refused or usage, 6 no Redis.
func TestL17b(t *testing.T) {
	t.Parallel()

	mr := l17Redis(t)
	addr := mr.Addr()
	now := strconv.FormatInt(1790000000, 10)
	dead := "127.0.0.1:1"
	cases := []struct {
		name string
		args []string
		code int
		out  string // a substring of stdout+stderr
	}{
		{"why unit", []string{"why", "--ids", l17Unit, "--redis", addr, "--sprint", l17Sprint, "--now", now}, 0, "ci OK@4139b79f"},
		{"why pr", []string{"why", "--ref", "nova-tools#3200", "--redis", addr, "--sprint", l17Sprint, "--now", now}, 0, "unit " + l17Unit + " nova-tools#3200"},
		{"why unknown unit", []string{"why", "--ids", "gh/mas-bandwidth/nova-tools/9999", "--redis", addr, "--sprint", l17Sprint}, 1, "MISSING s:l17:u:gh/mas-bandwidth/nova-tools/9999"},
		{"why unknown pr", []string{"why", "--ref", "nova-tools#9999", "--redis", addr, "--sprint", l17Sprint}, 1, "MISSING s:l17:prunit:nova-tools:9999"},
		{"why no arg", []string{"why", "--redis", addr, "--sprint", l17Sprint}, 2, "REFUSED"},
		{"why two args", []string{"why", "--ids", l17Unit, "--ref", "nova-tools#3200", "--redis", addr, "--sprint", l17Sprint}, 2, "REFUSED"},
		{"why bare number", []string{"why", "--ids", "3200", "--redis", addr, "--sprint", l17Sprint}, 2, "REFUSED"},
		{"why no sprint", []string{"why", "--ids", l17Unit, "--redis", addr}, 2, "REFUSED"},
		{"why dead redis", []string{"why", "--ids", l17Unit, "--redis", dead, "--sprint", l17Sprint}, 6, ""},
		{"status", []string{"land", "status", "--redis", addr, "--sprint", l17Sprint, "--now", now}, 0, "units 1: reading 1"},
		{"status unit", []string{"land", "status", "--ids", l17Unit, "--redis", addr, "--sprint", l17Sprint, "--now", now}, 0, "state reading"},
		{"status unknown unit", []string{"land", "status", "--ids", "gh/mas-bandwidth/nova-tools/9999", "--redis", addr, "--sprint", l17Sprint}, 1, "MISSING s:l17:u:gh/mas-bandwidth/nova-tools/9999"},
		{"status two units", []string{"land", "status", "--ids", l17Unit, l17Unit, "--redis", addr, "--sprint", l17Sprint}, 2, "REFUSED"},
		{"status no sprint", []string{"land", "status", "--redis", addr}, 2, "REFUSED"},
		{"status dead redis", []string{"land", "status", "--redis", dead, "--sprint", l17Sprint}, 6, ""},
		{"status unit dead redis", []string{"land", "status", "--ids", l17Unit, "--redis", dead, "--sprint", l17Sprint}, 6, ""},
	}
	for _, tc := range cases {
		code, stdout, stderr := runSprint(tc.args...)
		if code != tc.code {
			t.Errorf("%s: exit %d, want %d\nstdout %s\nstderr %s", tc.name, code, tc.code, stdout, stderr)
			continue
		}
		if tc.out != "" && !strings.Contains(stdout+stderr, tc.out) {
			t.Errorf("%s: want %q in\nstdout %s\nstderr %s", tc.name, tc.out, stdout, stderr)
		}
	}
}
