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
// Rollback: land writer --repo <r> --base <b> --to old-loop bumps gen after resolving pub:*.
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

	writerKey := fmt.Sprintf("land:%s:%s:writer", *repo, *base)

	if *to == "" {
		vals, err := client.HGetAll(ctx, writerKey).Result()
		if err != nil {
			return refuse(errOut, "land writer", err.Error())
		}
		gen := vals["gen"]
		owner := vals["owner"]
		if gen == "" {
			gen = "0"
		}
		if owner == "" {
			owner = "old-loop"
		}
		fmt.Fprintf(out, "WRITER repo=%s base=%s gen=%s owner=%s\n", *repo, *base, oneline.Field(gen), oneline.Field(owner))
		return 0
	}

	keys := []string{
		writerKey,
		fmt.Sprintf("land:%s:events", *repo),
		fmt.Sprintf("land:%s:%s:inflight", *repo, *base),
	}
	var fargs []interface{}
	fargs = append(fargs, *to, actor)
	if *inflight >= 0 {
		fargs = append(fargs, strconv.Itoa(*inflight))
	}

	res, err := client.FCall(ctx, "ns_writer", keys, fargs...).Slice()
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
