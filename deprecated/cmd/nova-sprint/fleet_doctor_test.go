package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
)

func TestFleetDoctorCLI(t *testing.T) {
	t.Parallel()

	m := miniredis.RunT(t)
	m.HSet("fleet:release", "version", "v0.16.0-dev.c250d86a", "commit", "c250d86a")
	m.SAdd("benches", "alpha")

	dir := t.TempDir()
	runnersPath := filepath.Join(dir, "runners.tsv")
	content := "alpha\t2\talpha\tnova\n"
	if err := os.WriteFile(runnersPath, []byte(content), 0644); err != nil {
		t.Fatalf("write runners.tsv: %v", err)
	}

	m.HSet(fleet.RunnersKeyRegistered, "alpha", "2")

	nowMS := time.Now().UnixMilli()
	ps := fleet.PSSample{
		Units: []fleet.PSUnit{
			{Name: "nova-runner-1.service", State: fleet.UnitDeclared},
			{Name: "nova-runner-2.service", State: fleet.UnitDeclared},
		},
	}
	allMirrors := strings.Join(fleet.DefaultDeclaredMirrors, ",")

	m.HSet("bench:alpha:beat",
		"build", "nova-sprint v0.16.0-dev.c250d86a linux/amd64 go1.26.6",
		"go_sha", "708effb774be",
		"mirrors", allMirrors,
		"disk_gib", "300",
		"ps", ps.Encode(),
		"at", string(rune(nowMS)),
	)
	m.HSet("bench:alpha:play", "at", "1000", "tag", "tools", "role", "bench", "result", "ok")

	// 1. Clean run -> exit 0
	{
		var out, errOut bytes.Buffer
		code := runFleet(context.Background(), []string{"doctor", "--redis", m.Addr(), "--runners", runnersPath}, &out, &errOut)
		if code != 0 {
			t.Fatalf("doctor clean exited with %d; stderr %q, stdout %q", code, errOut.String(), out.String())
		}
		if !strings.Contains(out.String(), "DOCTOR OK") {
			t.Fatalf("doctor clean stdout missing DOCTOR OK: %s", out.String())
		}
		if !strings.Contains(out.String(), "BENCH") {
			t.Fatalf("doctor clean stdout missing table header: %s", out.String())
		}
	}

	// 2. Drift run -> exit 1
	{
		m.HSet("bench:alpha:beat", "disk_gib", "50") // Under 200 GiB floor
		var out, errOut bytes.Buffer
		code := runFleet(context.Background(), []string{"doctor", "--redis", m.Addr(), "--runners", runnersPath}, &out, &errOut)
		if code != 1 {
			t.Fatalf("doctor drift exited with %d, want 1; stderr %q, stdout %q", code, errOut.String(), out.String())
		}
		if !strings.Contains(out.String(), "DOCTOR FIX") {
			t.Fatalf("doctor drift stdout missing DOCTOR FIX: %s", out.String())
		}
		if !strings.Contains(out.String(), "check=disk") {
			t.Fatalf("doctor drift stdout missing check=disk: %s", out.String())
		}
	}

	// 3. Usage error -> exit 2
	{
		var out, errOut bytes.Buffer
		code := runFleet(context.Background(), []string{"doctor", "--redis", m.Addr(), "unexpected-arg"}, &out, &errOut)
		if code != 2 {
			t.Fatalf("doctor unexpected arg exited with %d, want 2", code)
		}
	}

	// 4. Store unreachable -> exit 5
	{
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		closedAddr := ln.Addr().String()
		_ = ln.Close()

		var out, errOut bytes.Buffer
		code := runFleet(context.Background(), []string{"doctor", "--redis", closedAddr}, &out, &errOut)
		if code != 5 {
			t.Fatalf("doctor unreachable exited with %d, want 5", code)
		}
	}
}
