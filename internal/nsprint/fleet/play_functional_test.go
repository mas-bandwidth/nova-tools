//go:build functional

package fleet_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

func TestPlayFunctionalRealRedis(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	playDir := setupTestPlayDir(t, "tools")
	reg := setupTestRegistry(t, "alpha", "bravo")

	sha := "0123456789abcdef0123456789abcdef01234567"
	runner := &fakeExecRunner{
		out: map[string]string{
			"git rev-parse":    playDir + " " + sha,
			"git status":       "",
			"git fetch":        "",
			"git rev-list":     "0",
			"ansible-playbook": sampleAnsibleOutput(),
		},
	}

	var out bytes.Buffer
	p := &fleet.Play{
		Runner:   runner,
		Client:   c,
		PlayDir:  playDir,
		Tag:      "tools",
		Registry: reg,
		Benches:  []string{"alpha", "bravo"},
		Out:      &out,
	}

	ctx := context.Background()
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Play.Run failed: %v", err)
	}
	if res.OK() {
		t.Errorf("res.OK: got true, want false")
	}

	// Verify real Redis receipts
	alphaPlay, err := c.HGetAll(ctx, fleet.PlayKey("alpha")).Result()
	if err != nil || alphaPlay["result"] != "ok" {
		t.Errorf("alpha play receipt: %v, err=%v", alphaPlay, err)
	}

	bravoPlay, err := c.HGetAll(ctx, fleet.PlayKey("bravo")).Result()
	if err != nil || bravoPlay["result"] != "failed:role2" {
		t.Errorf("bravo play receipt: %v, err=%v", bravoPlay, err)
	}

	// Verify behind field on bench row
	behindBravo, err := c.HGet(ctx, "bench:bravo", "behind").Result()
	if err != nil || behindBravo != "role2" {
		t.Errorf("bench:bravo behind: %q, err=%v", behindBravo, err)
	}

	behindBravoState, err := c.HGet(ctx, "bench:bravo:state", "behind").Result()
	if err != nil || behindBravoState != "role2" {
		t.Errorf("bench:bravo:state behind: %q, err=%v", behindBravoState, err)
	}

	// Now run successful play on bravo and verify behind field is cleared
	runnerOK := &fakeExecRunner{
		out: map[string]string{
			"git rev-parse": playDir + " " + sha,
			"git status":    "",
			"git fetch":     "",
			"git rev-list":  "0",
			"ansible-playbook": `
PLAY [all] *********************************************************************
TASK [Gathering Facts] *********************************************************
ok: [bravo]
TASK [role1 : step] ************************************************************
ok: [bravo]
PLAY RECAP *********************************************************************
bravo                      : ok=2    changed=0    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0   
ROLES RECAP ********************************************************************
role1 ------------------------------------------------------------------- 0.10s
gather_facts ------------------------------------------------------------ 0.10s
`,
		},
	}

	pOK := &fleet.Play{
		Runner:   runnerOK,
		Client:   c,
		PlayDir:  playDir,
		Tag:      "tools",
		Registry: reg,
		Benches:  []string{"bravo"},
	}
	resOK, err := pOK.Run(ctx)
	if err != nil || !resOK.OK() {
		t.Fatalf("pOK.Run failed: %v", err)
	}

	behindCleared, _ := c.HGet(ctx, "bench:bravo", "behind").Result()
	if behindCleared != "" {
		t.Errorf("behind not cleared on bench:bravo after success: got %q", behindCleared)
	}
}
