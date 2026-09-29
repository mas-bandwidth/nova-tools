//go:build functional

package fleet

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRunnersStatusWithRedis(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	ctx := context.Background()

	// 1. Prepare runners.tsv
	dir := t.TempDir()
	runnersPath := filepath.Join(dir, "runners.tsv")
	content := "studio\t4\tstudio,darwin-arm64\tnova\nspace\t4\tspace\tubuntu\nhetzner\t4\tspace\tnova\n"
	if err := os.WriteFile(runnersPath, []byte(content), 0644); err != nil {
		t.Fatalf("write runners.tsv: %v", err)
	}

	// 2. Populate Redis benches and registered cache
	client.SAdd(ctx, "benches", "studio", "space", "hetzner")
	client.HSet(ctx, RunnersKeyRegistered, "studio", "4", "space", "4", "hetzner", "2")

	// 3. Populate beats
	studioSample := PSSample{
		Units: []PSUnit{
			{Name: "com.nova.runner-1.plist", State: UnitDeclared},
			{Name: "com.nova.runner-2.plist", State: UnitDeclared},
			{Name: "com.nova.runner-3.plist", State: UnitDeclared},
			{Name: "com.nova.runner-4.plist", State: UnitDeclared},
		},
	}
	spaceSample := PSSample{
		Units: []PSUnit{
			{Name: "nova-runner-1.service", State: UnitDeclared},
			{Name: "nova-runner-2.service", State: UnitDeclared},
			{Name: "nova-runner-3.service", State: UnitDeclared},
			{Name: "nova-runner-4.service", State: UnitDeclared},
		},
	}
	hetznerSample := PSSample{
		Units: []PSUnit{
			{Name: "nova-runner-1.service", State: UnitDeclared},
			{Name: "nova-runner-2.service", State: UnitDeclared},
		},
	}

	client.HSet(ctx, "bench:studio:beat", "ps", studioSample.Encode(), "at", "1000")
	client.HSet(ctx, "bench:space:beat", "ps", spaceSample.Encode(), "at", "1000")
	client.HSet(ctx, "bench:hetzner:beat", "ps", hetznerSample.Encode(), "at", "1000")

	// Read status
	st, err := ReadRunnersStatus(ctx, client, runnersPath, "", nil)
	if err != nil {
		t.Fatalf("ReadRunnersStatus: %v", err)
	}

	if st.TotalDeclared != 12 {
		t.Errorf("TotalDeclared = %d, want 12", st.TotalDeclared)
	}
	if st.TotalRegistered != 10 {
		t.Errorf("TotalRegistered = %d, want 10", st.TotalRegistered)
	}
	if st.TotalOnline != 10 {
		t.Errorf("TotalOnline = %d, want 10", st.TotalOnline)
	}

	if len(st.Drifting) != 1 || st.Drifting[0] != "hetzner" {
		t.Errorf("Drifting = %v, want [hetzner]", st.Drifting)
	}
}

func TestSetRunnersCommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runnersPath := filepath.Join(dir, "runners.tsv")
	content := "studio\t4\tstudio,darwin-arm64\tnova\nspace\t4\tspace\tubuntu\n"
	if err := os.WriteFile(runnersPath, []byte(content), 0644); err != nil {
		t.Fatalf("write runners.tsv: %v", err)
	}

	var capturedBranch, capturedTitle string
	fakePRCreator := func(ctx context.Context, dir, branch, title, body string) (string, error) {
		capturedBranch = branch
		capturedTitle = title
		return "https://example.com/mas-bandwidth/nova-tools/pull/999", nil
	}

	res, err := SetRunners(context.Background(), SetRunnersRequest{
		Bench:       "space",
		Count:       8,
		RunnersPath: runnersPath,
		PRCreator:   fakePRCreator,
	})
	if err != nil {
		t.Fatalf("SetRunners failed: %v", err)
	}

	if res.OldCount != 4 {
		t.Errorf("OldCount = %d, want 4", res.OldCount)
	}
	if res.Count != 8 {
		t.Errorf("Count = %d, want 8", res.Count)
	}
	if res.PRURL != "https://example.com/mas-bandwidth/nova-tools/pull/999" {
		t.Errorf("PRURL = %q", res.PRURL)
	}
	if want := "nova-sprint fleet play runners --limit space"; res.PlayCmd != want {
		t.Errorf("PlayCmd = %q, want %q", res.PlayCmd, want)
	}

	if capturedBranch == "" || capturedTitle == "" {
		t.Errorf("PRCreator not called with expected args")
	}

	// Verify file on disk
	tbl, err := ReadRunnersFile(runnersPath)
	if err != nil {
		t.Fatalf("ReadRunnersFile: %v", err)
	}
	if got := tbl.Count("space"); got != 8 {
		t.Errorf("saved count for space = %d, want 8", got)
	}
}
