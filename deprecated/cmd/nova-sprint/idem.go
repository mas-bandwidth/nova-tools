// The idem verb is a friend's typed resolution of an ambiguous idempotency
// key (#2930 rev 5, control 8). The reconciler never re-reads the forge: a
// PR open whose outcome Redis cannot prove (a 422 that is not on the
// validation allowlist, or an open pending past open_ms) is ambiguous:* and
// terminal for the machine, with one pr-ambiguous item in s:<S>:unresolved.
// A friend looks, then:
//
//	nova-sprint idem resolve --redis <addr> --sprint <S> --key <k> \
//	    --was <ambiguous:who:at_ms> (--url <u> | --none) --who <friend>
//
// --url records the PR (one receipt, the item deleted); --none deletes the
// key and the item, so the next open begins fresh and opens once. The fence
// is compare-and-set on --was: ns_idem_resolve writes only when the stored
// value is ambiguous:* and equals --was byte for byte.
//
// Lines and exits: 0 prints RESOLVED sprint=<S> key=<k> url=<u>|none
// receipt=<id>; 1 prints STATE sprint=<S> key=<k> value=<stored>|absent and
// wrote nothing (a stale --was, or a key that is not ambiguous); 2 could not
// run (Redis unreachable, the function library not loaded, a usage error),
// one refuse line on stderr.
package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "idem",
		Summary: "resolve an ambiguous idem key by compare-and-set on --was (exit 0 RESOLVED, 1 STATE, 2 could not run)",
		Run:     runIdem,
	})
}

const idemResolveUsage = "--redis <addr> --sprint <S> --key <k> --was <ambiguous:who:at_ms> (--url <u> | --none) --who <friend>"

func runIdem(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "idem", "want resolve "+idemResolveUsage)
	}
	if args[0] != "resolve" {
		return refuse(errOut, "idem", "unknown subverb "+args[0]+"; want resolve")
	}
	return runIdemResolve(ctx, args[1:], out, errOut)
}

func runIdemResolve(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "idem resolve"
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), "")
	sprint := fs.String("sprint", "", "")
	key := fs.String("key", "", "")
	was := fs.String("was", "", "")
	url := fs.String("url", "", "")
	none := fs.Bool("none", false, "")
	who := fs.String("who", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error()+"; want "+idemResolveUsage)
	}
	redisStr := strings.TrimSpace(*redisAddr)
	sprintStr := strings.TrimSpace(*sprint)
	keyStr := strings.TrimSpace(*key)
	whoStr := strings.TrimSpace(*who)
	wasStr := strings.TrimSpace(*was)
	urlStr := strings.TrimSpace(*url)
	switch {
	case fs.NArg() > 0:
		return refuse(errOut, verb, "takes flags, not positional arguments: "+idemResolveUsage)
	case redisStr == "" || sprintStr == "" || keyStr == "" || whoStr == "":
		return refuse(errOut, verb, "--redis, --sprint, --key and --who are required: "+idemResolveUsage)
	case wasStr == "":
		return refuse(errOut, verb, "--was is required: the ambiguous:<who>:<at_ms> value a STATE line printed")
	case (urlStr == "") == !*none:
		return refuse(errOut, verb, "want exactly one of --url <u> and --none")
	}
	st, err := store.Open(ctx, redisStr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	res, err := reconcile.ResolveIdem(ctx, st, reconcile.IdemResolveRequest{
		Sprint: sprintStr, Key: keyStr, Was: wasStr, URL: urlStr, None: *none, Who: whoStr,
	})
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	switch res.Status {
	case "RESOLVED":
		shown := urlStr
		if *none {
			shown = "none"
		}
		fmt.Fprintf(out, "RESOLVED sprint=%s key=%s url=%s receipt=%s\n", sprintStr, keyStr, shown, res.Receipt)
		return 0
	case "STATE":
		value := res.Value
		if value == "" {
			value = "absent"
		}
		fmt.Fprintf(out, "STATE sprint=%s key=%s value=%s\n", sprintStr, keyStr, value)
		return 1
	default:
		return refuse(errOut, verb, fmt.Sprintf("unexpected reply %s code=%d", res.Status, res.Code))
	}
}
