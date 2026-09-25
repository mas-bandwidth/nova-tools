package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// runLandWriter manages exclusive writer generations for the lander (#3139 section 10.2).
// land:<repo>:<base>:writer holds gen, owner (old-loop or nova-sprint), since, by.
//
// Cutover: land writer --repo <r> --base <b> --to nova-sprint bumps gen; refuses while
// old loop's inflight count in Redis is nonzero.
// Rollback: land writer --repo <r> --base <b> --to old-loop bumps gen; ns_writer refuses it
// (REFUSED pub=<batch>) while land:<repo>:<base>:pub:active names an unresolved intent.
func runLandWriter(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("land writer")
	repo := fs.String("repo", "", "")
	base := fs.String("base", "", "")
	to := fs.String("to", "", "")
	redisAddr := fs.String("redis", "", "")
	by := fs.String("by", "", "")
	inflight := fs.Int("inflight", -1, "")

	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "land writer", err.Error())
	}
	if n := fs.NArg(); n > 0 {
		return refuse(errOut, "land writer", fmt.Sprintf("takes flags, not positional arguments: %q", fs.Arg(0)))
	}
	if *repo == "" || *base == "" {
		return refuse(errOut, "land writer", "needs --repo <repo> and --base <base>")
	}
	if *to != "" && *to != "old-loop" && *to != "nova-sprint" {
		return refuse(errOut, "land writer", fmt.Sprintf("--to must be old-loop or nova-sprint, got %q", *to))
	}

	addr := *redisAddr
	if addr == "" {
		addr = os.Getenv("NOVA_REDIS_ADDR")
	}
	if addr == "" {
		addr = os.Getenv("REDIS_ADDR")
	}
	if addr == "" {
		return refuse(errOut, "land writer", "--redis <addr> is required")
	}

	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint land writer: %v\n", err)
		return 6
	}
	defer st.Close()
	client := st.Client()

	if _, _, err := fn.Ensure(ctx, client); err != nil {
		fmt.Fprintf(errOut, "nova-sprint land writer: %v\n", err)
		return 6
	}

	actor := *by
	if actor == "" {
		actor = os.Getenv("USER")
		if actor == "" {
			actor = "emma"
		}
	}

	if *to == "" {
		*to = "get"
	}
	fargs := []interface{}{*repo, *base, *to, actor}
	if *inflight >= 0 {
		fargs = append(fargs, strconv.Itoa(*inflight))
	}

	res, err := client.FCall(ctx, "ns_writer", nil, fargs...).Slice()
	if err != nil {
		return refuse(errOut, "land writer", err.Error())
	}
	if len(res) == 0 {
		return refuse(errOut, "land writer", "empty reply from ns_writer")
	}
	status := fmt.Sprint(res[0])
	if status == "REFUSED" {
		reason := "refused"
		if len(res) > 1 {
			reason = fmt.Sprint(res[1])
		}
		detail := ""
		if len(res) > 2 {
			detail = fmt.Sprint(res[2])
		}
		if reason == "pub" {
			fmt.Fprintf(errOut, "REFUSED pub=%s remedy=resolve the publisher's intent (ns_pub_state dead, or ns_land) before rollback\n", oneline.Field(detail))
			return 2
		}
		fmt.Fprintf(errOut, "REFUSED %s count=%s remedy=drain the old loop before cutover\n", oneline.Field(reason), oneline.Field(detail))
		return 2
	}
	if status != "OK" {
		return refuse(errOut, "land writer", fmt.Sprintf("unexpected status %s", status))
	}

	gen := fmt.Sprint(res[1])
	owner := fmt.Sprint(res[2])
	fmt.Fprintf(out, "WRITER repo=%s base=%s gen=%s owner=%s\n", *repo, *base, oneline.Field(gen), oneline.Field(owner))
	return 0
}
