package main

// `nova-sprint bench ls` prints the bench registry with its role column
// (nova-tools #3634): one BENCH line per registered bench, sorted by name,
// then one BENCHES receipt line with the counts per role. Exit 0; exit 1 when
// a stored role is neither friends nor fleet (the bench is named on its line
// and on the REFUSED line); exit 2 on usage or no Redis.

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchrole"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func runBenchLs(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("bench ls")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "bench ls", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "bench ls", "takes flags, not positional arguments")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "bench ls", err.Error())
	}
	defer st.Close()
	return benchLs(ctx, st, out, errOut)
}

func benchLs(ctx context.Context, st *store.Store, out, errOut io.Writer) int {
	rows, err := benchrole.List(ctx, st.Client())
	if err != nil {
		return refuse(errOut, "bench ls", err.Error())
	}
	counts := map[string]int{}
	var bad []string
	for _, r := range rows {
		fmt.Fprintln(out, r.Line())
		if !r.Valid {
			bad = append(bad, r.Bench+"="+r.Role)
			continue
		}
		counts[r.Role]++
	}
	fmt.Fprintf(out, "BENCHES n=%d fleet=%d friends=%d\n", len(rows), counts[benchrole.Fleet], counts[benchrole.Friends])
	if len(bad) > 0 {
		fmt.Fprintf(errOut, "REFUSED bench ls: role not friends or fleet: %s; set it with capacity bench --role friends|fleet\n",
			strings.Join(bad, ","))
		return 1
	}
	return 0
}
