// The fn verb loads and checks the nova_sprint Redis Function library (#3196).
// Every other Redis verb FCALLs into that library, and before this verb only
// tests loaded it, so the fleet Redis answered "Function not found".
//
// fn load --redis <addr> installs the embedded library with FUNCTION LOAD
// REPLACE unless the server already holds that exact source; it prints
// LOADED or UNCHANGED and the library sha, so a converge may run it every pass.
// fn check --redis <addr> changes nothing: it prints OK, MISSING or STALE and
// exits 1 unless the loaded library is the embedded one and FCALL ns_ping 0
// answers PONG, which is the bench-conform line. On MISSING or STALE it does
// not call ns_ping at all (ping=skipped): the server's ns_ping is then not the
// embedded one and could write.
package main

import (
	"context"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "fn",
		Summary: "load (idempotent, version-checked) or check the nova_sprint function library",
		Run:     runFn,
	})
}

func runFn(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "fn", "want load or check, each with --redis <addr>")
	}
	sub := args[0]
	if sub != "load" && sub != "check" {
		return refuse(errOut, "fn", "unknown subverb "+sub+"; want load or check")
	}
	fs := capacityFlags("fn " + sub)
	redisAddr := fs.String("redis", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, "fn "+sub, err.Error())
	}
	if *redisAddr == "" {
		return refuse(errOut, "fn "+sub, "--redis <addr> is required, for example --redis 127.0.0.1:6379")
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fn "+sub, "takes no arguments after the flags")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "fn "+sub, err.Error())
	}
	defer st.Close()
	if sub == "load" {
		sum, loaded, err := fn.Ensure(ctx, st.Client())
		if err != nil {
			return refuse(errOut, "fn load", err.Error())
		}
		word := "UNCHANGED"
		if loaded {
			word = "LOADED"
		}
		io.WriteString(out, word+" "+fn.Library+" sha="+sum+"\n")
		return 0
	}
	state, err := fn.Check(ctx, st.Client())
	if err != nil {
		return refuse(errOut, "fn check", err.Error())
	}
	ping := oneline.Escape(state.Ping)
	switch {
	case state.Missing:
		io.WriteString(out, "MISSING "+fn.Library+" want="+state.Want+" ping="+ping+"\n")
		return 1
	case state.Loaded != state.Want:
		io.WriteString(out, "STALE "+fn.Library+" loaded="+state.Loaded+" want="+state.Want+" ping="+ping+"\n")
		return 1
	case !state.OK():
		io.WriteString(out, "NOPING "+fn.Library+" sha="+state.Want+" ping="+ping+"\n")
		return 1
	}
	io.WriteString(out, "OK "+fn.Library+" sha="+state.Want+" ping=PONG\n")
	return 0
}
