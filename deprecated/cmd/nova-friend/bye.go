package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

// runBye is `bye --as <you>`: DEL your beat, so you read down at once. It
// is not fenced on a session: it is the friend saying so, from any window.
// Registration and dealt work stay for the next here.
func runBye(ctx context.Context, e env, args []string, out, errOut io.Writer) int {
	const verb = "bye"
	fs := newFlags(verb)
	redisAddr := fs.String("redis", "", redisHelp)
	as := fs.String("as", "", asHelp)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	name, err := e.actor(*as)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	st, err := openStore(ctx, e.redis(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	reply, err := st.Client().FCall(ctx, life.FunctionBye, nil, name, "", name, "").Result()
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	w := words(reply)
	if word(w, 0) != "DOWN" {
		return refuse(errOut, verb, "bye: "+strings.Join(w, " "))
	}
	was := "down"
	if word(w, 1) == "1" {
		was = "up"
	}
	fmt.Fprintf(out, "FRIEND BYE as=%s was=%s\n", name, was)
	return 0
}
