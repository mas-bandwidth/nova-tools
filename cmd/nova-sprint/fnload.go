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
//
// fn deploy --redis <addr> [--want <sha>] [--dry-run] is the deploy path's
// verb (#2937; the rowan-tools fn-load play, the last play of
// `make -C fleet tools`): fn load, then a read-back of the library the store
// holds and FCALL ns_ping 0, and one receipt line
//
//	FN RECEIPT at=<utc> store=<a> load=LOADED|UNCHANGED sha=<s> version=<v> ping=PONG
//
// It refuses (exit 1, one FN REFUSED line naming the remedy) on a library
// digest mismatch: --want is not the digest this binary embeds (the deploy
// declared another build; nothing is loaded), or the store holds another
// library after the load (a second deployer replaced it). --dry-run changes
// nothing: FN OK when the store is current, FN WOULD-LOAD when it is missing
// or stale. fn sum prints the embedded digest, so a deploy can read the
// declared build's --want from that build's own binary.
package main

import (
	"context"
	"io"
	"regexp"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

func init() {
	register(Verb{
		Name:    "fn",
		Summary: "load (idempotent, version-checked), check, deploy (load + digest read-back + receipt) or sum the nova_sprint function library",
		Run:     runFn,
	})
}

func runFn(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "fn", "want load or check (each with --redis <addr>), deploy (--redis <addr> [--want <sha>] [--dry-run]) or sum")
	}
	sub := args[0]
	switch sub {
	case "deploy":
		return runFnDeploy(ctx, args[1:], out, errOut)
	case "sum":
		return runFnSum(args[1:], out, errOut)
	case "load", "check":
	default:
		return refuse(errOut, "fn", "unknown subverb "+sub+"; want load, check, deploy or sum")
	}
	fs := capacityFlags("fn " + sub)
	redisAddr := fs.String("redis", redisDefault(), "")
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

// wantSum is the shape of a library digest (fn.Sum): 16 lowercase hex digits.
var wantSum = regexp.MustCompile(`^[0-9a-f]{16}$`)

// fnNow is the receipt clock; tests may set it.
var fnNow = time.Now

func runFnSum(args []string, out, errOut io.Writer) int {
	if len(args) != 0 {
		return refuse(errOut, "fn sum", "takes no arguments; it prints the digest of the library this binary embeds")
	}
	source, err := fn.Source()
	if err != nil {
		return refuse(errOut, "fn sum", err.Error())
	}
	io.WriteString(out, "SUM "+fn.Library+" sha="+fn.Sum(source)+"\n")
	return 0
}

func runFnDeploy(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("fn deploy")
	redisAddr := fs.String("redis", redisDefault(), "")
	want := fs.String("want", "", "")
	dry := fs.Bool("dry-run", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fn deploy", err.Error())
	}
	if *redisAddr == "" {
		return refuse(errOut, "fn deploy", "--redis <addr> is required, for example --redis 127.0.0.1:6379")
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fn deploy", "takes no arguments after the flags")
	}
	if *want != "" && !wantSum.MatchString(*want) {
		return refuse(errOut, "fn deploy", "--want must be a library digest, 16 lowercase hex digits (nova-sprint fn sum prints one)")
	}
	source, err := fn.Source()
	if err != nil {
		return refuse(errOut, "fn deploy", err.Error())
	}
	sha := fn.Sum(source)
	addr := oneline.Escape(*redisAddr)
	if *want != "" && *want != sha {
		io.WriteString(out, "FN REFUSED store="+addr+" reason=digest-mismatch have="+sha+" want="+*want+
			" remedy=run the declared build's nova-sprint (make -C fleet tools), or declare the build this one is\n")
		return 1
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "fn deploy", err.Error())
	}
	defer st.Close()
	if *dry {
		state, err := fn.Check(ctx, st.Client())
		if err != nil {
			return fnDeployFailed(out, addr, "check", err)
		}
		switch {
		case state.Missing:
			io.WriteString(out, "FN WOULD-LOAD store="+addr+" got=MISSING sha="+sha+"\n")
		case state.Loaded != state.Want:
			io.WriteString(out, "FN WOULD-LOAD store="+addr+" got=STALE loaded="+state.Loaded+" sha="+sha+"\n")
		case !state.OK():
			io.WriteString(out, "FN REFUSED store="+addr+" reason=noping sha="+sha+" ping="+oneline.Escape(state.Ping)+
				" remedy=the store holds this library but ns_ping does not answer PONG; read its log\n")
			return 1
		default:
			io.WriteString(out, "FN OK store="+addr+" sha="+sha+" ping=PONG\n")
		}
		return 0
	}
	_, loaded, err := fn.Ensure(ctx, st.Client())
	if err != nil {
		return fnDeployFailed(out, addr, "load", err)
	}
	if err := store.DeployACLs(ctx, st.Client()); err != nil {
		return fnDeployFailed(out, addr, "deploy-acls", err)
	}
	return fnDeployReadBack(ctx, st.Client(), addr, sha, loaded, out)
}

// fnDeployReadBack is the step after the load: the store must hold exactly
// the embedded library (digest sha) and its ns_ping must answer PONG, or the
// deploy refuses with no receipt.
func fnDeployReadBack(ctx context.Context, client *redis.Client, addr, sha string, loaded bool, out io.Writer) int {
	state, err := fn.Check(ctx, client)
	if err != nil {
		return fnDeployFailed(out, addr, "read-back", err)
	}
	if state.Missing || state.Loaded != sha {
		got := state.Loaded
		if state.Missing {
			got = "none"
		}
		io.WriteString(out, "FN REFUSED store="+addr+" reason=digest-mismatch loaded="+got+" want="+sha+
			" remedy=another deployer holds the store; find it (only the coordinator's make -C fleet tools loads), then rerun\n")
		return 1
	}
	if !state.OK() {
		io.WriteString(out, "FN REFUSED store="+addr+" reason=noping sha="+sha+" ping="+oneline.Escape(state.Ping)+
			" remedy=the store holds this library but ns_ping does not answer PONG; read its log\n")
		return 1
	}
	word := "UNCHANGED"
	if loaded {
		word = "LOADED"
	}
	io.WriteString(out, "FN RECEIPT at="+fnNow().UTC().Format("2006-01-02T15:04:05Z")+" store="+addr+
		" load="+word+" sha="+sha+" version="+oneline.Field(buildinfo.Version(version))+" ping=PONG\n")
	return 0
}

func fnDeployFailed(out io.Writer, addr, step string, err error) int {
	io.WriteString(out, "FN REFUSED store="+addr+" reason="+step+"-failed err="+oneline.Escape(err.Error())+
		" remedy=the store refused the "+step+"; check the address and that this seat's ACL user may FUNCTION LOAD and LIST\n")
	return 1
}
