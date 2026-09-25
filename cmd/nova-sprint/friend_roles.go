package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

func runFriendRoles(ctx context.Context, args []string, out, errOut io.Writer) int {
	if hasAssignFlag(args, "--actor") {
		fmt.Fprintln(errOut, "REFUSED actor: --actor is not supported; actor is NOVA_FRIEND")
		return 2
	}
	fs, addr := lifeFlags("friend roles")
	set := fs.String("set", "", "friend whose roles are changed")
	roles := fs.String("roles", "", "comma separated may-hold,builder,coordinator,reader")
	idem := fs.String("idem", "", "idempotency marker")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "friend roles", err.Error())
	}
	if fs.NArg() != 0 || *set == "" || !hasAssignFlag(args, "--roles") {
		return refuse(errOut, "friend roles", "needs --set <friend> --roles <csv>")
	}
	actor := os.Getenv(seatEnv)
	if actor == "" {
		fmt.Fprintln(errOut, "REFUSED actor: NOVA_FRIEND is empty; run from a seat that exports it (nova-tools#2929)")
		return 2
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "friend roles", err.Error())
	}
	defer st.Close()
	reply, err := st.Client().FCall(ctx, "ns_friend_roles", nil, *set, *roles, actor, *idem).Result()
	if err != nil {
		return refuse(errOut, "friend roles", err.Error())
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return refuse(errOut, "friend roles", fmt.Sprintf("unexpected reply %T", reply))
	}
	switch status := fmt.Sprint(values[0]); status {
	case "OK":
		fmt.Fprintf(out, "ROLES %s %s bootstrap=%s\n", values[1], values[2], values[3])
		return 0
	case "REFUSED":
		fmt.Fprintf(errOut, "REFUSED roles: %s is not coordinator\n", actor)
		return 1
	case "UNKNOWN", "BADROLE":
		if len(values) < 2 {
			return refuse(errOut, "friend roles", fmt.Sprintf("malformed %s reply", status))
		}
		fmt.Fprintf(errOut, "%s %s\n", status, values[1])
		return 1
	case "LASTCOORD":
		fmt.Fprintln(errOut, status)
		return 1
	default:
		return refuse(errOut, "friend roles", status)
	}
}
