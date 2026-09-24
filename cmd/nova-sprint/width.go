package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
	"github.com/redis/go-redis/v9"
)

func init() {
	register(Verb{
		Name:    "width",
		Summary: "read only width per friend and fleet: slots, working, deficit, eligible, idle",
		Run:     runWidth,
	})
	registerReconcileDuty("width", func(st *store.Store) (reconcileDuty, error) {
		return &width.Duty{Store: st}, nil
	})
}

func runWidth(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("width")
	addr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	sprint := fs.String("sprint", "", "")
	_ = sprint
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "width", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "width", "takes no arguments")
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(errOut, "width", err.Error())
	}
	defer st.Close()

	client := st.Client()
	timeVal, err := client.Time(ctx).Result()
	if err != nil {
		return refuse(errOut, "width", err.Error())
	}
	nowMs := timeVal.UnixMilli()

	if *as != "" {
		fsData, ok, err := width.ReadFillstate(ctx, st, *as)
		if err != nil {
			return refuse(errOut, "width", err.Error())
		}
		if !ok {
			return refuse(errOut, "width", fmt.Sprintf("friend %s has no fillstate", *as))
		}
		fmt.Fprintln(out, fsData.Line(nowMs))
		return 0
	}

	friends, err := client.SMembers(ctx, "friends").Result()
	if err != nil {
		return refuse(errOut, "width", err.Error())
	}
	sort.Strings(friends)

	if len(friends) == 0 {
		fmt.Fprintln(out, width.FleetLine(0, 0, 0))
		return 0
	}

	pipe := client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(friends))
	for i, f := range friends {
		cmds[i] = pipe.HGetAll(ctx, width.FillstateKey(f))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return refuse(errOut, "width", err.Error())
	}

	fleetWorking := 0
	fleetSlots := 0
	fleetDeficit := 0

	for i, f := range friends {
		m := cmds[i].Val()
		if len(m) == 0 {
			continue
		}
		fsData := width.ParseFillstate(f, m)
		fmt.Fprintln(out, fsData.Line(nowMs))
		fleetWorking += fsData.Working
		fleetSlots += fsData.Slots
		fleetDeficit += fsData.Deficit
	}

	fmt.Fprintln(out, width.FleetLine(fleetWorking, fleetSlots, fleetDeficit))
	return 0
}

func splitNames(csv string) []string {
	var out []string
	for _, name := range strings.Split(csv, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}
