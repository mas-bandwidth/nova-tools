//go:build functional

package main

import (
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// The remedies FG-B prints are run through the binary against a throwaway
// store: a next verb that exists, spelled so it parses.

func TestFGBHoldReleaseRemediesRun(t *testing.T) {
	t.Parallel()
	addr := rowanStore(t)
	code, out, errOut := runSprint("hold", "release", "--as", "rowan", "--sprint", "audit", "--redis", addr,
		"nova-tools#9", "stella", "--head", "0123456789abcdef0123456789abcdef01234567", "--evidence", "evidence-1")
	want := "REFUSED no-unit s:audit:prunit:nova-tools:9 unit=nova-tools#9 sprint=audit holder=stella: nothing written; run: nova-sprint why nova-tools#9 --sprint audit --redis " + addr + ", or nova-sprint hold show --sprint audit --redis " + addr + " nova-tools#9\n"
	if code != 2 || out != want || errOut != "" {
		t.Fatalf("no-unit release: exit %d out %q err %q\nwant %q", code, out, errOut, want)
	}
	for _, remedy := range [][]string{
		{"why", "nova-tools#9", "--sprint", "audit", "--redis", addr},
		{"hold", "show", "--sprint", "audit", "--redis", addr, "nova-tools#9"},
	} {
		code, out, errOut := runSprint(remedy...)
		all := out + errOut
		if strings.Contains(all, "not defined") || strings.Contains(all, "unknown subverb") || strings.Contains(all, "usage:") {
			t.Errorf("remedy nova-sprint %s does not run: exit %d %q", strings.Join(remedy, " "), code, all)
		}
		if !strings.Contains(all, "nova-tools#9") {
			t.Errorf("remedy nova-sprint %s does not name the unit: exit %d %q", strings.Join(remedy, " "), code, all)
		}
	}
}

func TestFGBTaskDoneRemediesRun(t *testing.T) {
	t.Parallel()
	addr := rowanStore(t)
	for _, remedy := range [][]string{
		{"task", "list", "--redis", addr, "--sprint", "audit"},
		{"ready", "--why", "ghost", "--redis", addr, "--sprint", "audit"},
	} {
		code, out, errOut := runSprint(remedy...)
		all := out + errOut
		if strings.Contains(all, "not defined") || strings.Contains(all, "unknown subverb") {
			t.Errorf("remedy nova-sprint %s does not run: exit %d %q", strings.Join(remedy, " "), code, all)
		}
	}
}

func TestFGBHoldShowNamesAMissingUnitRecord(t *testing.T) {
	t.Parallel()
	addr := rowanStore(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := c.Set(t.Context(), "s:audit:prunit:nova-tools:7", "nova-tools#7", 0).Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runSprint("hold", "show", "--redis", addr, "--sprint", "audit", "nova-tools#7")
	if code != 1 || !strings.HasPrefix(out, "HOLDS nova-tools#7 unit=nova-tools#7 MISSING key=s:audit:u:nova-tools#7: unit record missing") || strings.Contains(out, "<nil>") || errOut != "" {
		t.Fatalf("exit %d out %q err %q", code, out, errOut)
	}
}
