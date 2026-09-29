//go:build functional

package main

import (
	"fmt"
	"strings"
	"testing"
)

func countMatchingLines(output, prefix string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}

func TestMaxFlagList(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)

	// Create 25 tables: t01 to t25
	for i := 1; i <= 25; i++ {
		tname := fmt.Sprintf("tbl%02d", i)
		code, _, errout := runTable(at(addr, "create", tname, "--columns", "c1")...)
		if code != 0 {
			t.Fatalf("create %s failed: %d %q", tname, code, errout)
		}
	}

	// 1. Default max: should show 20 tables and summary line "... and 5 more (use --max 0 to see all)"
	code, out, errout := runTable(at(addr, "list")...)
	if code != 0 || errout != "" {
		t.Fatalf("list default max: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE table="); n != 20 {
		t.Fatalf("list default max: got %d table lines, want 20\n%s", n, out)
	}
	if !strings.Contains(out, "... and 5 more (use --max 0 to see all)\n") {
		t.Fatalf("list default max missing summary line:\n%s", out)
	}
	if !strings.Contains(out, "TABLE LIST tables=25") {
		t.Fatalf("list default max missing total count:\n%s", out)
	}

	// 2. Custom max (--max 5): should show 5 tables and "... and 20 more (use --max 0 to see all)"
	code, out, errout = runTable(at(addr, "list", "--max", "5")...)
	if code != 0 || errout != "" {
		t.Fatalf("list --max 5: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE table="); n != 5 {
		t.Fatalf("list --max 5: got %d table lines, want 5\n%s", n, out)
	}
	if !strings.Contains(out, "... and 20 more (use --max 0 to see all)\n") {
		t.Fatalf("list --max 5 missing summary line:\n%s", out)
	}

	// 3. Custom max larger than total (--max 30): should show all 25 tables, no summary line
	code, out, errout = runTable(at(addr, "list", "--max", "30")...)
	if code != 0 || errout != "" {
		t.Fatalf("list --max 30: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE table="); n != 25 {
		t.Fatalf("list --max 30: got %d table lines, want 25\n%s", n, out)
	}
	if strings.Contains(out, "... and") {
		t.Fatalf("list --max 30 should have no summary line:\n%s", out)
	}

	// 4. Max 0 (--max 0): should show all 25 tables, no summary line
	code, out, errout = runTable(at(addr, "list", "--max", "0")...)
	if code != 0 || errout != "" {
		t.Fatalf("list --max 0: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE table="); n != 25 {
		t.Fatalf("list --max 0: got %d table lines, want 25\n%s", n, out)
	}
	if strings.Contains(out, "... and") {
		t.Fatalf("list --max 0 should have no summary line:\n%s", out)
	}
}

func TestMaxFlagShow(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)

	// Create table and add 25 rows: r01 to r25
	code, _, errout := runTable(at(addr, "create", "showtable", "--columns", "c1")...)
	if code != 0 {
		t.Fatalf("create failed: %d %q", code, errout)
	}
	rowArgs := []string{"row", "add", "showtable"}
	for i := 1; i <= 25; i++ {
		rowArgs = append(rowArgs, fmt.Sprintf("r%02d", i))
	}
	code, _, errout = runTable(at(addr, rowArgs...)...)
	if code != 0 {
		t.Fatalf("row add failed: %d %q", code, errout)
	}

	// 1. Default max: should show 20 rows and summary line "... and 5 more (use --max 0 to see all)"
	code, out, errout := runTable(at(addr, "show", "showtable")...)
	if code != 0 || errout != "" {
		t.Fatalf("show default max: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE ROW "); n != 20 {
		t.Fatalf("show default max: got %d row lines, want 20\n%s", n, out)
	}
	if !strings.Contains(out, "... and 5 more (use --max 0 to see all)\n") {
		t.Fatalf("show default max missing summary line:\n%s", out)
	}
	if !strings.Contains(out, "rows=25") {
		t.Fatalf("show default max missing total rows count in header:\n%s", out)
	}

	// 2. Custom max (--max 5): should show 5 rows and "... and 20 more (use --max 0 to see all)"
	code, out, errout = runTable(at(addr, "show", "showtable", "--max", "5")...)
	if code != 0 || errout != "" {
		t.Fatalf("show --max 5: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE ROW "); n != 5 {
		t.Fatalf("show --max 5: got %d row lines, want 5\n%s", n, out)
	}
	if !strings.Contains(out, "... and 20 more (use --max 0 to see all)\n") {
		t.Fatalf("show --max 5 missing summary line:\n%s", out)
	}

	// 3. Custom max larger than total (--max 30): should show all 25 rows, no summary line
	code, out, errout = runTable(at(addr, "show", "showtable", "--max", "30")...)
	if code != 0 || errout != "" {
		t.Fatalf("show --max 30: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE ROW "); n != 25 {
		t.Fatalf("show --max 30: got %d row lines, want 25\n%s", n, out)
	}
	if strings.Contains(out, "... and") {
		t.Fatalf("show --max 30 should have no summary line:\n%s", out)
	}

	// 4. Max 0 (--max 0): should show all 25 rows, no summary line
	code, out, errout = runTable(at(addr, "show", "showtable", "--max", "0")...)
	if code != 0 || errout != "" {
		t.Fatalf("show --max 0: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE ROW "); n != 25 {
		t.Fatalf("show --max 0: got %d row lines, want 25\n%s", n, out)
	}
	if strings.Contains(out, "... and") {
		t.Fatalf("show --max 0 should have no summary line:\n%s", out)
	}
}

func TestMaxFlagCellMembers(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)

	// Create table, add a row, and add 25 members to cell: m01 to m25
	code, _, errout := runTable(at(addr, "create", "celltable", "--columns", "c1")...)
	if code != 0 {
		t.Fatalf("create failed: %d %q", code, errout)
	}
	code, _, errout = runTable(at(addr, "row", "add", "celltable", "r1")...)
	if code != 0 {
		t.Fatalf("row add failed: %d %q", code, errout)
	}
	memberArgs := []string{"cell", "add", "celltable", "r1", "c1"}
	for i := 1; i <= 25; i++ {
		memberArgs = append(memberArgs, fmt.Sprintf("m%02d", i))
	}
	code, _, errout = runTable(at(addr, memberArgs...)...)
	if code != 0 {
		t.Fatalf("cell add failed: %d %q", code, errout)
	}

	// 1. Default max: should show 20 members and summary line "... and 5 more (use --max 0 to see all)"
	code, out, errout := runTable(at(addr, "cell", "members", "celltable", "r1", "c1")...)
	if code != 0 || errout != "" {
		t.Fatalf("cell members default max: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE MEMBER "); n != 20 {
		t.Fatalf("cell members default max: got %d member lines, want 20\n%s", n, out)
	}
	if !strings.Contains(out, "... and 5 more (use --max 0 to see all)\n") {
		t.Fatalf("cell members default max missing summary line:\n%s", out)
	}
	if !strings.Contains(out, "TABLE CELL table=celltable row=r1 col=c1 n=25") {
		t.Fatalf("cell members default max missing total member count:\n%s", out)
	}

	// 2. Custom max (--max 5): should show 5 members and "... and 20 more (use --max 0 to see all)"
	code, out, errout = runTable(at(addr, "cell", "members", "celltable", "r1", "c1", "--max", "5")...)
	if code != 0 || errout != "" {
		t.Fatalf("cell members --max 5: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE MEMBER "); n != 5 {
		t.Fatalf("cell members --max 5: got %d member lines, want 5\n%s", n, out)
	}
	if !strings.Contains(out, "... and 20 more (use --max 0 to see all)\n") {
		t.Fatalf("cell members --max 5 missing summary line:\n%s", out)
	}

	// 3. Custom max larger than total (--max 30): should show all 25 members, no summary line
	code, out, errout = runTable(at(addr, "cell", "members", "celltable", "r1", "c1", "--max", "30")...)
	if code != 0 || errout != "" {
		t.Fatalf("cell members --max 30: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE MEMBER "); n != 25 {
		t.Fatalf("cell members --max 30: got %d member lines, want 25\n%s", n, out)
	}
	if strings.Contains(out, "... and") {
		t.Fatalf("cell members --max 30 should have no summary line:\n%s", out)
	}

	// 4. Max 0 (--max 0): should show all 25 members, no summary line
	code, out, errout = runTable(at(addr, "cell", "members", "celltable", "r1", "c1", "--max", "0")...)
	if code != 0 || errout != "" {
		t.Fatalf("cell members --max 0: %d %q", code, errout)
	}
	if n := countMatchingLines(out, "TABLE MEMBER "); n != 25 {
		t.Fatalf("cell members --max 0: got %d member lines, want 25\n%s", n, out)
	}
	if strings.Contains(out, "... and") {
		t.Fatalf("cell members --max 0 should have no summary line:\n%s", out)
	}
}
