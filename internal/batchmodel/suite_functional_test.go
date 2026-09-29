//go:build functional

package batchmodel

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

func TestSuite(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	output := filepath.Join(t.TempDir(), "suite")
	cases, err := CaptureSuite(ctx, CaptureSuiteOptions{
		Source:      filepath.Join("..", "nsprint", "fn", "lua", "table.lua"),
		RedisServer: testutil.Program(t), ModelsDir: filepath.Join("..", "..", "tla"), OutputDir: output, TmpDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 6 {
		t.Fatalf("got %d suite cases", len(cases))
	}
	for _, c := range cases {
		if c.SourceSHA256 == "" || c.Bundle.SourceSHA256 == "" || c.Bundle.ConfigSHA256 == "" || !filepath.IsAbs(c.Bundle.Module) || !filepath.IsAbs(c.Bundle.Config) || !filepath.IsAbs(c.Bundle.Evidence) {
			t.Fatalf("incomplete bundle %s: %+v", c.Name, c)
		}
	}
	if cases[3].Name != "corrupt-observation" || cases[3].Expected != "invariant-reject" || cases[3].Property != "MatchesExecution" {
		t.Fatalf("negative case metadata differs: %+v", cases[3])
	}
	if cases[4].Name != "second-epoch" || cases[4].Expected != "pass" {
		t.Fatalf("second-epoch case missing: %+v", cases[4])
	}
	if cases[5].Name != "ordinary-remove-omitted-guard" || cases[5].Expected != "pass" {
		t.Fatalf("ordinary writer case missing: %+v", cases[5])
	}
}
