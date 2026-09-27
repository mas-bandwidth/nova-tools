package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func cmdMember(args []string, stdout, stderr io.Writer) int {
	const verb = "member create"
	if len(args) == 0 || args[0] != "create" {
		return refuse(stderr, "member", "wants create <table> <id>")
	}
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	write, receipt := writeFlags(fs)
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 {
		return refuse(stderr, verb, "wants a table and a new member ID: member create <table> <id>")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	if err := ntable.MemberCreate(ctx, c, pos[0], pos[1], *write); err != nil {
		return storeRefusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE MEMBER CREATE table=%s member=%s trips=%d\n", pos[0], field(pos[1]), trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	const verb = "check"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: check <table>")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	report, err := ntable.Check(ctx, c, pos[0])
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE CHECK table=%s epoch=%d revision=%d members=%d cells=%d trips=%d\n", pos[0], report.Epoch, report.Revision, report.Members, report.Cells, trips.N())
	return 0
}

func cmdMemberFind(args []string, stdout, stderr io.Writer) int {
	const verb = "member find"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 {
		return refuse(stderr, verb, "wants a table and a member ID: member find <table> <id>")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	loc, err := ntable.MemberFind(ctx, c, pos[0], pos[1])
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE MEMBER table=%s member=%s state=%s", pos[0], field(pos[1]), loc.State)
	if loc.State == "placed" {
		fmt.Fprintf(stdout, " row=%s col=%s", field(loc.Row), loc.Column)
	}
	fmt.Fprintf(stdout, " epoch=%d revision=%d trips=%d\n", loc.Epoch, loc.Revision, trips.N())
	return 0
}
