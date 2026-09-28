package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// runAway is `away <name> --reason <r> --as <actor>` (on) and `back <name>
// --as <actor>` (off): the one writer of friend:<f>:down (ns_friend_down).
// An away friend gets no push, take or copy; the reconciler's deal duty
// moves its ready copies on its next pass.
func runAway(ctx context.Context, e env, on bool, args []string, out, errOut io.Writer) int {
	verb := "back"
	if on {
		verb = "away"
	}
	fs := newFlags(verb)
	redisAddr := fs.String("redis", "", redisHelp)
	as := fs.String("as", "", asHelp)
	reason := ""
	if on {
		fs.StringVar(&reason, "reason", "", "why the friend is away (required)")
	}
	name, err := parseNamed(fs, args)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	actor, err := e.actor(*as)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if on && strings.TrimSpace(reason) == "" {
		return refuse(errOut, verb, "--reason wants why "+name+" is away")
	}
	st, err := openStore(ctx, e.redis(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	ok, err := registered(ctx, st.Client(), name)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if !ok {
		return refused(errOut, verb, unregistered(name))
	}
	r, err := task.FriendDown(ctx, st, name, on, reason, actor, "")
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	switch r.Status {
	case "DOWN", "UP", "SAME":
	default:
		return refuse(errOut, verb, fmt.Sprintf("%s: %s %s", verb, r.Status, strings.Join(r.Args, " ")))
	}
	changed := yesNo(r.Status != "SAME")
	if on {
		fmt.Fprintf(out, "FRIEND AWAY name=%s reason=%s changed=%s\n", name, quoteField(reason), changed)
	} else {
		fmt.Fprintf(out, "FRIEND BACK name=%s changed=%s\n", name, changed)
	}
	return 0
}
