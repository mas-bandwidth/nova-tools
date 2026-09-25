package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
)

func init() {
	register(Verb{
		Name:    "width",
		Summary: "read only width per friend and fleet: slots, working, deficit, eligible, idle; width fill claims up to the deficit",
		Run:     runWidth,
	})
}

func runWidth(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "fill" {
		return runWidthFill(ctx, args[1:], out, errOut)
	}
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
		rows, err := width.ReadRows(ctx, st, []string{*as})
		if err != nil {
			return refuse(errOut, "width", err.Error())
		}
		if !rows[0].Measured && !rows[0].Declared {
			return refuse(errOut, "width", fmt.Sprintf("friend %s has no fillstate and no desired slots (set them with capacity friend)", *as))
		}
		fmt.Fprintln(out, rows[0].Line(nowMs))
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

	rows, err := width.ReadRows(ctx, st, friends)
	if err != nil {
		return refuse(errOut, "width", err.Error())
	}

	fleetWorking := 0
	fleetSlots := 0
	fleetDeficit := 0

	for _, r := range rows {
		switch {
		case r.Measured:
			fmt.Fprintln(out, r.Line(nowMs))
			fleetWorking += r.Fillstate.Working
			fleetSlots += r.Fillstate.Slots
			fleetDeficit += r.Fillstate.Deficit
		case r.Declared:
			// Declared but not yet measured: the slots count toward the
			// fleet, working and deficit are unknown so they add nothing.
			fmt.Fprintln(out, r.Line(nowMs))
			fleetSlots += r.DesiredSlots()
		}
	}

	fmt.Fprintln(out, width.FleetLine(fleetWorking, fleetSlots, fleetDeficit))

	readBound := false
	if client.Get(ctx, "sprint:read_bound").Val() == "1" {
		readBound = true
	} else {
		for _, s := range client.SMembers(ctx, "sprints").Val() {
			if client.HGet(ctx, "s:"+s+":backpressure", "read_bound").Val() == "1" {
				readBound = true
				break
			}
		}
	}
	if !readBound {
		readers := client.SMembers(ctx, "width:readers").Val()
		if len(readers) == 0 {
			readers = client.SMembers(ctx, "readers").Val()
		}
		if len(readers) > 0 {
			allUpDeficitZero := true
			hasUpReader := false
			for _, r := range readers {
				if client.Exists(ctx, "friend:"+r+":beat").Val() == 1 {
					hasUpReader = true
					fsData, ok, _ := width.ReadFillstate(ctx, st, r)
					if !ok || fsData.Deficit > 0 {
						allUpDeficitZero = false
						break
					}
				}
			}
			if hasUpReader && allUpDeficitZero {
				for _, s := range client.SMembers(ctx, "sprints").Val() {
					for _, r := range readers {
						for _, tid := range client.ZRange(ctx, "s:"+s+":open:"+r, 0, -1).Val() {
							kind := client.HGet(ctx, "s:"+s+":task:"+tid, "kind").Val()
							if kind == "read" || kind == "review" {
								readBound = true
								break
							}
						}
						if readBound {
							break
						}
					}
					if readBound {
						break
					}
				}
			}
		}
	}
	if readBound {
		fmt.Fprintln(out, "READ-BOUND")
	}
	return 0
}

// runWidthFill is `width fill`: it claims min(deficit, eligible, --max) tasks
// for --as through ns_width_fill (task.WidthFill). --max 0 is refused: an
// unbounded claim is the fillstate deficit, so --max, when given, is positive.
// The one task store's queue lease stays `task fill` (#3206 PR A).
func runWidthFill(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("width fill")
	addr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	sprint := fs.String("sprint", "", "")
	max := fs.Int("max", 0, "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "width fill", err.Error())
	}
	if *as == "" {
		return refuse(errOut, "width fill", "--as is required")
	}
	for i, arg := range args {
		if strings.HasPrefix(arg, "--max=") {
			v, err := strconv.Atoi(strings.TrimPrefix(arg, "--max="))
			if err != nil || v <= 0 {
				return refuse(errOut, "width fill", "--max must be a positive integer")
			}
		} else if arg == "--max" {
			if i+1 >= len(args) {
				return refuse(errOut, "width fill", "--max requires an argument")
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v <= 0 {
				return refuse(errOut, "width fill", "--max must be a positive integer")
			}
		}
	}
	if *max < 0 {
		return refuse(errOut, "width fill", "--max must be a positive integer")
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "width fill", "takes no arguments")
	}
	st, err := openTaskStore(ctx, *addr)
	if err != nil {
		return refuse(errOut, "width fill", err.Error())
	}
	defer st.Close()

	if st.Client().Exists(ctx, "friend:"+*as+":desired").Val() == 0 {
		return refuse(errOut, "width fill", fmt.Sprintf("friend %s has no desired slots", *as))
	}
	res, err := task.WidthFill(ctx, st, *as, *sprint, *max, *actor, *idem)
	if err != nil {
		return refuse(errOut, "width fill", err.Error())
	}
	for _, t := range res.Tasks {
		fmt.Fprintf(out, "FILL %s kind=%s ref=%s token=%s\n", t.ID, t.Kind, t.Ref, t.Token)
	}
	fmt.Fprintf(out, "FILLED %s n=%d deficit=%d\n", *as, res.N, res.Deficit)
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
