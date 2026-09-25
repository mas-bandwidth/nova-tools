package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// runLandFreeze is `land freeze <repo> <base> --reason <t>` and `land thaw <repo> <base>`
// (#3139 rev 7 §7.7, §8.4, B11): stop and resume publishing on one base; gating continues,
// and a frozen base still publishes its revert train. A red tip receipt freezes and a green
// one thaws by themselves (ns_tip_tick); these verbs are the hand path. Exits 0 (done, or
// already in that state), 2 (usage), 6 (no Redis).
func runLandFreeze(ctx context.Context, verb string, args []string, out, errOut io.Writer) int {
	name := "land " + verb
	usage := name + " <repo> <base> --redis <addr>"
	if verb == "freeze" {
		usage = name + " <repo> <base> --reason <text> --redis <addr>"
	}
	if len(args) < 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return refuse(errOut, name, "usage: "+usage)
	}
	repo, base := args[0], args[1]
	fs := taskFlags(name)
	reason := fs.String("reason", "", "")
	redisAddr := fs.String("redis", "", "")
	by := fs.String("by", "", "")
	if err := fs.Parse(args[2:]); err != nil {
		return refuse(errOut, name, err.Error()+"; usage: "+usage)
	}
	if fs.NArg() > 0 {
		return refuse(errOut, name, fmt.Sprintf("takes <repo> <base> and flags, not %q; usage: %s", fs.Arg(0), usage))
	}
	if verb == "freeze" && strings.TrimSpace(*reason) == "" {
		return refuse(errOut, name, "needs --reason <text>; usage: "+usage)
	}
	addr := *redisAddr
	if addr == "" {
		addr = os.Getenv("NOVA_REDIS_ADDR")
	}
	if addr == "" {
		addr = os.Getenv("REDIS_ADDR")
	}
	if addr == "" {
		return refuse(errOut, name, "--redis <addr> is required")
	}
	actor := *by
	if actor == "" {
		actor = os.Getenv("USER")
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
		return 6
	}
	defer st.Close()
	client := st.Client()
	if _, _, err := fn.Ensure(ctx, client); err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
		return 6
	}
	if verb == "freeze" {
		status, detail, err := land.CallFreeze(ctx, client, repo, base, *reason, actor)
		if err != nil {
			return refuse(errOut, name, err.Error())
		}
		if status == "ALREADY" {
			fmt.Fprintf(out, "FROZEN %s %s already reason=%s\n", oneline.Field(repo), oneline.Field(base), oneline.Field(detail))
			return 0
		}
		fmt.Fprintf(out, "FROZEN %s %s reason=%s\n", oneline.Field(repo), oneline.Field(base), oneline.Field(*reason))
		return 0
	}
	status, source, err := land.CallThaw(ctx, client, repo, base, actor)
	if err != nil {
		return refuse(errOut, name, err.Error())
	}
	if status == "NOTFROZEN" {
		fmt.Fprintf(out, "THAW %s %s not frozen\n", oneline.Field(repo), oneline.Field(base))
		return 0
	}
	fmt.Fprintf(out, "THAW %s %s was=%s\n", oneline.Field(repo), oneline.Field(base), oneline.Field(source))
	return 0
}
