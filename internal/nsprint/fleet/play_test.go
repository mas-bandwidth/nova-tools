package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
)

func testRedisClient(t *testing.T, addr string) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

type fakeExecRunner struct {
	mu    sync.Mutex
	calls []string
	out   map[string]string
	err   map[string]error
}

func (f *fakeExecRunner) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := strings.Join(argv, " ")
	f.calls = append(f.calls, cmd)
	for k, v := range f.out {
		if strings.Contains(cmd, k) {
			return v, f.err[k]
		}
	}
	return "", nil
}

func (f *fakeExecRunner) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func sampleAnsibleOutput() string {
	return `
PLAY [all] *********************************************************************

TASK [Gathering Facts] *********************************************************
ok: [alpha]
ok: [bravo]

TASK [role1 : step one] ********************************************************
ok: [alpha]
changed: [bravo]

TASK [role2 : step two] ********************************************************
ok: [alpha]
fatal: [bravo]: FAILED! => {"msg": "command failed"}

PLAY RECAP *********************************************************************
alpha                      : ok=3    changed=0    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0   
bravo                      : ok=2    changed=1    unreachable=0    failed=1    skipped=0    rescued=0    ignored=0   

ROLES RECAP ********************************************************************
role1 ------------------------------------------------------------------- 0.25s
role2 ------------------------------------------------------------------- 0.50s
gather_facts ------------------------------------------------------------ 0.15s
`
}

func TestParsePlayReadsTheRecordedRecap(t *testing.T) {
	t.Parallel()

	hosts := fleet.ParsePlay(sampleAnsibleOutput())
	if len(hosts) != 2 {
		t.Fatalf("ParsePlay returned %d hosts, want 2", len(hosts))
	}

	h0 := hosts[0]
	if h0.Host != "alpha" || h0.Failed != "" || h0.Result() != "ok" {
		t.Errorf("host 0: got host=%s failed=%s result=%s, want alpha / '' / ok", h0.Host, h0.Failed, h0.Result())
	}
	if len(h0.Roles) != 3 {
		t.Fatalf("host 0: got %d roles, want 3", len(h0.Roles))
	}
	if h0.Roles[0].Role != "facts" || h0.Roles[0].State != "ok" || h0.Roles[0].MS != 150 {
		t.Errorf("host 0 role 0: got %+v, want facts ok ms=150", h0.Roles[0])
	}
	if h0.Roles[1].Role != "role1" || h0.Roles[1].State != "ok" || h0.Roles[1].MS != 250 {
		t.Errorf("host 0 role 1: got %+v, want role1 ok ms=250", h0.Roles[1])
	}
	if h0.Roles[2].Role != "role2" || h0.Roles[2].State != "ok" || h0.Roles[2].MS != 500 {
		t.Errorf("host 0 role 2: got %+v, want role2 ok ms=500", h0.Roles[2])
	}

	h1 := hosts[1]
	if h1.Host != "bravo" || h1.Failed != "role2" || h1.Result() != "failed:role2" {
		t.Errorf("host 1: got host=%s failed=%s result=%s, want bravo / role2 / failed:role2", h1.Host, h1.Failed, h1.Result())
	}
	if len(h1.Roles) != 3 {
		t.Fatalf("host 1: got %d roles, want 3", len(h1.Roles))
	}
	if h1.Roles[1].Role != "role1" || h1.Roles[1].State != "changed" {
		t.Errorf("host 1 role 1: got %+v, want role1 changed", h1.Roles[1])
	}
	if h1.Roles[2].Role != "role2" || h1.Roles[2].State != "failed" {
		t.Errorf("host 1 role 2: got %+v, want role2 failed", h1.Roles[2])
	}
}

func TestParsePlayEdges(t *testing.T) {
	t.Parallel()

	// Missing PLAY RECAP
	if got := fleet.ParsePlay("ERROR! the playbook could not be found\n"); len(got) != 0 {
		t.Errorf("ParsePlay on error output returned %d hosts, want 0", len(got))
	}

	// Rescued failure (...ignoring)
	ign := `
TASK [role1 : do thing] ********************************************************
fatal: [alpha]: FAILED! => {"msg": "ignore me"}
...ignoring

PLAY RECAP *********************************************************************
alpha                      : ok=1    changed=0    unreachable=0    failed=0    skipped=0    rescued=0    ignored=1   
`
	got := fleet.ParsePlay(ign)
	if len(got) != 1 || got[0].Failed != "" {
		t.Errorf("ParsePlay ignored failure: got %+v, want unfailed", got)
	}
}

func setupTestPlayDir(t *testing.T, tag string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fleet.PlayInventory), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fleet.PlayFile(tag)), []byte("---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func setupTestRegistry(t *testing.T, benches ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "machines.tsv")
	var lines []string
	for _, b := range benches {
		lines = append(lines, b+"\tlinux-amd64\tamd64")
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlayRunsThroughExecRunner(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := testRedisClient(t, mr.Addr())

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
		t.Errorf("PlayResult.OK: got true, want false (bravo failed)")
	}
	if failed := res.Failed(); len(failed) != 1 || failed[0] != "bravo:role2" {
		t.Errorf("PlayResult.Failed: got %v, want [bravo:role2]", failed)
	}

	// Verify receipts printed
	s := out.String()
	if !strings.Contains(s, "FLEET PLAY alpha facts ok ms=150") {
		t.Errorf("output missing alpha facts ok: %s", s)
	}
	if !strings.Contains(s, "FLEET PLAY bravo role2 failed ms=500") {
		t.Errorf("output missing bravo role2 failed: %s", s)
	}

	// Verify Redis writes
	alphaPlay, err := c.HGetAll(ctx, fleet.PlayKey("alpha")).Result()
	if err != nil || alphaPlay["result"] != "ok" || alphaPlay["role"] != "role2" {
		t.Errorf("alpha play receipt: got %v, want result=ok role=role2", alphaPlay)
	}
	behindAlpha, _ := c.HGet(ctx, "bench:alpha", "behind").Result()
	if behindAlpha != "" {
		t.Errorf("alpha behind: got %q, want empty", behindAlpha)
	}

	bravoPlay, err := c.HGetAll(ctx, fleet.PlayKey("bravo")).Result()
	if err != nil || bravoPlay["result"] != "failed:role2" || bravoPlay["role"] != "role2" {
		t.Errorf("bravo play receipt: got %v, want result=failed:role2 role=role2", bravoPlay)
	}
	behindBravo, _ := c.HGet(ctx, "bench:bravo", "behind").Result()
	if behindBravo != "role2" {
		t.Errorf("bravo behind on bench:bravo: got %q, want role2", behindBravo)
	}
	behindBravoState, _ := c.HGet(ctx, "bench:bravo:state", "behind").Result()
	if behindBravoState != "role2" {
		t.Errorf("bravo behind on bench:bravo:state: got %q, want role2", behindBravoState)
	}
}

func TestPlayRefusesDirtyOrBehindClone(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := testRedisClient(t, mr.Addr())
	playDir := setupTestPlayDir(t, "tools")
	reg := setupTestRegistry(t, "alpha")
	sha := "0123456789abcdef0123456789abcdef01234567"

	// 1. Dirty clone
	runnerDirty := &fakeExecRunner{
		out: map[string]string{
			"git rev-parse": playDir + " " + sha,
			"git status":    " M dirty.txt\n",
		},
	}
	pDirty := &fleet.Play{
		Runner:   runnerDirty,
		Client:   c,
		PlayDir:  playDir,
		Tag:      "tools",
		Registry: reg,
	}
	if _, err := pDirty.Run(context.Background()); !errors.Is(err, fleet.ErrRefused) {
		t.Errorf("dirty clone: got error %v, want ErrRefused", err)
	}

	// 2. Behind upstream
	runnerBehind := &fakeExecRunner{
		out: map[string]string{
			"git rev-parse": playDir + " " + sha,
			"git status":    "",
			"git fetch":     "",
			"git rev-list":  "3",
		},
	}
	pBehind := &fleet.Play{
		Runner:   runnerBehind,
		Client:   c,
		PlayDir:  playDir,
		Tag:      "tools",
		Registry: reg,
	}
	if _, err := pBehind.Run(context.Background()); !errors.Is(err, fleet.ErrRefused) {
		t.Errorf("behind clone: got error %v, want ErrRefused", err)
	}
}

func TestPlayDryRun(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := testRedisClient(t, mr.Addr())
	playDir := setupTestPlayDir(t, "tools")
	reg := setupTestRegistry(t, "alpha")
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

	p := &fleet.Play{
		Runner:   runner,
		Client:   c,
		PlayDir:  playDir,
		Tag:      "tools",
		Registry: reg,
		DryRun:   true,
	}

	ctx := context.Background()
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("DryRun failed: %v", err)
	}
	if !res.DryRun {
		t.Errorf("PlayResult.DryRun: got false, want true")
	}
	if !strings.Contains(res.Line(), "check=yes") {
		t.Errorf("PlayResult.Line: got %q, want check=yes", res.Line())
	}

	// In DryRun, no receipts written to store
	keys, err := c.Keys(ctx, "bench:*").Result()
	if err != nil || len(keys) != 0 {
		t.Errorf("DryRun wrote keys: %v", keys)
	}
}
