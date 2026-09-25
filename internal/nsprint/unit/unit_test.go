package unit

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func TestUnits(t *testing.T) {
	t.Run("plan-golden", func(t *testing.T) {
		opts := PlanOptions{
			Role:    "coordinator",
			Store:   "127.0.0.1:6379",
			OS:      "darwin",
			Friends: map[string]int{"rowan": 4},
		}
		digests, files, planSha, err := Plan(opts)
		if err != nil {
			t.Fatalf("Plan failed: %v", err)
		}
		if len(digests) != 4 {
			t.Errorf("got %d units, want 4", len(digests))
		}
		if planSha == "" {
			t.Errorf("empty planSha")
		}
		for name, content := range files {
			if !strings.Contains(content, "AbandonProcessGroup") {
				t.Errorf("unit %s missing AbandonProcessGroup", name)
			}
		}
	})

	t.Run("plan-negative", func(t *testing.T) {
		u := UnitDef{
			Name:   "bad-unit",
			Verb:   []string{"reconcile"},
			User:   "coordinator",
			Secret: "SECRET-WITH-PASSWORD-123",
		}
		_, err := RenderUnit(u, "darwin")
		if err == nil {
			t.Errorf("expected error when password in argv, got nil")
		}
	})

	t.Run("declare", func(t *testing.T) {
		mr := miniredis.RunT(t)
		ctx := context.Background()
		st, err := store.Open(ctx, mr.Addr())
		if err != nil {
			t.Fatalf("store open: %v", err)
		}
		defer st.Close()

		opts := PlanOptions{
			Role:  "coordinator",
			Store: mr.Addr(),
			OS:    "darwin",
		}
		res, err := Declare(ctx, st, opts, "seat-1", false)
		if err != nil {
			t.Fatalf("Declare failed: %v", err)
		}
		if !strings.HasPrefix(res, "DECLARED") {
			t.Errorf("expected DECLARED, got %q", res)
		}

		res2, err := Declare(ctx, st, opts, "seat-1", false)
		if err != nil {
			t.Fatalf("Declare 2 failed: %v", err)
		}
		if res2 != "UNCHANGED" {
			t.Errorf("expected UNCHANGED, got %q", res2)
		}
	})

	t.Run("check-match", func(t *testing.T) {
		mr := miniredis.RunT(t)
		ctx := context.Background()
		st, err := store.Open(ctx, mr.Addr())
		if err != nil {
			t.Fatalf("store open: %v", err)
		}
		defer st.Close()

		optsCoord := PlanOptions{Role: "coordinator", Store: mr.Addr(), OS: "darwin"}
		_, err = Declare(ctx, st, optsCoord, "seat-1", false)
		if err != nil {
			t.Fatalf("Declare coord: %v", err)
		}
		_, err = Apply(ctx, st, optsCoord, t.TempDir())
		if err != nil {
			t.Fatalf("Apply coord: %v", err)
		}

		defects, code, err := Check(ctx, st, "", true)
		if err != nil {
			t.Fatalf("Check failed: %v", err)
		}
		if code != 0 {
			t.Errorf("Check exit code = %d, want 0, defects: %v", code, defects)
		}
	})

	t.Run("check-missing", func(t *testing.T) {
		mr := miniredis.RunT(t)
		ctx := context.Background()
		st, err := store.Open(ctx, mr.Addr())
		if err != nil {
			t.Fatalf("store open: %v", err)
		}
		defer st.Close()

		optsCoord := PlanOptions{Role: "coordinator", Store: mr.Addr(), OS: "darwin"}
		_, err = Declare(ctx, st, optsCoord, "seat-1", false)
		if err != nil {
			t.Fatalf("Declare coord: %v", err)
		}
		_, err = Apply(ctx, st, optsCoord, t.TempDir())
		if err != nil {
			t.Fatalf("Apply coord: %v", err)
		}
		client := st.Client()
		client.Del(ctx, "unit:coordinator:ns-reconciler")

		defects, code, err := Check(ctx, st, "", true)
		if err != nil {
			t.Fatalf("Check failed: %v", err)
		}
		if code == 0 {
			t.Errorf("Check expected non-zero code for missing unit")
		}
		foundMissing := false
		for _, d := range defects {
			if strings.Contains(d, "MISSING") {
				foundMissing = true
			}
		}
		if !foundMissing {
			t.Errorf("expected MISSING defect, got %v", defects)
		}
	})

	t.Run("check-extra-undeclared", func(t *testing.T) {
		mr := miniredis.RunT(t)
		ctx := context.Background()
		st, err := store.Open(ctx, mr.Addr())
		if err != nil {
			t.Fatalf("store open: %v", err)
		}
		defer st.Close()

		optsCoord := PlanOptions{Role: "coordinator", Store: mr.Addr(), OS: "darwin"}
		_, err = Declare(ctx, st, optsCoord, "seat-1", false)
		if err != nil {
			t.Fatalf("Declare coord: %v", err)
		}
		_, err = Apply(ctx, st, optsCoord, t.TempDir())
		if err != nil {
			t.Fatalf("Apply coord: %v", err)
		}
		client := st.Client()
		client.SAdd(ctx, "units:coordinator", "extra-unit")

		defects, code, err := Check(ctx, st, "", true)
		if err != nil {
			t.Fatalf("Check failed: %v", err)
		}
		if code == 0 {
			t.Errorf("Check expected non-zero code for extra unit")
		}
		foundExtra := false
		for _, d := range defects {
			if strings.Contains(d, "EXTRA") {
				foundExtra = true
			}
		}
		if !foundExtra {
			t.Errorf("expected EXTRA defect, got %v", defects)
		}
	})
}
