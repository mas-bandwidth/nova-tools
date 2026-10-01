package store

import (
	"context"
	"path"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/redisacl"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Every key a store step's own Go reads touch is in the rendered patterns of the
// role that runs the step (internal/redisacl, Roles). The table layer's keys are
// read through the library's functions, which the Lua class tests hold; what a
// step adds beside them is the route reads its flags turn on (Step.Routes: the
// routes, the tiers' arrays and the sprint row's reader tier; Step.Prices: the
// routes set and the routes' records alone). A worker's finish or read that read
// the tiers' arrays was refused NOPERM on the fleet (2026-10-01), every finish of
// every member; this table is what holds that from coming back. A new step a worker
// runs is a row here.

// keyRecorder is a go-redis hook that answers every command itself, never dialling:
// it records each command's keys and gives the set one route name, so the route
// reads walk every key they would read on a real store.
type keyRecorder struct{ keys []string }

func (k *keyRecorder) DialHook(next redis.DialHook) redis.DialHook { return next }

func (k *keyRecorder) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error { k.answer(cmd); return nil }
}

func (k *keyRecorder) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		for _, c := range cmds {
			k.answer(c)
		}
		return nil
	}
}

// answer records the key of a one-key read and gives it an empty answer, the set
// one route.
func (k *keyRecorder) answer(cmd redis.Cmder) {
	args := cmd.Args()
	if len(args) > 1 {
		if key, ok := args[1].(string); ok {
			k.keys = append(k.keys, key)
		}
	}
	switch c := cmd.(type) {
	case *redis.StringSliceCmd:
		c.SetVal([]string{"flash-a"})
	case *redis.MapStringStringCmd:
		c.SetVal(map[string]string{"tier": "flash", "provider": "p", "model": "m", "enabled": "true"})
	case *redis.StringCmd:
		c.SetVal("")
	}
}

// readKeys are the keys the route reads a step's flags turn on touch, on a store that
// answers through the recorder.
func readKeys(t *testing.T, step Step) []string {
	t.Helper()
	rec := &keyRecorder{}
	c := redis.NewClient(&redis.Options{Addr: "store.invalid:6379"})
	t.Cleanup(func() { _ = c.Close() })
	c.AddHook(rec)
	r := &Redis{C: c}
	ctx := context.Background()
	if step.Routes {
		_, _, err := r.Routes(ctx)
		require.NoError(t, err)
	}
	if step.Prices {
		_, _, err := r.PriceRoutes(ctx)
		require.NoError(t, err)
	}
	return rec.keys
}

// rolePatterns are the key patterns a role's rendered user may read.
func rolePatterns(t *testing.T, role string) []string {
	t.Helper()
	for _, r := range redisacl.Roles() {
		if r.Name != role {
			continue
		}
		var out []string
		for _, k := range r.Keys {
			_, p, _ := strings.Cut(k, "~")
			out = append(out, p)
		}
		return out
	}
	t.Fatalf("no role %s in redisacl.Roles", role)
	return nil
}

// outside are the keys no pattern of the role matches.
func outside(keys, patterns []string) []string {
	var out []string
	for _, k := range keys {
		ok := false
		for _, p := range patterns {
			if m, _ := path.Match(p, k); m {
				ok = true
				break
			}
		}
		if !ok {
			out = append(out, k)
		}
	}
	return out
}

func TestEveryStepReadsOnlyKeysItsRoleMayRead(t *testing.T) {
	t.Parallel()
	usage := "wall=1s budget=1/400000 input=10 output=1"
	gens := map[string]int{"s1-1.w1": 1}
	cases := []struct {
		name string
		role string
		step Step
	}{
		// the steps a member or a reader runs, as the bench user
		{name: "take", role: redisacl.Member, step: TakeStep(sprint.TakeReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, As: "m1", Gens: gens})},
		{name: "finish", role: redisacl.Member, step: FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, As: "m1", Gens: gens})},
		{name: "finish with usage", role: redisacl.Member, step: FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, As: "m1", Gens: gens, Usage: usage})},
		{name: "finish failed with usage", role: redisacl.Member, step: FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, As: "m1", Gens: gens, Failed: true, Report: "provider failure: 529", Usage: usage})},
		{name: "read begin", role: redisacl.Member, step: ReadStep(sprint.ReadReq{Sel: sprint.Sel{Limit: 1}, As: "reader-a", Begin: true})},
		{name: "read ok with usage", role: redisacl.Member, step: ReadStep(sprint.ReadReq{Sel: sprint.Sel{Limit: 1}, As: "reader-a", Verdict: "ok", Usage: usage})},
		{name: "read broken with usage", role: redisacl.Member, step: ReadStep(sprint.ReadReq{Sel: sprint.Sel{Limit: 1}, As: "reader-a", Verdict: "broken", Usage: usage})},
		{name: "read return with usage", role: redisacl.Member, step: ReadStep(sprint.ReadReq{Sel: sprint.Sel{IDs: []string{"s1-1.r1.reader-a"}}, As: "reader-a", Return: true, Reason: "no verdict", Usage: usage})},
		// the coordinator's steps read the whole route set
		{name: "deal", role: redisacl.Coordinator, step: DealStep(sprint.DealReq{})},
		{name: "ask", role: redisacl.Coordinator, step: AskStep(sprint.AskReq{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			keys := readKeys(t, tc.step)
			assert.Empty(t, outside(keys, rolePatterns(t, tc.role)), "%s, run as the %s role, reads keys outside its rendered patterns", tc.name, tc.role)
		})
	}
}

// The check bites: the full route set a dealing step reads holds a key the member
// may not read (a tier's array), which is why a worker's step prices with
// Step.Prices and never Step.Routes.
func TestTheFullRouteSetIsOutsideTheMembersKeys(t *testing.T) {
	t.Parallel()
	keys := readKeys(t, Step{Routes: true})
	assert.Contains(t, outside(keys, rolePatterns(t, redisacl.Member)), "tier:flash")
	assert.Empty(t, outside(readKeys(t, Step{Prices: true}), rolePatterns(t, redisacl.Member)))
}
