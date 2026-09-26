package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// runFriendDown is `friend down|up --as <actor> <f> [--reason r]`: the one
// writer of friend:<f>:down (#3206 PR A). Down refuses push and take; the
// reconciler\x27s deal duty moves the friend\x27s ready copies on its next pass.
func runFriendDown(ctx context.Context, on bool, args []string, out, errOut io.Writer) int {
	verb := "friend up"
	if on {
		verb = "friend down"
	}
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", "", "")
	actor := fs.String("as", "", "")
	reason := fs.String("reason", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *actor == "" {
		return refuse(errOut, verb, "--as actor is required")
	}
	if fs.NArg() != 1 {
		return refuse(errOut, verb, "want one friend name; flags precede it")
	}
	st, err := openTaskStore(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	r, err := task.FriendDown(ctx, st, fs.Arg(0), on, *reason, *actor, *idem)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	_, _ = fmt.Fprintf(out, "FRIEND %s %s\n", r.Status, fs.Arg(0))
	if r.Status != "DOWN" && r.Status != "UP" && r.Status != "SAME" {
		return 2
	}
	return 0
}

// hasAssignFlag reports whether argv carries the flag want (as --x or --x=v).
func hasAssignFlag(args []string, want string) bool {
	for _, arg := range args {
		if strings.SplitN(arg, "=", 2)[0] == want {
			return true
		}
	}
	return false
}

// quoteField keeps a stream name with spaces ("swarm: cards") one field.
func quoteField(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return s
}
