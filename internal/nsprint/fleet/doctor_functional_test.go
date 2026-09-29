//go:build functional

package fleet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestFleetDoctorCleanWithRedis(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	ctx := context.Background()

	// 1. Release
	client.HSet(ctx, "fleet:release", "version", "v0.16.0-dev.c250d86a", "commit", "c250d86a1122334455667788")

	// 2. Benches set
	client.SAdd(ctx, "benches", "studio", "space")

	// 3. Runners TSV
	dir := t.TempDir()
	runnersPath := filepath.Join(dir, "runners.tsv")
	content := "studio\t4\tstudio,darwin-arm64\tnova\nspace\t4\tspace\tubuntu\n"
	if err := os.WriteFile(runnersPath, []byte(content), 0644); err != nil {
		t.Fatalf("write runners.tsv: %v", err)
	}

	// 4. Registered runners cache
	client.HSet(ctx, RunnersKeyRegistered, "studio", "4", "space", "4")

	// 5. Beats
	nowMS := time.Now().UnixMilli()
	studioPS := PSSample{
		Units: []PSUnit{
			{Name: "com.nova.runner-1.plist", State: UnitDeclared},
			{Name: "com.nova.runner-2.plist", State: UnitDeclared},
			{Name: "com.nova.runner-3.plist", State: UnitDeclared},
			{Name: "com.nova.runner-4.plist", State: UnitDeclared},
		},
	}
	spacePS := PSSample{
		Units: []PSUnit{
			{Name: "nova-runner-1.service", State: UnitDeclared},
			{Name: "nova-runner-2.service", State: UnitDeclared},
			{Name: "nova-runner-3.service", State: UnitDeclared},
			{Name: "nova-runner-4.service", State: UnitDeclared},
		},
	}

	allMirrors := strings.Join(DefaultDeclaredMirrors, ",")

	client.HSet(ctx, "bench:studio:beat",
		"build", "nova-sprint v0.16.0-dev.c250d86a darwin/arm64 go1.26.6",
		"go_sha", "2dc95ce46758",
		"mirrors", allMirrors,
		"disk_gib", "450",
		"ps", studioPS.Encode(),
		"at", nowMS,
	)
	client.HSet(ctx, "bench:space:beat",
		"build", "nova-sprint v0.16.0-dev.c250d86a linux/amd64 go1.26.6",
		"go_sha", "708effb774be",
		"mirrors", allMirrors,
		"disk_gib", "320",
		"ps", spacePS.Encode(),
		"at", nowMS,
	)

	// 6. Plays
	client.HSet(ctx, "bench:studio:play", "at", nowMS, "tag", "tools", "role", "bench", "result", "ok")
	client.HSet(ctx, "bench:space:play", "at", nowMS, "tag", "tools", "role", "bench", "result", "ok")

	// Run doctor
	res, err := Doctor(ctx, client, DoctorRequest{
		RunnersPath: runnersPath,
	})
	if err != nil {
		t.Fatalf("Doctor failed: %v", err)
	}

	if res.TotalFixes != 0 {
		t.Errorf("TotalFixes = %d, want 0; fixes: %v", res.TotalFixes, res.Fixes)
	}
	if res.TotalChecks != 14 {
		t.Errorf("TotalChecks = %d, want 14", res.TotalChecks)
	}
	if !strings.HasPrefix(res.SummaryLine(), "DOCTOR OK") {
		t.Errorf("summary = %q, want DOCTOR OK prefix", res.SummaryLine())
	}
	if !strings.Contains(res.ScreenTable(), "studio") || !strings.Contains(res.ScreenTable(), "space") {
		t.Errorf("ScreenTable missing benches:\n%s", res.ScreenTable())
	}
}

func TestFleetDoctorDriftsWithRedis(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	ctx := context.Background()

	client.HSet(ctx, "fleet:release", "version", "v0.16.0-dev.c250d86a", "commit", "c250d86a1122334455667788")

	benches := []string{"b_ver", "b_sdk", "b_play", "b_run", "b_mir", "b_disk", "b_unit"}
	for _, b := range benches {
		client.SAdd(ctx, "benches", b)
	}

	dir := t.TempDir()
	runnersPath := filepath.Join(dir, "runners.tsv")
	content := "b_ver\t4\tx\t-\nb_sdk\t4\tx\t-\nb_play\t4\tx\t-\nb_run\t4\tx\t-\nb_mir\t4\tx\t-\nb_disk\t4\tx\t-\nb_unit\t4\tx\t-\n"
	if err := os.WriteFile(runnersPath, []byte(content), 0644); err != nil {
		t.Fatalf("write runners.tsv: %v", err)
	}

	// Runners registered cache: 4 for each except b_run
	regFields := []any{}
	for _, b := range benches {
		regFields = append(regFields, b, "4")
	}
	client.HSet(ctx, RunnersKeyRegistered, regFields...)

	nowMS := time.Now().UnixMilli()
	allMirrors := strings.Join(DefaultDeclaredMirrors, ",")
	cleanPS := PSSample{
		Units: []PSUnit{
			{Name: "nova-runner-1.service", State: UnitDeclared},
			{Name: "nova-runner-2.service", State: UnitDeclared},
			{Name: "nova-runner-3.service", State: UnitDeclared},
			{Name: "nova-runner-4.service", State: UnitDeclared},
		},
	}.Encode()

	// Base clean state for every bench, then perturb one dimension per bench
	for _, b := range benches {
		client.HSet(ctx, "bench:"+b+":beat",
			"build", "nova-sprint v0.16.0-dev.c250d86a linux/amd64 go1.26.6",
			"go_sha", "708effb774be",
			"mirrors", allMirrors,
			"disk_gib", "300",
			"ps", cleanPS,
			"at", nowMS,
		)
		client.HSet(ctx, "bench:"+b+":play", "at", nowMS, "tag", "tools", "role", "bench", "result", "ok")
	}

	// 1. b_ver: version mismatch
	client.HSet(ctx, "bench:b_ver:beat", "build", "nova-sprint v0.16.0-dev.old11111 linux/amd64 go1.26.6")

	// 2. b_sdk: go_sha mismatch
	client.HSet(ctx, "bench:b_sdk:beat", "go_sha", "deadbeef0000")

	// 3. b_play: play stopped in role quack
	client.HSet(ctx, "bench:b_play:play", "result", "failed:quack", "role", "quack")

	// 4. b_run: runners online=0 (empty ps)
	client.HSet(ctx, "bench:b_run:beat", "ps", "{}")

	// 5. b_mir: mirror absent (only 4 mirrors present)
	client.HSet(ctx, "bench:b_mir:beat", "mirrors", "nova-tools,schema,serialize")

	// 6. b_disk: disk low (120 GiB < 200 GiB)
	client.HSet(ctx, "bench:b_disk:beat", "disk_gib", "120")

	// 7. b_unit: undeclared unit
	strayPS := PSSample{
		Units: []PSUnit{
			{Name: "nova-runner-1.service", State: UnitDeclared},
			{Name: "nova-runner-2.service", State: UnitDeclared},
			{Name: "nova-runner-3.service", State: UnitDeclared},
			{Name: "nova-runner-4.service", State: UnitDeclared},
			{Name: "stray-loop.service", State: UnitUndeclared},
		},
	}.Encode()
	client.HSet(ctx, "bench:b_unit:beat", "ps", strayPS)

	// Run doctor
	res, err := Doctor(ctx, client, DoctorRequest{
		RunnersPath: runnersPath,
	})
	if err != nil {
		t.Fatalf("Doctor failed: %v", err)
	}

	if res.TotalFixes != 7 {
		t.Errorf("TotalFixes = %d, want 7; fixes: %v", res.TotalFixes, res.Fixes)
	}
	if !strings.HasPrefix(res.SummaryLine(), "DOCTOR FIX benches=7 fixes=7") {
		t.Errorf("summary = %q, want DOCTOR FIX with 7 fixes", res.SummaryLine())
	}

	checksSeen := make(map[string]bool)
	for _, f := range res.Fixes {
		checksSeen[f.Check] = true
		if f.Why == "" || f.Remedy == "" {
			t.Errorf("fix missing why or remedy: %+v", f)
		}
	}

	expectedChecks := []string{"version", "go_sdk", "play", "runners", "mirrors", "disk", "units"}
	for _, exp := range expectedChecks {
		if !checksSeen[exp] {
			t.Errorf("check %q was not reported in fixes", exp)
		}
	}
}
