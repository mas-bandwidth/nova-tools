package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// runCardOwner binds the actual local harness process after launch. A child
// UUID is not a pid. The current lease token must come from the copy's claim.
// Binding never extends the startup lease; the loop's independent observation
// is what renews it. No binding is changed within an attempt.
func runCardOwner(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "card owner"
	fs := verbflag.New(verb)
	addr := fs.String("redis", redisDefault(), "")
	as := fs.String("as", "", "")
	id := fs.String("id", "", "")
	token := fs.String("token", "", "")
	pid := fs.Int("pid", 0, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 || !taskcard.IsCopy(*id) || *token == "" || *pid <= 0 || *pid == os.Getpid() {
		return refuse(errOut, verb, "want --as friend:<f> --id <copy> --token <claim-token> --pid <live-harness-pid>")
	}
	asConsumer, err := friendConsumer(*as)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if _, err := friendActor(asConsumer.Name); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	host, err := os.Hostname()
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	observed := life.ProbeProcess(*pid)
	if observed.Err != nil || observed.Absent || observed.Start == "" {
		return refuse(errOut, verb, fmt.Sprintf("cannot bind pid=%d: process absent or identity unknown (%v)", *pid, observed.Err))
	}
	st, err := store.OpenSingle(ctx, taskAddr(*addr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	owner := taskcard.ProcessOwner{Host: host, PID: *pid, Start: observed.Start}
	if err := taskcard.BindOwner(ctx, st.Client(), asConsumer, *id, *token, owner); err != nil {
		if why, ok := taskcard.IsRefused(err); ok {
			fmt.Fprintf(out, "CARD OWNER REFUSED id=%s why=%s\n", *id, quoteField(why))
			return moveRefusedCode(why)
		}
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(out, "CARD OWNER id=%s host=%s pid=%d start=%s\n", *id, host, *pid, observed.Start)
	if !printFriendBeat(ctx, st.Client(), asConsumer, redisArg(*addr), out) {
		return 1
	}
	return 0
}
