package land_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

func TestLoadPRSurfacesExpectedPolicyReadError(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })

	const (
		sprint = "sprint-test"
		repo   = "nova-tools"
		prNum  = 101
		head   = "1111111111111111111111111111111111111111"
		base   = "dev"
	)
	id := land.ID{Repo: repo, N: prNum}

	// Setup basic PR and sprint records
	c.HSet(ctx, id.Key(sprint), "head", head, "base", base, "base_sha", "tip1")
	c.HSet(ctx, "s:"+sprint+":policy", "readers", "1")

	t.Run("surfaces redis read error on policy lookup", func(t *testing.T) {
		// Set a string value on the policy key so HMGet fails with WRONGTYPE
		polKey := civerdict.PolicyKey(repo, base)
		mr.Set(polKey, "not-a-hash")

		_, err := land.LoadPR(ctx, c, sprint, id)
		if err == nil {
			t.Fatal("LoadPR succeeded; want error surfacing policy lookup failure")
		}
		if !strings.Contains(err.Error(), "WRONGTYPE") {
			t.Fatalf("LoadPR error %v; want WRONGTYPE", err)
		}
	})

	t.Run("no policy record results in nil CI without error", func(t *testing.T) {
		mr.Del(civerdict.PolicyKey(repo, base))

		p, err := land.LoadPR(ctx, c, sprint, id)
		if err != nil {
			t.Fatalf("LoadPR failed: %v", err)
		}
		if len(p.CI) != 0 {
			t.Fatalf("p.CI = %v; want empty map when policy absent", p.CI)
		}
	})
}
