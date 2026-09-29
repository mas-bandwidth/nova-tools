package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "friend",
		Summary: "ls friends or show one friend's details, working copies and receipts (the joy lens)",
		Run:     runFriend,
	})
}

func runFriend(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "friend", "want ls or show")
	}
	switch args[0] {
	case "ls", "list":
		return runFriendLs(ctx, args[1:], out, errOut)
	case "show":
		return runFriendShow(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "friend", fmt.Sprintf("unknown subverb %s; want ls or show", args[0]))
	}
}

func runFriendLs(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("friend ls")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "friend ls", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "friend ls", "takes flags, not positional arguments")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "friend ls", err.Error())
	}
	defer st.Close()
	return friendLs(ctx, st, out, errOut)
}

func friendLs(ctx context.Context, st *store.Store, out, errOut io.Writer) int {
	now := time.Now()
	summaries, err := friend.List(ctx, st.Client(), now)
	if err != nil {
		return refuse(errOut, "friend ls", err.Error())
	}
	fmt.Fprint(out, friend.FormatList(summaries, now))
	return 0
}

func runFriendShow(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("friend show")
	asFriend := fs.String("friend", "", "friend name to show")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "friend show", err.Error())
	}
	target := *asFriend
	if target == "" && fs.NArg() == 1 {
		target = fs.Arg(0)
	} else if fs.NArg() > 1 {
		return refuse(errOut, "friend show", "takes one friend name")
	}
	if target == "" {
		return refuse(errOut, "friend show", "want friend name as argument or --friend")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "friend show", err.Error())
	}
	defer st.Close()
	return friendShow(ctx, st, target, out, errOut)
}

func friendShow(ctx context.Context, st *store.Store, target string, out, errOut io.Writer) int {
	now := time.Now()
	detail, err := friend.Show(ctx, st.Client(), target, now)
	if err != nil {
		return refuse(errOut, "friend show", err.Error())
	}
	fmt.Fprint(out, friend.FormatShow(detail, now))
	return 0
}
