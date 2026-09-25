// The dev-red verb and duty (nova-tools #3629): when a base branch's own CI
// at its tip is red, the reconciler holds every stream merge into that base
// (land:<repo>:<base>:red, the key the lander checks through
// land.RedBlocked) and pushes ONE fix task to the coordinator's queue naming
// the failing check; the hold clears on green.
//
//	nova-sprint dev-red status --repo <r> --base <b> --redis <addr>
//	nova-sprint dev-red check  --repo <r> --base <b> --redis <addr> [--sprint <S>] [--to <friend>]
//	nova-sprint dev-red watch|unwatch --repo <r> --base <b> --redis <addr>
//
// status prints RED <check> <sha> task=<id> or GREEN <base> and exits 0
// either way (the row is the answer; 6 is no Redis). check runs one duty
// pass over that base now and prints its DEVRED receipt. watch adds the
// base to devred:bases, the set the reconciler's dev-red duty walks every
// pass (internal/nsprint/reconcile). The duty reads only Redis: the CI
// record ci:<repo>:<sha>, the GitHub leg ci:<repo>:<sha>:gh that
// `nova-sprint ci github` writes from the webhook stream (#3597), then the
// gated receipt. It never reads GitHub.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// devRedTo is the coordinator's queue the fix task goes to; NOVA_DEVRED_TO
// overrides it.
const devRedTo = "rowan"

// devRedOwner is the forge owner of every repo the fleet lands;
// NOVA_GH_OWNER overrides it. The duty reads no forge (#3597); the
// done-already issue closer (#3919) names repos with it.
const devRedOwner = "mas-bandwidth"

func init() {
	register(Verb{
		Name:    "dev-red",
		Summary: "dev-red status|check|watch|unwatch --repo <r> --base <b>: hold stream merges while the base's own CI is red, one fix task to the coordinator",
		Run:     runDevRed,
	})
	registerReconcileDuty("dev-red", func(st *store.Store) (reconcileDuty, error) {
		return devRedDuty(st, nil, "", devRedEnv("NOVA_DEVRED_TO", devRedTo)), nil
	})
}

func devRedEnv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// devRedDuty builds the duty over a store: the push goes through
// task.Push (kind fix, front of the queue) into the sprint given or, when
// sprint is "", the first of sprint:order.
func devRedDuty(st *store.Store, bases []land.RepoBase, sprint, to string) *reconcile.DevRed {
	d := &reconcile.DevRed{
		Client: st.Client(), Bases: bases, To: to,
		Push: func(ctx context.Context, t reconcile.FixTask) (string, error) {
			sprint, err := devRedSprint(ctx, st.Client(), sprint)
			if err != nil {
				return "", err
			}
			res, err := task.Push(ctx, st, task.PushRequest{
				Sprint: sprint, ID: t.ID, Kind: task.KindFix, Title: t.Title,
				Effects: task.EffectsExternal, Repo: t.Repo, Head: t.SHA, Ref: t.Base,
				To: t.To, Front: true, Actor: "dev-red", ErrOut: io.Discard,
			})
			if err != nil {
				return "", err
			}
			switch res {
			case task.PushCreated, task.PushExists, task.PushClosed:
				return t.ID, nil
			}
			return "", fmt.Errorf("task push %s: %s", t.ID, res)
		},
	}
	return d
}

// devRedSprint is the sprint the fix task goes to: the one given, else the
// first of sprint:order.
func devRedSprint(ctx context.Context, c *redis.Client, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	names, err := c.ZRange(ctx, "sprint:order", 0, 0).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	if len(names) == 0 {
		return "", task.ErrNoSprint
	}
	return names[0], nil
}

func runDevRed(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "dev-red", "want status, check, watch or unwatch, each with --repo <r> --base <b> --redis <addr>")
	}
	sub := args[0]
	switch sub {
	case "status", "check", "watch", "unwatch":
	default:
		return refuse(errOut, "dev-red", "unknown subverb "+sub+"; want status, check, watch or unwatch")
	}
	name := "dev-red " + sub
	fs := taskFlags(name)
	redisAddr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	base := fs.String("base", "dev", "")
	sprint := fs.String("sprint", "", "")
	to := fs.String("to", devRedEnv("NOVA_DEVRED_TO", devRedTo), "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, name, err.Error())
	}
	if *repo == "" || *base == "" || fs.NArg() != 0 {
		return refuse(errOut, name, "needs --repo <r> and --base <b>, nothing after the flags")
	}
	st, err := store.Open(ctx, lifeAddr(*redisAddr))
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: connect redis: %v\n", name, err)
		return 6
	}
	defer st.Close()
	c := st.Client()
	rb := land.RepoBase{Repo: *repo, Base: *base}
	switch sub {
	case "status":
		red, ok, err := land.ReadRed(ctx, c, rb.Repo, rb.Base)
		if storeDown(errOut, name, err) {
			return 6
		}
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
			return 1
		}
		if ok {
			fmt.Fprintln(out, red.Line())
		} else {
			fmt.Fprintf(out, "GREEN %s/%s\n", rb.Repo, rb.Base)
		}
		return 0
	case "watch", "unwatch":
		member := rb.Repo + "/" + rb.Base
		var n int64
		if sub == "watch" {
			n, err = c.SAdd(ctx, reconcile.BasesKey, member).Result()
		} else {
			n, err = c.SRem(ctx, reconcile.BasesKey, member).Result()
		}
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
			return 1
		}
		fmt.Fprintf(out, "%s %s changed=%d\n", strings.ToUpper(sub), member, n)
		return 0
	}
	d := devRedDuty(st, []land.RepoBase{rb}, *sprint, *to)
	outs, err := d.Pass(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
		return 1
	}
	code := 0
	for _, o := range outs {
		fmt.Fprintln(out, o.Line())
		if o.Err != nil {
			code = 1
		}
	}
	return code
}
