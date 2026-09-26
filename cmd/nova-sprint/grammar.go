// The one grammar (nova-tools #4352 A): `nova-sprint <noun> <verb> [--flags]
// [positionals]`. The same flag means the same thing on every verb
// (internal/nsprint/verbflag holds the vocabulary and the retired spellings),
// lists are comma-separated, a worker is friend:<f> or bench:<b>, and the
// actor a receipt records is the seat, never a flag. The class test in
// internal/ci (cli-style) walks every verb's flag set and holds it here.
package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// seatActor is who a receipt says did it: the seat's friend (NOVA_FRIEND,
// which every harness exports), else the --seat name, else the login user,
// else nova-sprint. --actor and --by are retired (#4352 A): under a harness
// the actor is the seat by construction, and a coordinator shell is its user.
func seatActor() string {
	for _, v := range []string{os.Getenv(seatEnv), seatcred.Selected(), os.Getenv("USER")} {
		if v != "" {
			return v
		}
	}
	return "nova-sprint"
}

// oneID is --ids with exactly one id (the verbs that act on one card): the
// id, or "" when the list is empty or longer.
func oneID(ids string) string {
	list := verbflag.List(ids)
	if len(list) != 1 {
		return ""
	}
	return list[0]
}

// consumerOrName reads a worker from --as: friend:<f> or bench:<b>, or a
// bare name under a verb whose noun already says the kind (capacity friend
// <f> is `capacity friend --as <f>` as well as `--as friend:<f>`).
func consumerOrName(kind, s string) (taskcard.Consumer, error) {
	if k, err := taskcard.ParseConsumer(s); err == nil {
		return k, nil
	}
	return taskcard.ParseConsumer(kind + ":" + s)
}
