package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/unit"
)

func init() {
	register(Verb{
		Name:    "consume",
		Summary: "run sprint stream consumers: ok-to-friend",
		Run:     runConsume,
	})
}

func runConsume(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "ok-to-friend" {
		return refuse(errOut, "consume", "want ok-to-friend --redis <addr>")
	}
	fs := flag.NewFlagSet("consume ok-to-friend", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	redisAddr := fs.String("redis", "", "")
	inst := fs.String("instance", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, "consume ok-to-friend", err.Error())
	}
	if *redisAddr == "" {
		return refuse(errOut, "consume ok-to-friend", "--redis <addr> is required")
	}

	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "consume ok-to-friend", err.Error())
	}
	defer st.Close()

	lease, err := unit.AcquireLease(ctx, st, "lease:consume:ok-to-friend", 6*time.Second)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		return 2
	}
	defer lease.Release(ctx)

	consumerName := *inst
	if consumerName == "" {
		consumerName = lease.Instance()
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
			if err := lease.Renew(ctx); err != nil {
				return 3 // fenced
			}
			sprints, err := st.Client().SMembers(ctx, "sprints").Result()
			if err != nil {
				continue
			}
			for _, s := range sprints {
				of := &consume.OkFriend{
					Store:    st,
					Sprint:   s,
					Consumer: consumerName,
					Actor:    "consumer",
					CICut: func(c context.Context, req consume.CICut) error {
						_, err := ci.Cut(c, st, ci.CutRequest{
							Sprint: req.Sprint,
							Repo:   req.Repo,
							PR:     req.PR,
							Head:   req.Head,
							Base:   req.Base,
							Leg:    "go",
							Actor:  "consumer",
						})
						return err
					},
				}
				go func(sprintName string) {
					_ = of.Run(ctx)
				}(s)
			}
		}
	}
}
