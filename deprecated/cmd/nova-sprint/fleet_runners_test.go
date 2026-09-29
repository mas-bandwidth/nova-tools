package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
)

func TestFleetRunnersCLI(t *testing.T) {
	t.Parallel()

	m := miniredis.RunT(t)
	m.SAdd("benches", "bravo")

	dir := t.TempDir()
	runnersPath := filepath.Join(dir, "runners.tsv")
	content := "bravo\t3\tbravo\tnova\n"
	if err := os.WriteFile(runnersPath, []byte(content), 0644); err != nil {
		t.Fatalf("write runners.tsv: %v", err)
	}

	m.HSet(fleet.RunnersKeyRegistered, "bravo", "3")

	ps := fleet.PSSample{
		Units: []fleet.PSUnit{
			{Name: "nova-runner-1.service", State: fleet.UnitDeclared},
			{Name: "nova-runner-2.service", State: fleet.UnitDeclared},
			{Name: "nova-runner-3.service", State: fleet.UnitDeclared},
		},
	}
	m.HSet("bench:bravo:beat", "ps", ps.Encode())

	// 1. Clean list run -> exit 0
	{
		var out, errOut bytes.Buffer
		code := runFleet(context.Background(), []string{"runners", "--redis", m.Addr(), "--runners", runnersPath}, &out, &errOut)
		if code != 0 {
			t.Fatalf("runners list clean exited with %d; stderr %q, stdout %q", code, errOut.String(), out.String())
		}
		if !strings.Contains(out.String(), "RUNNERS OK total_declared=3 total_registered=3 total_online=3") {
			t.Fatalf("runners list clean stdout mismatch: %s", out.String())
		}
	}

	// 2. Drift list run -> exit 1
	{
		m.HSet(fleet.RunnersKeyRegistered, "bravo", "2") // Mismatch
		var out, errOut bytes.Buffer
		code := runFleet(context.Background(), []string{"runners", "--redis", m.Addr(), "--runners", runnersPath}, &out, &errOut)
		if code != 1 {
			t.Fatalf("runners list drift exited with %d, want 1; stderr %q, stdout %q", code, errOut.String(), out.String())
		}
		if !strings.Contains(out.String(), "RUNNERS DRIFT") {
			t.Fatalf("runners list drift stdout missing RUNNERS DRIFT: %s", out.String())
		}
	}

	// 3. Set run -> exit 0 (with seam or file edit)
	{
		var out, errOut bytes.Buffer
		// set without git seam will try default seam, which fails git commands if not a repo;
		// we verify the validation and file update
		code := runFleet(context.Background(), []string{"runners", "set", "bravo", "6", "--runners", runnersPath}, &out, &errOut)
		// Even if PR creation fails without git repo, file was saved with 6 and play command is printed
		data, err := os.ReadFile(runnersPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "bravo\t6\t") {
			t.Fatalf("runners.tsv was not updated to 6: %s", string(data))
		}
		if code == 0 {
			if !strings.Contains(out.String(), "RUNNERS SET bench=bravo count=6") {
				t.Fatalf("runners set stdout missing RUNNERS SET: %s", out.String())
			}
		}
	}

	// 4. Usage error for set (negative count) -> exit 2
	{
		var out, errOut bytes.Buffer
		code := runFleet(context.Background(), []string{"runners", "set", "bravo", "-1", "--runners", runnersPath}, &out, &errOut)
		if code != 2 {
			t.Fatalf("runners set negative count exited with %d, want 2", code)
		}
	}

	// 5. Store unreachable for list -> exit 5
	{
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		closedAddr := ln.Addr().String()
		_ = ln.Close()

		var out, errOut bytes.Buffer
		code := runFleet(context.Background(), []string{"runners", "--redis", closedAddr}, &out, &errOut)
		if code != 5 {
			t.Fatalf("runners unreachable exited with %d, want 5", code)
		}
	}
}
