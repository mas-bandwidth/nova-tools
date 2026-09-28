package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// TestCostImportExitCodes is nova-tools #3159's exit table through run: 0, 2, 3, 4 and 6.
// Exit 3 includes a day whose accepted rows overflow int64 micro-dollars: nothing written.
// 7 and 8 need a failure inside the write and are TestCostImportWriteOutcomes' cases.
func TestCostImportExitCodes(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // the any-seat check: nothing under HOME is read
	t.Setenv("NOVA_SPRINT_REDIS_USER", "")
	mr := miniredis.RunT(t)
	fixtures := filepath.Join("..", "..", "internal", "nsprint", "cost", "testdata")
	noCost := filepath.Join(t.TempDir(), "no-cost.csv")
	if err := os.WriteFile(noCost, []byte("date,workspace,model\n2026-09-21,nova,claude-opus-5-5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ten permitted $1e12 rows: each cell is accepted, but the day's sum (1e19 micro-dollars)
	// overflows int64. Unchecked, it wrapped to -8446744073709551616 and reconciled.
	// The day is 2026-09-19, which no other case writes, so a write would add its own key.
	const overflowDay = "2026-09-19"
	overflow := filepath.Join(t.TempDir(), "overflow.csv")
	rows := "usage_date,workspace_name,model,cost_usd\n" + strings.Repeat(overflowDay+",nova,claude-opus-5-5,1000000000000\n", 10)
	if err := os.WriteFile(overflow, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	closed := miniredis.RunT(t)
	closedAddr := closed.Addr()
	closed.Close()

	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"imported", []string{"--provider", "anthropic", "--file", filepath.Join(fixtures, "anthropic.csv"), "--redis", mr.Addr()}, 0,
			"COST IMPORT DONE provider=anthropic days=2 written=2 same=0 repaired=0 recovered=0", ""},
		{"unknown provider", []string{"--provider", "aws", "--file", filepath.Join(fixtures, "anthropic.csv"), "--redis", mr.Addr()}, 2, "", "nova-sprint cost import: --provider"},
		{"no redis", []string{"--provider", "anthropic", "--file", filepath.Join(fixtures, "anthropic.csv")}, 2, "", "no --redis"},
		{"missing file", []string{"--provider", "anthropic", "--file", filepath.Join(t.TempDir(), "absent.csv"), "--redis", mr.Addr()}, 3, "", "cannot read the export"},
		{"no cost column", []string{"--provider", "anthropic", "--file", noCost, "--redis", mr.Addr()}, 3, "", "no cost column"},
		{"sum overflows", []string{"--provider", "anthropic", "--file", overflow, "--redis", mr.Addr()}, 3, "", "overflows int64"},
		{"bad total", []string{"--provider", "openrouter", "--file", filepath.Join(fixtures, "openrouter-bad-total.csv"), "--redis", mr.Addr()}, 4, "", "total row"},
		{"closed redis", []string{"--provider", "anthropic", "--file", filepath.Join(fixtures, "anthropic.csv"), "--redis", closedAddr}, 6, "", "nothing written"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(append([]string{"cost", "import"}, tc.args...), &out, &errOut)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout %s\nstderr %s", code, tc.code, out.String(), errOut.String())
			}
			if !strings.Contains(out.String(), tc.stdout) || !strings.Contains(errOut.String(), tc.stderr) {
				t.Fatalf("want stdout ~ %q and stderr ~ %q\nstdout %s\nstderr %s", tc.stdout, tc.stderr, out.String(), errOut.String())
			}
		})
	}
	if keys := mr.Keys(); len(keys) != 2+1 {
		t.Fatalf("only the one good import may write: keys %v", keys)
	}
	if mr.Exists("cost:anthropic:" + overflowDay) {
		t.Fatalf("the overflowing day was written: cost:anthropic:%s exists", overflowDay)
	}
	if members, err := mr.ZMembers("cost:idx"); err != nil {
		t.Fatalf("cost:idx: %v", err)
	} else {
		for _, m := range members {
			if m == "anthropic:"+overflowDay {
				t.Fatalf("the overflowing day was indexed: cost:idx %v", members)
			}
		}
	}
}
